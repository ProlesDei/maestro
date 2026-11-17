package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// A chain is the fixed next step a run's end starts itself: a build in
// review goes to review, a GO to QA, a send-back to its engineer, a
// Rereview: to review. Each of those was a line a bot passed to another
// bot, which woke, reread its chat and ran job: Grok spent a turn on each
// and decided nothing (owner's ruling, Nov 15 2026). It picks no new work,
// and whatever it can't start stays for the launcher's pickup, as before.
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
	notify := filepath.Join(filepath.Dir(dir), "notify")
	now := time.Now().UTC().Round(time.Millisecond)
	var chained []string
	done, toPlanner := false, false
	if chainJobs[r.Job] {
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
		if rt.Outcome == outDone {
			// As pickup would: a job a look handed out lately is on its way.
			sends := pickupSends(filepath.Join(filepath.Dir(dir), "handouts"), r.ID, r.StartedAt, rt.Send, now)
			left = []send{}
			for _, sd := range sends {
				if c.handOn(ctx, dir, notify, r, sd) {
					chained = append(chained, sd.Bot+": "+sd.Line)
				} else {
					left = append(left, sd)
				}
			}
		}
		rt.Send = left
		// A launcher with no webhook, such as a person, reads its pickup:
		// leave it what it would have seen.
		bot := hasHook(notify, r.Launcher)
		rest := leftForBot(rt)
		done = bot && len(left) == 0 && !rest && ackable(before)
		toPlanner = bot && len(left) > 0 && !rest && r.Launcher != "planner" &&
			!slices.ContainsFunc(left, func(sd send) bool { return sd.Bot != "planner" }) && hasHook(notify, "planner")
	}
	// One save, after every start: no reader sees the route before the
	// handoffs it names have started.
	rec, err := runs.Load(dir, r.ID)
	if err != nil {
		return
	}
	rec.Chained = chained
	if rec.Routed, err = json.Marshal(rt); err != nil {
		return
	}
	if toPlanner {
		rec.Notify = "planner"
	}
	if runs.Save(dir, rec) == nil && done {
		runs.Ack(dir, r.ID, chainBy, now)
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

// runDo runs one do: line of r's route as r's launcher would, and reports
// whether the work goes on: started now, or by another start of the same
// line; and whether the line is gone for good, its ticket moved on. It
// runs none for a launcher with no webhook, such as a person, who reads
// its pickup, and none that names another run. Any other refusal, such as
// a pause, leaves the line for the launcher.
func (c *call) runDo(ctx context.Context, dir, notify string, r runs.Record, line string) (on, gone bool) {
	m := doGrammar.FindStringSubmatch(line)
	if m == nil || !hasHook(notify, r.Launcher) {
		return false, false
	}
	cc := *c
	cc.actor, cc.runCache = r.Launcher, nil
	var err error
	switch {
	case m[1] != "":
		if m[1] != r.ID {
			return false, false
		}
		_, err = cc.resume(ctx, resumeOpts{run: m[1], died: m[2] == "died", goOn: m[2] == "go-on"})
	default:
		if m[3] != r.ID {
			return false, false
		}
		var lim time.Duration
		if m[4] != "" {
			if lim, err = time.ParseDuration(m[4]); err != nil {
				return false, false
			}
		}
		_, err = cc.again(ctx, againOpts{run: m[3], limit: lim})
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
	k, id, ok := nextStep(r, sd)
	if !ok || !hasHook(notify, sd.Bot) {
		return false
	}
	cc := *c
	cc.actor, cc.runCache = sd.Bot, nil
	req := jobReq{kind: k, ids: []string{id}, chainOf: r.ID, hops: r.Hops + 1}
	// QA runs at once: rules set qa_slots's, which startJob reads, else
	// what the bot passed last.
	if k.slot != "" && c.boardQASlots() == 0 {
		req.slots = lastSlots(dir, sd.Bot, k.slot)
	}
	_, _, _, err := cc.startJob(ctx, req)
	var a alreadyGoing
	return err == nil || errors.As(err, &a)
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

// hasHook reports whether bot has a webhook to ring.
func hasHook(notify, bot string) bool {
	h, err := runs.Webhook(notify, bot)
	return err == nil && h.URL != ""
}

// lastSlots is the slot count bot passed on its newest run in slot name,
// as its memories set it; 0, the default, if none of its runs did.
func lastSlots(dir, bot, name string) int {
	list, _ := runs.List(dir, time.Now(), true)
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
