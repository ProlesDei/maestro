package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// A chain is the fixed next step a run's end starts itself: a build in
// review goes to review, a GO to QA, a send-back to its engineer, a
// Rereview: to review. Each of those was a line a bot passed to another
// bot, which woke, reread its chat and ran job: Grok spent a turn on each
// and decided nothing (owner's ruling, Nov 15 2026). New work it picks
// only through refill, the planner's look without the judge; whatever it
// can't start stays for the launcher's pickup, as before.
var (
	chainJobs = map[string]bool{"build": true, "review": true, "qa": true, "live": true}
	chainBots = map[string]bool{"junior-engineer": true, "senior-engineer": true, "reviewer": true, "qa-lead": true}
	chainLine = regexp.MustCompile(`^(build|review|qa) (T-[0-9]+)$`)
)

// maxHops is how many ends in a row may each start the next step: past
// it, a bot looks. A ticket's tries end a send-back loop well before.
const maxHops = 10

// chainLimit bounds a chain, so a start that hangs never holds the ring.
const chainLimit = 2 * time.Minute

// chainBy is who acks a run whose end left nothing for a bot to do.
const chainBy = "chain"

// chain runs in a job run's supervisor once the run ended, before the
// first ring, while the record is settling. It routes the run as pickup
// would and stores the route, so the ring's body and every pickup show
// the same lines. It runs a fixed do: line itself first, as the launcher
// (a retry, a died run's resume), so no bot wakes to type it. For a run
// that ended done, it starts each fixed handoff itself, as the bot it was
// for. What is left only tells the planner something goes to the
// planner; a run that leaves nothing to do is acked, and rings no one. A
// run that ended short with nothing routed is never acked here.
func (c *call) chain(ctx context.Context, dir string, r runs.Record) {
	if r.Job == "" {
		return
	}
	s, err := c.open(ctx)
	if err != nil {
		return
	}
	rt, err := c.route(ctx, s, r)
	s.Close()
	if err != nil {
		return
	}
	co, err := c.coord()
	if err != nil {
		return
	}
	notify := filepath.Join(filepath.Dir(dir), "notify")
	now := time.Now().UTC().Round(time.Millisecond)
	var chained []string
	done, toPlanner := false, false
	if chainJobs[r.Job] || codeRole(notify, r.Launcher) {
		before := rt
		// First, so a retry runs before anything counts its slot free.
		keep := []string{}
		for _, l := range rt.Do {
			switch on, gone := c.runDo(ctx, dir, notify, r, l); {
			case on:
				chained = append(chained, r.Launcher+": "+l)
			case !gone:
				keep = append(keep, l)
			}
		}
		rt.Do = keep
		left := rt.Send
		unsure := false
		// A run whose work landed before it ended short hands on the same:
		// a line left for a bot to pass could meet a pause set meanwhile.
		if rt.Outcome == outDone || rt.Landed {
			// As pickup would: a job a look handed out lately is on its way.
			// A store that can't say starts nothing and stays the
			// launcher's: its pickup shows why.
			sends, err := co.pickupSends(r.ID, r.StartedAt, rt.Send, now)
			if err != nil {
				sends, unsure = nil, true
			} else {
				left = []send{}
			}
			// A recent handout alone cannot acknowledge a runtime handoff.
			// Let its action receipt settle the race with another sender.
			for _, sd := range rt.Send {
				if codeRole(notify, sd.Bot) && !slices.Contains(sends, sd) {
					sends = append(sends, sd)
				}
			}
			for _, sd := range sends {
				if c.handOn(ctx, dir, notify, r, sd) {
					chained = append(chained, sd.Bot+": "+sd.Line)
				} else {
					left = append(left, sd)
				}
			}
		}
		// A freed slot or a merge woke the planner only to look: the end
		// looks itself, and wakes it only for what that look leaves
		// (owner's ruling, Nov 17 2026).
		// A code planner resolves a delivery dispute by judging its current board rows.
		if !unsure && r.BoardID != "" && codeRole(notify, "planner") {
			remaining := make([]send, 0, len(left))
			for _, sd := range left {
				if sd.Bot == "planner" && !(strings.HasPrefix(sd.Line, "goal ") && strings.HasSuffix(sd.Line, " done")) {
					if err := c.savePlannerWake(co, dir, r.ID, sd.Line); err == nil {
						chained = append(chained, "planner: wake saved")
						continue
					}
				}
				remaining = append(remaining, sd)
			}
			left = remaining
		}
		if !unsure && slices.ContainsFunc(left, refillLine) && served(notify, "planner") && c.refill(ctx, dir) {
			left = slices.DeleteFunc(left, refillLine)
		}
		if r.BoardID != "" {
			remaining := left[:0]
			for _, sd := range left {
				if (sd.Bot == "chief-of-staff" || sd.Bot == "planner" && strings.HasPrefix(sd.Line, "goal ") && strings.HasSuffix(sd.Line, " done")) && c.queueGoalItem(ctx, notify, r, sd) == nil {
					chained = append(chained, "chief-of-staff: inbox")
					continue
				}
				remaining = append(remaining, sd)
			}
			left = remaining
		}
		rt.Send = left
		// A launcher with no webhook, such as a person, reads its pickup:
		// leave it what it would have seen.
		bot := served(notify, r.Launcher)
		rest := leftForBot(rt)
		done = bot && !unsure && len(left) == 0 && !rest && ackable(before)
		toPlanner = bot && !unsure && len(left) > 0 && !rest && r.Launcher != "planner" &&
			!slices.ContainsFunc(left, func(sd send) bool { return sd.Bot != "planner" }) && served(notify, "planner")
	}
	// One save, after every start: no reader sees the route before the
	// handoffs it names have started.
	rec, err := runs.Load(dir, r.ID)
	if err != nil {
		return
	}
	// Routed and chained: settled, so a stop that lands now leaves no
	// reader to chain it again.
	rec.Chained, rec.Settling = chained, false
	if rec.Routed, err = json.Marshal(rt); err != nil {
		return
	}
	if toPlanner {
		rec.Notify = "planner"
	}
	if runs.Save(dir, rec) != nil {
		return
	}
	if done {
		runs.Ack(dir, r.ID, chainBy, now)
	}
	// On a board machines share, the launch says it too, before the ring
	// sends the planner to look from wherever it runs.
	if host, _ := c.hostID(); toPlanner && rec.LaunchID > 0 && host != "" {
		b, err := c.board()
		if err == nil {
			err = b.NotifyLaunch(ctx, rec.LaunchID, "planner")
		}
		if err != nil {
			fmt.Fprintf(c.env.Stderr, "maestro-ticket: run %s: the planner's look elsewhere holds its ticket a stall: %v\n", r.ID, err)
		}
	}
}

// ackable reports whether a route, as first made, may be acked once
// nothing is left of it: a run that ended done, or one whose end routed
// something. A run that ended short with nothing routed stays for its
// launcher, never acked unseen.
func ackable(rt routed) bool {
	return rt.Outcome == outDone || len(rt.Send)+len(rt.Do) > 0 || rt.Reply != "" || rt.quiet
}

// doGrammar is every do: line a run's end runs itself: a died run's one
// resume, a limited one's go-on, and one job again. Nothing else, and
// never a rules write: the supervisor runs outside any run, as the
// launcher, so a looser grammar would let a reply set what only a bot
// outside a run may.
var doGrammar = regexp.MustCompile(`^maestro-ticket (?:resume (R-[a-z2-7]{8}) --(died|go-on)|job again (R-[a-z2-7]{8})(?: --limit ([0-9]+[hms](?:[0-9]+[ms]){0,2}))?)$`)

var runtimeDo = regexp.MustCompile(`^maestro-ticket (?:resume (R-[a-z2-7]{8}) --(died|go-on|opus|escalate|decide)|job again (R-[a-z2-7]{8})(?: (--full|--limit [0-9]+[hms](?:[0-9]+[ms]){0,2}))?)$`)

// runDo runs one do: line of r's route as r's launcher would, and reports
// whether the work goes on: started now, or by another start of the same
// line; and whether the line is gone for good, its ticket moved on. It
// runs none for a launcher with no webhook, such as a person, who reads
// its pickup, and none that names another run. Any other refusal, such as
// a pause, leaves the line for the launcher.
func (c *call) runDo(ctx context.Context, dir, notify string, r runs.Record, line string) (on, gone bool) {
	grammar := doGrammar
	if codeRole(notify, r.Launcher) {
		grammar = runtimeDo
	}
	m := grammar.FindStringSubmatch(line)
	if m == nil || !served(notify, r.Launcher) {
		return false, false
	}
	cc := *c // the route's list of past runs serves its resume too
	cc.actor = r.Launcher
	var err error
	switch {
	case m[1] != "":
		if m[1] != r.ID {
			return false, false
		}
		_, err = cc.resume(ctx, resumeOpts{run: m[1], died: m[2] == "died", goOn: m[2] == "go-on", opus: m[2] == "opus", escalate: m[2] == "escalate", decide: m[2] == "decide"})
	default:
		if m[3] != r.ID {
			return false, false
		}
		var lim time.Duration
		if m[4] != "" && m[4] != "--full" {
			if lim, err = time.ParseDuration(strings.TrimPrefix(m[4], "--limit ")); err != nil {
				return false, false
			}
		}
		_, err = cc.again(ctx, againOpts{run: m[3], limit: lim, full: m[4] == "--full"})
	}
	var a alreadyGoing
	if errors.As(err, &a) {
		// Only a start that exists carries the work on.
		_, gerr := runs.Get(dir, a.run, time.Now())
		return gerr == nil, false
	}
	return err == nil, errors.As(err, new(movedOn))
}

// leftForBot reports whether a route holds more than sends for a bot to
// act on: a command to run, a reply to read, or a write that failed, such
// as follow-ups not filed, which would be lost in a run acked unseen.
func leftForBot(rt routed) bool {
	return len(rt.Do) > 0 || rt.Reply != "" || slices.ContainsFunc(rt.Wrote, func(w string) bool {
		return strings.HasPrefix(w, "follow-ups not filed") || strings.HasPrefix(w, "not parked")
	})
}

// refileFollowUps tries again the follow-ups a stored route failed to
// file, as routing the run again on each pickup did before it was stored.
func (c *call) refileFollowUps(ctx context.Context, s ticket.Store, r runs.Record, rt *routed) {
	i := slices.IndexFunc(rt.Wrote, func(w string) bool { return strings.HasPrefix(w, "follow-ups not filed") })
	if i < 0 {
		return
	}
	tid, err := ticket.ParseID(r.Ticket)
	if err != nil {
		return
	}
	d, err := s.Get(ctx, tid)
	if err != nil {
		return
	}
	rt.Wrote = slices.Delete(rt.Wrote, i, i+1)
	if err := c.fileFollowUps(ctx, s, r, d, rt); err != nil {
		rt.Wrote = append(rt.Wrote, "follow-ups not filed: "+err.Error())
	}
}

// handOn starts one send's job as the bot it was for, and reports whether
// that job now runs: started, or already going. It starts none for a bot
// with no webhook, which would never hear how its run ended.
func (c *call) handOn(ctx context.Context, dir, notify string, r runs.Record, sd send) bool {
	req, ok := stepRequest(notify, r, sd)
	if !ok || !served(notify, sd.Bot) {
		return false
	}
	req.chainOf, req.hops = r.ID, r.Hops+1
	if !codeRole(notify, sd.Bot) {
		_, err := c.startAs(ctx, dir, sd.Bot, req)
		var a alreadyGoing
		return err == nil || errors.As(err, &a)
	}
	co, err := c.coord()
	if err != nil {
		return false
	}
	req.actionID = strings.Join([]string{r.ID, sd.Bot, req.kind.name, req.ids[0]}, ".")
	if req.kind.name == "qa" {
		d, err := c.detailForJob(ctx, req.ids[0])
		if err != nil {
			return false
		}
		if review, head, _ := recoveryProof(d); head != "" {
			req.actionID = qaReceiptID(req.ids[0], review)
		} else {
			return false
		}
	}
	req.actionHash = fmt.Sprintf("%x", sha256.Sum256([]byte(sd.Line)))
	if m, err := co.s.read("receipts", req.actionID); err == nil {
		hash, id, ok := strings.Cut(m.body, " ")
		return ok && hash == req.actionHash && runs.ValidID(id)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	child, err := c.startAs(ctx, dir, sd.Bot, req)
	if err != nil {
		var going alreadyGoing
		if !errors.As(err, &going) || !runs.ValidID(going.run) {
			return false
		}
		child, err = runs.Load(dir, going.run)
		if err != nil {
			return false
		}
	}
	return child.ActionID == req.actionID && child.ActionHash == req.actionHash && child.PID != 0 && saveActionReceipt(co, child) == nil
}

// Receipts outlive pruned run records. A reused ID must identify the same action and run.
func saveActionReceipt(co coord, r runs.Record) error {
	body := r.ActionHash + " " + r.ID
	err := co.s.create("receipts", r.ActionID, body)
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	m, err := co.s.read("receipts", r.ActionID)
	if err != nil {
		return err
	}
	if m.body != body {
		return errors.New("handoff receipt conflicts with an earlier action")
	}
	return nil
}

// startAs starts req as bot, so the run rings bot and its end chains as
// bot's would.
func (c *call) startAs(ctx context.Context, dir, bot string, req jobReq) (runs.Record, error) {
	if c.runCache == nil {
		c.runCache = &pastRuns{} // made before the copy, so each start reads it once
	}
	cc := *c
	cc.actor = bot
	req.inherit = true
	// QA runs at once: rules set qa_slots's, which startJob reads, else
	// what the bot passed last.
	if req.kind.slot != "" && c.boardQASlots() == 0 {
		req.slots = lastSlots(cc.allRuns(), bot, req.kind.slot)
	}
	r, _, _, err := cc.startJob(ctx, req)
	return r, err
}

// refillLine is a line that woke the planner only to look: a freed build
// slot, or a merge that frees what waited on it.
func refillLine(sd send) bool {
	return sd.Bot == "planner" && (strings.HasPrefix(sd.Line, "slot free ") || strings.HasPrefix(sd.Line, "merged "))
}

// nextStep is the job and ticket of a send that is a fixed handoff to a
// worker bot, which r's end may start itself.
func nextStep(r runs.Record, sd send) (jobKind, string, bool) {
	m := chainLine.FindStringSubmatch(sd.Line)
	if m == nil || !chainBots[sd.Bot] || r.Hops >= maxHops {
		return jobKind{}, "", false
	}
	k, ok := findJob(m[1])
	return k, m[2], ok
}

var runtimeStep = regexp.MustCompile(`^(build|review|qa|live|design) (T-[0-9]+)(?: --approved ([0-9a-f]{7,40}))?(?: --drop ([A-Za-z0-9._,-]+): ([^\r\n]+))?$`)

func codeRole(dir, bot string) bool {
	h, err := runs.Webhook(dir, bot)
	return err == nil && h.Code
}

func stepRequest(notify string, r runs.Record, sd send) (jobReq, bool) {
	if !codeRole(notify, sd.Bot) {
		k, id, ok := nextStep(r, sd)
		return jobReq{kind: k, ids: []string{id}}, ok
	}
	m := runtimeStep.FindStringSubmatch(sd.Line)
	if m == nil || r.Hops >= maxHops {
		return jobReq{}, false
	}
	roles := map[string]string{"review": "reviewer", "qa": "qa-lead", "live": "qa-lead", "design": "designer"}
	if m[1] == "build" {
		if sd.Bot != "junior-engineer" && sd.Bot != "senior-engineer" {
			return jobReq{}, false
		}
	} else if sd.Bot != roles[m[1]] {
		return jobReq{}, false
	}
	if m[3] != "" || m[4] != "" {
		if r.Job != "judge" || m[1] != "review" || (m[4] != "" && !dropPattern.MatchString(m[4])) {
			return jobReq{}, false
		}
	}
	k, _ := findJob(m[1])
	return jobReq{kind: k, ids: []string{m[2]}, approved: m[3], drop: m[4], dropWhy: m[5]}, true
}

// served reports whether the role has a configured server.
func served(notify, bot string) bool {
	h, err := runs.Webhook(notify, bot)
	return err == nil && h.Served()
}

// lastSlots is the slot count bot passed on its newest run in slot name,
// as its memories set it; 0, the default, if none of its runs did.
func lastSlots(list []runs.Record, bot, name string) int {
	var at time.Time
	n := 0
	for _, r := range list {
		if r.Launcher != bot || !r.StartedAt.After(at) {
			continue
		}
		if i := slices.Index(r.Command, "slot"); i >= 0 && flagValue(r.Command[i:], "--name") == name {
			if v, err := strconv.Atoi(flagValue(r.Command[i:], "--max")); err == nil && v > 0 {
				n, at = v, r.StartedAt
			}
		}
	}
	return n
}

// lastOverrides are the --model, --effort and --limit the actor's newest
// job run of kind job started with: what it typed, as its memories say,
// or what a start code made for it carried on. A resume or a job again
// copies its run's command, not the actor's choice, so it counts for
// none.
func (c *call) lastOverrides(job string) (model, effort string, limit time.Duration, err error) {
	list, err := c.pastRuns()
	if err != nil {
		return "", "", 0, err
	}
	var last *runs.Record
	for _, r := range list {
		if r.Launcher == c.actor && r.Job == job && r.ResumeOf == "" && r.AgainOf == "" && (last == nil || r.StartedAt.After(last.StartedAt)) {
			last = &r
		}
	}
	if last == nil {
		return "", "", 0, nil
	}
	model, effort = flagValue(last.Overrides, "--model"), flagValue(last.Overrides, "--effort")
	limit, _ = time.ParseDuration(flagValue(last.Overrides, "--limit"))
	return model, effort, limit, nil
}

func (c *call) queueGoalItem(ctx context.Context, notify string, r runs.Record, sd send) error {
	goal := "all"
	fields := strings.Fields(sd.Line)
	if len(fields) == 3 && fields[0] == "goal" && fields[2] == "done" && ticket.ValidateGoal(fields[1]) == nil {
		goal = fields[1]
	} else if len(fields) >= 3 && fields[0] == "eta" && fields[1] == "missed:" && ticket.ValidateGoal(fields[2]) == nil {
		goal = fields[2]
	} else if id := ticketInLine.FindString(sd.Line); id != "" {
		t, err := c.ticketForJob(ctx, id)
		if err != nil {
			return err
		}
		if t.Goal != "" {
			goal = t.Goal
		}
	} else if r.Delivery != "" {
		goal, _, _ = strings.Cut(r.Delivery, "/")
	} else if r.Ticket != "" {
		t, err := c.ticketForJob(ctx, r.Ticket)
		if err != nil {
			return err
		}
		if t.Goal != "" {
			goal = t.Goal
		}
	}
	b, err := c.board()
	if err != nil {
		return err
	}
	id := fmt.Sprintf("item-%x", sha256.Sum256([]byte(goal+"\n"+sd.Line)))
	if err := b.PutGoalItem(ctx, id, goal, sd.Line); err != nil {
		return err
	}
	h, err := runs.Webhook(notify, "chief-of-staff")
	if err == nil && h.Exec != "" {
		return c.wakeGoalInbox(ctx, b, filepath.Join(filepath.Dir(notify), "runs"), h)
	}
	return nil
}

// A successful invocation can still leave new items behind the private wake gate.
func (c *call) wakeGoalInbox(ctx context.Context, b *mongostore.Store, dir string, h runs.Hook) error {
	err := runs.Ring(ctx, h, runs.Notification{})
	pending, checkErr := b.HasPendingGoalItem(ctx)
	if err != nil || checkErr != nil || pending {
		if e := c.scheduleReconsider(filepath.Dir(dir), time.Now().Add(runs.GateWindow)); e != nil {
			return errors.Join(err, checkErr, e)
		}
	}
	return errors.Join(err, checkErr)
}

// Replay only the remaining fixed actions. Each start checks its original
// receipt or resume mark. The saved route keeps the original reset time.
func (c *call) replayCode(ctx context.Context, dir, notify string, r runs.Record) error {
	if r.Job == "" || runs.Acked(dir, r.ID) || r.Settling {
		return nil
	}
	var rt routed
	if len(r.Routed) == 0 || json.Unmarshal(r.Routed, &rt) != nil {
		return nil
	}
	cc := *c
	cc.actor, cc.runCache = r.Launcher, nil
	if err := cc.mayStart(); err != nil {
		if errors.Is(err, ticket.ErrStopped) || errors.Is(err, ticket.ErrPaused) {
			return nil
		}
		return err
	}
	done := true
	for _, line := range rt.Do {
		on, gone := cc.runDo(ctx, dir, notify, r, line)
		if !on && !gone {
			done = false
		}
	}
	for _, sd := range rt.Send {
		switch {
		case sd.Bot == "chief-of-staff" || sd.Bot == "planner" && strings.HasPrefix(sd.Line, "goal ") && strings.HasSuffix(sd.Line, " done"):
			if err := cc.queueGoalItem(ctx, notify, r, sd); err != nil {
				return err
			}
		case sd.Bot == "planner":
			co, err := cc.coord()
			if err != nil {
				return err
			}
			if !codeRole(notify, "planner") {
				done = false
				continue
			}
			if err := cc.savePlannerWake(co, dir, r.ID, sd.Line); err != nil {
				return err
			}
		default:
			if !cc.handOn(ctx, dir, notify, r, sd) {
				done = false
			}
		}
	}
	if outcome, _ := routeOutcome(r); r.Delivery != "" && r.State == runs.Done && !r.IsError && outcome == outDone {
		b, err := cc.board()
		if err != nil {
			return err
		}
		if err := b.CompleteDelivery(ctx, r.Delivery, r.ActionHash, r.DeliveryOwner(), r.ID, r.Reply); err != nil {
			return err
		}
		rt.Reply = ""
	}
	if done && rt.Reply == "" && (len(rt.Send)+len(rt.Do) > 0 || rt.Outcome == outDone) {
		_, err := runs.Ack(dir, r.ID, chainBy, time.Now())
		return err
	}
	return nil
}

// A pending planner wake survives a failed timer, a restart or a dirty lease.
// Its stable key distinguishes a second event on the same ticket.
func (c *call) savePlannerWake(co coord, dir, run, line string) error {
	id := fmt.Sprintf("%s-%x", run, sha256.Sum256([]byte(line)))
	if err := co.s.create("planner-pending", id, line); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return err
	}
	return c.scheduleReconsider(filepath.Dir(dir), time.Now())
}
