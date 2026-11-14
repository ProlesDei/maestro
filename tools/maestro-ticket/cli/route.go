package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// Run outcomes, as pickup sorts an ended job.
const (
	outDone       = "done"
	outQuestion   = "question"
	outUsageLimit = "usage_limit"
	outDiskFull   = "disk_full"
	outTimedOut   = "timed_out"
	outDied       = "died"
	outRefused    = "refused"
	outFailed     = "failed"
)

// send is one message for a bot to send: Line to Bot.
type send struct {
	Bot  string `json:"bot"`
	Line string `json:"line"`
}

func (s send) String() string { return "send " + s.Bot + ": " + s.Line }

// routed is what pickup makes of one ended job: what happened, what the
// ticket shows, and what the bot does next. A bot reads this, not the
// reply.
type routed struct {
	Run      string   `json:"run"`
	Job      string   `json:"job"`
	Ticket   string   `json:"ticket,omitempty"`
	Outcome  string   `json:"outcome"`
	Reset    string   `json:"reset,omitempty"`  // when a usage limit lifts, as the reply said
	Status   string   `json:"status,omitempty"` // the ticket's status now
	First    string   `json:"first"`            // the reply's first line
	Mismatch string   `json:"mismatch,omitempty"`
	Send     []send   `json:"send"`
	Do       []string `json:"do"`
	Wrote    []string `json:"wrote"`
	// Reply is the whole reply, shown only when the bot must read it: a
	// question, a failure, or a text job whose reply is the answer.
	Reply string `json:"reply,omitempty"`
}

var (
	// Claude Code's own words for a plan's limit, in an error reply, or
	// opening a reply. Text that only talks about limits, as a build that
	// adds a rate limit or a gh error in stderr does, is no limit.
	usageLimitText  = regexp.MustCompile(`(?i)(usage limit|hit your limit|limit reached|out of (extra )?usage)`)
	usageLimitExact = regexp.MustCompile(`(?i)^(claude ai usage limit reached|you've hit your (session |weekly |usage )?limit|(5-hour|weekly|session) limit reached)`)
	resetText       = regexp.MustCompile(`(?i)\bresets?\s+(?:at\s+)?([^\n.;]+)`)
	resetEpoch      = regexp.MustCompile(`limit reached\|(\d{10})`)
	diskFullText    = regexp.MustCompile(`(?i)(infra block: disk full|infra block: low_disk)`)
	diskFailText    = regexp.MustCompile(`(?i)(\blow_disk\b|no space left on device)`)
	refusedText     = regexp.MustCompile(`(?i)^[a-z][a-z -]*: refused\b`)
	fullModelText   = regexp.MustCompile(`^review: stopped: tier \d+ needs the full model`)
	hexSHA          = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	shortSHA        = regexp.MustCompile(`\bat ([0-9a-f]{7,40})\b`)
)

// headLine is a reply's first line, cut to 200 bytes, since a bot reads
// it in place of the reply.
func headLine(s string) string {
	l := firstLine(s)
	if len(l) > 200 {
		l = strings.ToValidUTF8(l[:200], "") + "…"
	}
	return l
}

// lastLine is the reply's last line with text.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// outcomeOf sorts an ended run by its state and reply.
func outcomeOf(r runs.Record) (outcome, reset string) {
	text := r.Reply + "\n" + r.Error
	failed := r.IsError || r.State != runs.Done
	switch {
	case r.State == runs.Died:
		return outDied, ""
	case r.State == runs.TimedOut:
		return outTimedOut, ""
	case r.IsError && usageLimitText.MatchString(firstLine(r.Reply)) || usageLimitExact.MatchString(firstLine(r.Reply)):
		if m := resetEpoch.FindStringSubmatch(text); m != nil {
			var sec int64
			fmt.Sscan(m[1], &sec)
			return outUsageLimit, time.Unix(sec, 0).UTC().Format(time.RFC3339)
		}
		if m := resetText.FindStringSubmatch(text); m != nil {
			return outUsageLimit, strings.TrimSpace(m[1])
		}
		return outUsageLimit, ""
	case diskFullText.MatchString(text) || failed && diskFailText.MatchString(text):
		return outDiskFull, ""
	case r.State == runs.Failed || r.IsError:
		return outFailed, ""
	case refusedText.MatchString(firstLine(r.Reply)):
		return outRefused, ""
	case strings.HasSuffix(lastLine(r.Reply), "?") && !strings.HasPrefix(lastLine(r.Reply), "up:"):
		return outQuestion, ""
	}
	return outDone, ""
}

// latest is a ticket's newest comment, or the zero comment.
func latest(d ticket.Detail) ticket.Comment {
	if len(d.Comments) == 0 {
		return ticket.Comment{}
	}
	return d.Comments[len(d.Comments)-1]
}

// latestWith is the newest comment that starts with prefix.
func latestWith(d ticket.Detail, prefix string) (ticket.Comment, bool) {
	for i := len(d.Comments) - 1; i >= 0; i-- {
		if strings.HasPrefix(d.Comments[i].Body, prefix) {
			return d.Comments[i], true
		}
	}
	return ticket.Comment{}, false
}

// shaIn is the commit a note names: a full sha, else the one after "at".
func shaIn(body string) string {
	line := firstLine(body)
	if m := hexSHA.FindString(line); m != "" {
		return m
	}
	if m := shortSHA.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	if m := hexSHA.FindString(body); m != "" {
		return m
	}
	return ""
}

// sameEngineer is the bot that last claimed the ticket as an engineer:
// the part before the / in the holder of its newest claimed event that
// starts junior-engineer/ or senior-engineer/; "" if none did.
func sameEngineer(ctx context.Context, s ticket.Store, id ticket.ID) (string, error) {
	evs, err := s.Events(ctx, ticket.EventFilter{TicketID: id, Type: ticket.EventClaimed})
	if err != nil {
		return "", err
	}
	for i := len(evs) - 1; i >= 0; i-- {
		var d struct{ Holder string }
		json.Unmarshal(evs[i].Data, &d)
		if bot := ticket.HolderBot(d.Holder); bot == "junior-engineer" || bot == "senior-engineer" {
			return bot, nil
		}
	}
	return "", nil
}

// kickbackRoute says who builds a sent-back ticket next: the same engineer
// at TRIES 0 or 1; a junior's ticket goes to the senior at 2; a senior's at
// 2, and any at 3 or more, go to the planner, which splits, rewrites or
// sends it up.
func kickbackRoute(ctx context.Context, s ticket.Store, t ticket.Ticket) (send, error) {
	eng, err := sameEngineer(ctx, s, t.ID)
	if err != nil {
		return send{}, err
	}
	if eng == "" {
		eng = "senior-engineer"
		if t.Tier == ticket.TierJunior {
			eng = "junior-engineer"
		}
	}
	switch {
	case t.Tries <= 1:
		return send{eng, "build " + t.ID.String()}, nil
	case t.Tries == 2 && eng == "junior-engineer":
		return send{"senior-engineer", "build " + t.ID.String()}, nil
	}
	return send{"planner", fmt.Sprintf("kickback %s tries %d", t.ID, t.Tries)}, nil
}

// readyWork reports whether a build waits for a free slot.
func readyWork(ctx context.Context, s ticket.Store) (bool, error) {
	l, err := s.List(ctx, ticket.ListFilter{Statuses: []ticket.Status{ticket.StatusTodo}, Ready: true, Limit: 1})
	return len(l) > 0, err
}

// goalDone reports whether a goal has no open ticket left.
func goalDone(ctx context.Context, s ticket.Store, goal string) (bool, error) {
	if goal == "" {
		return false, nil
	}
	l, err := s.List(ctx, ticket.ListFilter{Goal: goal, Statuses: openStatuses})
	for _, t := range l {
		if !strings.HasPrefix(t.Title, "Follow-ups: ") { // made with a goal before they took none
			return false, err
		}
	}
	return true, err
}

// textJobs reply with what the bot asked for: it reads the reply.
var textJobs = map[string]bool{"plan": true, "rule": true, "finish": true, "judge": true, "cos": true, "qa-ask": true}

// route sorts one ended job and says what comes next. It writes to the
// ticket only for the fixed follow-up a timed-out review needs, keyed so
// a second pickup of the same run writes nothing new.
func (c *call) route(ctx context.Context, s ticket.Store, r runs.Record) (routed, error) {
	out := routed{Run: r.ID, Job: r.Job, Ticket: r.Ticket, First: headLine(r.Reply), Send: []send{}, Do: []string{}, Wrote: []string{}}
	out.Outcome, out.Reset = outcomeOf(r)
	if out.First == "" {
		out.First = headLine(r.Error)
	}
	if out.First == "" && r.Exit != nil {
		out.First = fmt.Sprintf("exit %d, no reply", *r.Exit)
	}
	planner := func(line string) { out.Send = append(out.Send, send{"planner", line}) }
	id := r.Ticket
	// A judge run that gave no answer frees its rows for the next look,
	// unless it died: then its resume works them.
	if r.Job == "judge" && out.Outcome != outDone && !(out.Outcome == outDied && !r.AfterDeath) {
		if dir, err := c.besideStore("handouts"); err == nil {
			for _, tk := range r.Tickets {
				os.Remove(handoutPath(dir, tk, "judge"))
			}
		}
	}

	switch out.Outcome {
	case outUsageLimit:
		// The limit is the team's plan's: no bot starts a run until it
		// lifts. A reset pickup can't read waits an hour.
		ended := time.Now()
		if r.EndedAt != nil {
			ended = *r.EndedAt // so a second pickup of the run sets the same pause
		}
		until := ended.Add(time.Hour)
		if t, err := time.Parse(time.RFC3339, out.Reset); err == nil {
			until = t
		}
		if store, _, err := c.storePath(); err == nil && until.After(time.Now()) {
			// One line to the planner per pause, not one per stopped run.
			if setPause(filepath.Dir(store), pause{Until: until, Why: strings.TrimSpace("usage limit " + resetSuffix(out.Reset))}, time.Now()) {
				out.Wrote = append(out.Wrote, "paused every job until "+until.Format(time.RFC3339))
				planner(strings.Join(strings.Fields("usage_limit "+id+" "+resetSuffix(out.Reset)), " "))
			}
		}
		if r.Job != "" {
			// The work isn't lost: once the pause lifts, the run's job
			// starts again; job refuses it till then, and the bot leaves
			// the run unacked.
			out.Do = append(out.Do, "maestro-ticket job again "+r.ID)
		}
		// A limit is no death: free the claim the stopped session left, so
		// it counts no lease expiry and the rerun can take the ticket.
		if id != "" {
			if tid, err := ticket.ParseID(id); err == nil {
				if d, err := s.Get(ctx, tid); err == nil {
					if cl := d.LiveClaim(); cl != nil && ticket.HolderBot(cl.Holder) == ticket.HolderBot(r.Launcher) {
						if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: tid, Holder: cl.Holder, Token: cl.Token, Actor: c.actor, IdempotencyKey: r.ID + "-release"}); err == nil {
							out.Wrote = append(out.Wrote, "released "+id+" till the limit lifts")
						}
					}
				}
			}
		}
		return out, nil
	case outDiskFull:
		planner(strings.Join(strings.Fields("disk_full "+id), " ") + ": " + out.First)
		return out, nil
	case outDied:
		if r.AfterDeath || c.diedBefore(r) {
			planner(strings.TrimSpace("died twice " + id))
		} else {
			out.Do = append(out.Do, "maestro-ticket resume "+r.ID+" --died")
		}
		return out, nil
	}
	// A plan, rule or finish run that gave no result frees its ticket for
	// the next look to start again.
	if (r.Job == "plan" || r.Job == "rule" || r.Job == "finish") && out.Outcome != outDone && out.Outcome != outQuestion && r.Ticket != "" {
		if dir, err := c.besideStore("handouts"); err == nil {
			os.Remove(handoutPath(dir, r.Ticket, "planner-job"))
		}
	}
	if textJobs[r.Job] {
		switch r.Job {
		case "judge":
			out.Send = append(out.Send, judgeSends(r.Reply, "")...)
		case "plan", "rule", "finish":
			// The planner reads none of it: owner's calls go up as cards
			// that name the run, and a question gets the session to decide.
			sent := judgeSends(r.Reply, r.Job+" "+r.ID+": ")
			out.Send = append(out.Send, sent...)
			// A question beside cards waits on their answers; alone, the
			// session decides.
			if out.Outcome == outQuestion && len(sent) == 0 && !c.nudged(r, "decide") {
				out.Do = append(out.Do, "maestro-ticket resume "+r.ID+" --decide")
			}
		default: // cos, qa-ask: the asker reads the answer
			out.Reply = r.Reply
		}
		return out, nil
	}
	if id == "" {
		if out.Outcome != outDone {
			planner(out.Outcome + " " + r.ID)
		}
		return out, nil
	}
	tid, err := ticket.ParseID(id)
	if err != nil {
		return out, err
	}
	d, err := s.Get(ctx, tid)
	if err != nil {
		return out, err
	}
	t := d.Ticket
	out.Status = string(t.Status)
	last := latest(d)

	if r.Job == "review" || r.Job == "qa" {
		// Follow-ups are no part of the route: one that can't be filed
		// says so, and the run routes on.
		if err := c.fileFollowUps(ctx, s, r, d, &out); err != nil {
			out.Wrote = append(out.Wrote, "follow-ups not filed: "+err.Error())
		}
	}
	switch r.Job {
	case "design":
		switch {
		case t.Status == ticket.StatusBlocked && strings.HasPrefix(last.Body, "Sent up:"):
			// Up already. If this run's pickup sent it, show the card again
			// until the run is acked: a bot that stopped loses nothing.
			if err := c.designUp(ctx, s, d, "pickup "+r.ID, &out); err != nil {
				return out, err
			}
		case t.Status == ticket.StatusBlocked && designReady(d):
			if err := c.designUp(ctx, s, d, "pickup "+r.ID, &out); err != nil {
				return out, err
			}
		case t.Status == ticket.StatusBlocked:
			planner("block " + id + ": " + firstLine(last.Body))
		default:
			planner(out.Outcome + " " + id + ": " + out.First)
		}
	case "build":
		return c.routeBuild(ctx, s, r, d, out)
	case "review":
		switch {
		case out.Outcome == outTimedOut:
			if h, ok := latestWith(d, "Handoff:"); !ok || !c.timedOutBefore(r, h.CreatedAt) {
				// Once at a head: again, with twice the time.
				out.Do = append(out.Do, fmt.Sprintf("maestro-ticket job again %s --limit %s", r.ID, short(min(2*r.LimitDuration(), 4*time.Hour))))
				return out, nil
			}
			return c.reviewTimedOut(ctx, s, r, d, out)
		case fullModelText.MatchString(out.First):
			out.Do = append(out.Do, "maestro-ticket job again "+r.ID+" --full")
		case t.Status == ticket.StatusInReview && strings.HasPrefix(last.Body, "Review: GO"):
			out.Send = append(out.Send, send{"qa-lead", "qa " + id})
		case t.Status == ticket.StatusInProgress && strings.HasPrefix(last.Body, "Kickback:"):
			next, err := kickbackRoute(ctx, s, t)
			if err != nil {
				return out, err
			}
			out.Send = append(out.Send, next)
		case t.Status == ticket.StatusBlocked:
			planner("block " + id + ": " + firstLine(last.Body))
		case out.Outcome == outRefused:
			planner("refused " + id + ": " + out.First)
		default:
			out.Mismatch = "a Review: GO, a Kickback: or an Escalation:, found " + string(t.Status) + " " + quoteFirst(last.Body)
			planner("mismatch " + id)
		}
	case "qa", "live":
		switch {
		case out.Outcome == outTimedOut:
			gate, _ := latestWith(d, "Review: GO")
			if !c.timedOutBefore(r, gate.CreatedAt) {
				out.Do = append(out.Do, fmt.Sprintf("maestro-ticket job again %s --limit %s", r.ID, short(min(2*r.LimitDuration(), 4*time.Hour))))
				return out, nil
			}
			return c.qaTimedOut(ctx, s, r, d, out)
		case r.Job == "live" && t.Status == ticket.StatusBacklog && strings.HasPrefix(last.Body, "Hold: set by qa-lead: live run failed"):
			planner("live fail " + id)
		case t.Status == ticket.StatusDone:
			done, err := goalDone(ctx, s, t.Goal)
			if err != nil {
				return out, err
			}
			ready, err := readyWork(ctx, s)
			if err != nil {
				return out, err
			}
			switch {
			case done:
				planner("goal " + t.Goal + " done")
			case ready || t.Goal != "":
				// A merge frees what waited on it: the planner looks.
				planner("merged " + id)
			}
		case t.Status == ticket.StatusInProgress && strings.HasPrefix(last.Body, "Kickback: QA fail"):
			next, err := kickbackRoute(ctx, s, t)
			if err != nil {
				return out, err
			}
			out.Send = append(out.Send, next)
		case strings.HasPrefix(last.Body, "Rereview:"):
			out.Send = append(out.Send, send{"reviewer", "review " + id})
		case t.Status == ticket.StatusBlocked:
			planner("block " + id + ": " + firstLine(last.Body))
		case r.Job == "live":
			planner("live " + id + ": " + out.First)
		case out.Outcome == outRefused:
			planner("refused " + id + ": " + out.First)
		default:
			out.Mismatch = "done, a Kickback: QA fail, a Rereview: or an Escalation:, found " + string(t.Status) + " " + quoteFirst(last.Body)
			planner("mismatch " + id)
		}
	default:
		if out.Outcome != outDone {
			planner(out.Outcome + " " + id)
		}
	}
	return out, nil
}

// routeBuild sorts an ended build. In review: the reviewer takes it, and
// the planner hears of the free slot when a build waits for one. Stopped
// on a question: a junior's session moves to Opus; the rest go up.
func (c *call) routeBuild(ctx context.Context, s ticket.Store, r runs.Record, d ticket.Detail, out routed) (routed, error) {
	t, id, last := d.Ticket, d.ID.String(), latest(d)
	planner := func(line string) { out.Send = append(out.Send, send{"planner", line}) }
	// up hands a block to the planner. The session stopped and won't go
	// on, so its claim goes too: else the judge waits out the lease.
	up := func(line string) error {
		planner(line)
		cl := t.LiveClaim()
		if cl == nil || ticket.HolderBot(cl.Holder) != ticket.HolderBot(r.Launcher) {
			return nil
		}
		if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: t.ID, Holder: cl.Holder, Token: cl.Token, Actor: c.actor, IdempotencyKey: r.ID + "-release"}); err != nil {
			return err
		}
		out.Wrote = append(out.Wrote, "released "+id+" for the planner")
		return nil
	}
	escalated := t.Status == ticket.StatusBlocked && strings.HasPrefix(last.Body, "Escalation:")
	outside := strings.Contains(firstLine(last.Body), "outside step:")
	switch {
	case t.Status == ticket.StatusInReview:
		out.Send = append(out.Send, send{"reviewer", "review " + id})
		ready, err := readyWork(ctx, s)
		if err != nil {
			return out, err
		}
		if ready {
			planner("slot free " + id)
		}
	case out.Outcome == outTimedOut:
		planner("timed_out " + id)
	case (out.Outcome == outQuestion || escalated) && !outside && flagValue(r.Command, "--model") == modelSonnet && !c.nudged(r, "opus"):
		// The junior's session holds the ticket; on Opus it settles the
		// question itself or sends it up (owner's ruling, Nov 14 2026).
		out.Do = append(out.Do, "maestro-ticket resume "+r.ID+" --opus")
	case escalated:
		if err := up("block " + id + ": " + firstLine(last.Body)); err != nil {
			return out, err
		}
	case out.Outcome == outQuestion && !c.nudged(r, "escalate"):
		// Asked, but posted nothing: the session writes it on the ticket.
		out.Do = append(out.Do, "maestro-ticket resume "+r.ID+" --escalate")
	case out.Outcome == outQuestion:
		// Told once to post it, it asked again: pickup posts the question
		// itself and blocks the ticket, so no look builds it again before
		// the judge answers.
		q := headLine(lastLine(r.Reply))
		req := ticket.CommentRequest{ID: t.ID, Body: "Escalation: build: " + q + "\nReturn-role: engineer", Actor: c.actor, IdempotencyKey: r.ID + "-question"}
		move := ticket.TransitionRequest{ID: t.ID, To: ticket.StatusBlocked, Actor: c.actor, Note: "a question the session didn't post", IdempotencyKey: r.ID + "-question-move"}
		if cl := t.LiveClaim(); cl != nil {
			req.Holder, req.Token = cl.Holder, cl.Token
			move.Holder, move.Token, move.Release = cl.Holder, cl.Token, true
		}
		if t.Status != ticket.StatusBlocked {
			if _, err := s.Comment(ctx, req); err != nil {
				return out, err
			}
			if _, err := s.Transition(ctx, move); err != nil {
				return out, err
			}
			out.Status = string(ticket.StatusBlocked)
			out.Wrote = append(out.Wrote, id+" blocked: Escalation: build: "+trim(q, 80))
		}
		planner("question " + id + ": " + q)
	case out.Outcome == outRefused:
		planner("refused " + id + ": " + out.First)
	default:
		out.Mismatch = "in_review with a Handoff:, found " + string(t.Status) + " " + quoteFirst(last.Body)
		planner("build " + id + " stopped " + string(t.Status) + ": " + out.First)
	}
	// A question the bot doesn't answer itself it needn't read.
	if len(out.Do) > 0 {
		out.Reply = ""
	}
	return out, nil
}

// reviewTimedOut does what the reviewer did by hand when a review ran past
// its limit: unless a verdict landed since the run began, it blocks the
// ticket with an Escalation the planner answers. Idempotency keys make a
// second pickup of the same run a replay.
func (c *call) reviewTimedOut(ctx context.Context, s ticket.Store, r runs.Record, d ticket.Detail, out routed) (routed, error) {
	t, id := d.Ticket, d.ID.String()
	planner := func(line string) { out.Send = append(out.Send, send{"planner", line}) }
	for _, cm := range d.Comments {
		if cm.CreatedAt.Before(r.StartedAt) {
			continue
		}
		if strings.HasPrefix(cm.Body, "Review:") || (strings.HasPrefix(cm.Body, "Kickback:") && !strings.HasPrefix(cm.Body, "Kickback: QA fail")) {
			planner("timed_out " + id + " after its verdict")
			return out, nil
		}
	}
	if t.Status == ticket.StatusBlocked && strings.HasPrefix(latest(d).Body, "Escalation: review at ") && strings.Contains(firstLine(latest(d).Body), ": ran past "+r.Limit) {
		planner("block " + id + ": review ran past " + r.Limit) // as the first pickup of this run said
		return out, nil
	}
	if t.Status != ticket.StatusInReview || t.LiveClaim() != nil {
		planner("timed_out " + id)
		return out, nil
	}
	sha := ""
	if h, ok := latestWith(d, "Handoff:"); ok {
		sha = shaIn(h.Body)
	}
	if sha == "" {
		planner("timed_out " + id + ": no Handoff: sha to name in an Escalation")
		return out, nil
	}
	role := t.Role
	if role == "" {
		role = ticket.RoleEngineer
	}
	// The note first, then the move, so a stop between leaves the ticket
	// in review, where the next pickup finishes both.
	body := fmt.Sprintf("Escalation: review at %s: ran past %s\nReturn-role: %s", sha, r.Limit, role)
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: t.ID, Body: body, Actor: c.actor, IdempotencyKey: r.ID + "-timeout-note"}); err != nil {
		return out, err
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{
		ID: t.ID, From: ticket.StatusInReview, To: ticket.StatusBlocked, Actor: c.actor,
		Note: "review ran past its limit", IdempotencyKey: r.ID + "-timeout-move",
	}); err != nil {
		return out, err
	}
	out.Status = string(ticket.StatusBlocked)
	out.Wrote = append(out.Wrote, id+" blocked: Escalation: review at "+sha+": ran past "+r.Limit)
	planner("block " + id + ": review ran past " + r.Limit)
	return out, nil
}

// nudged reports whether r's session had the fixed resume nudge once.
func (c *call) nudged(r runs.Record, nudge string) bool {
	if r.Nudge == nudge {
		return true
	}
	session := cmp.Or(r.SessionID, flagValue(r.Command, "--session-id"), flagValue(r.Command, "--resume"))
	dir, err := c.besideStore("runs")
	if err != nil || session == "" {
		return false
	}
	list, _ := runs.List(dir, time.Now(), true)
	for _, o := range list {
		if o.Nudge == nudge && flagValue(o.Command, "--resume") == session {
			return true
		}
	}
	return false
}

// diedBefore reports whether r's session was resumed once after a death
// already, as resume counts it: a second death goes up, not to a resume
// resume refuses.
func (c *call) diedBefore(r runs.Record) bool {
	session := cmp.Or(r.SessionID, flagValue(r.Command, "--session-id"), flagValue(r.Command, "--resume"))
	dir, err := c.besideStore("runs")
	if err != nil || session == "" {
		return false
	}
	list, _ := runs.List(dir, time.Now(), true)
	for _, o := range list {
		if o.AfterDeath && flagValue(o.Command, "--resume") == session {
			return true
		}
	}
	return false
}

// timedOutBefore reports whether another run of r's job on its ticket ran
// out of time since since: a second timeout at one head goes up.
func (c *call) timedOutBefore(r runs.Record, since time.Time) bool {
	dir, err := c.besideStore("runs")
	if err != nil {
		return false
	}
	list, _ := runs.List(dir, time.Now(), true)
	for _, o := range list {
		if o.ID != r.ID && o.Ticket == r.Ticket && o.Job == r.Job && o.State == runs.TimedOut && o.StartedAt.After(since) {
			return true
		}
	}
	return false
}

// qaTimedOut blocks a ticket whose QA ran out of time twice at one GO,
// with an Escalation the planner answers, unless a verdict landed.
func (c *call) qaTimedOut(ctx context.Context, s ticket.Store, r runs.Record, d ticket.Detail, out routed) (routed, error) {
	t, id := d.Ticket, d.ID.String()
	planner := func(line string) { out.Send = append(out.Send, send{"planner", line}) }
	gate, ok := latestWith(d, "Review: GO")
	if t.Status != ticket.StatusInReview || t.LiveClaim() != nil || !ok {
		planner("timed_out " + id)
		return out, nil
	}
	sha := shaIn(gate.Body)
	body := fmt.Sprintf("Escalation: QA at %s: ran past %s twice: a hung app or a test too big for one run?\nReturn-role: qa-lead", sha, r.Limit)
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: t.ID, Body: body, Actor: c.actor, IdempotencyKey: r.ID + "-timeout-note"}); err != nil {
		return out, err
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{
		ID: t.ID, From: ticket.StatusInReview, To: ticket.StatusBlocked, Actor: c.actor,
		Note: "QA ran past its limit twice", IdempotencyKey: r.ID + "-timeout-move",
	}); err != nil {
		return out, err
	}
	out.Status = string(ticket.StatusBlocked)
	out.Wrote = append(out.Wrote, id+" blocked: QA ran past "+r.Limit+" twice")
	planner("block " + id + ": QA ran past " + r.Limit + " twice")
	return out, nil
}

// designReady: the designer's session handed a design up as the
// owner's card, and no answer or Sent up came after it.
func designReady(d ticket.Detail) bool {
	for i := len(d.Comments) - 1; i >= 0; i-- {
		b := d.Comments[i].Body
		switch {
		case strings.HasPrefix(b, "Escalation: design ready"):
			return true
		case strings.HasPrefix(b, "Answer:"), strings.HasPrefix(b, "Sent up:"):
			return false
		}
	}
	return false
}

// designUp sends a ready design's card to the chief of staff as the
// designer's session wrote it, one line, and marks the ticket Sent up so
// no look sends it again. The write is keyed by the escalation, so pickup
// and look never mark one twice.
func (c *call) designUp(ctx context.Context, s ticket.Store, d ticket.Detail, sender string, out *routed) error {
	e, ok := latestWith(d, "Escalation: design ready")
	if !ok {
		return nil
	}
	key := fmt.Sprintf("design-up-%s-%d", d.ID, e.ID)
	dir, err := c.besideStore("handouts")
	if err != nil {
		return err
	}
	mine, hid := key+" "+sender, fmt.Sprintf("%s-design%d", d.ID, e.ID)
	if !claimHandout(dir, hid, "chief-of-staff", mine, 24*time.Hour, time.Now()) {
		if b, _ := os.ReadFile(handoutPath(dir, hid, "chief-of-staff")); strings.TrimSpace(string(b)) != mine {
			return nil // another look or pickup sends this card
		}
	}
	var lines []string
	for _, l := range strings.Split(e.Body, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "Return-role:") {
			lines = append(lines, l)
		}
	}
	card := strings.Join(strings.Fields(strings.Join(lines, " | ")), " ")
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: d.ID, Body: "Sent up: chief of staff: approve the design on " + d.ID.String() + "?", Actor: c.actor, IdempotencyKey: key}); err != nil {
		os.Remove(handoutPath(dir, hid, "chief-of-staff")) // the next one tries again
		return err
	}
	out.Wrote = append(out.Wrote, d.ID.String()+" sent up: design ready")
	out.Send = append(out.Send, send{"chief-of-staff", d.ID.String() + " " + card})
	return nil
}

// judgeSends reads a reply's send and up lines; an up line goes to the
// chief of staff, after tag, which names the run a planning card came
// from so the answer can resume it.
func judgeSends(reply, tag string) []send {
	out := []send{}
	for _, l := range strings.Split(reply, "\n") {
		l = strings.TrimSpace(l)
		if rest, ok := strings.CutPrefix(l, "send "); ok {
			if bot, line, ok := strings.Cut(rest, ": "); ok && bot != "" && !strings.ContainsAny(bot, " \t") {
				out = append(out, send{bot, line})
			}
		}
		if rest, ok := strings.CutPrefix(l, "up: "); ok {
			out = append(out, send{"chief-of-staff", tag + rest})
		}
	}
	return out
}

func resetSuffix(reset string) string {
	if reset == "" {
		return ""
	}
	return "resets " + reset
}

func joinNL(s string) string {
	if s == "" {
		return ""
	}
	return "\n" + s
}

func quoteFirst(body string) string {
	if body == "" {
		return "with no note"
	}
	return "under " + strings.SplitN(firstLine(body), ":", 2)[0] + ":"
}

// followUpLines are the problems a review or QA run found outside its
// ticket: a review's "## Out of scope" section, and the "New tickets:"
// line of QA's verdict and the "Open:" line of its reply.
func followUpLines(r runs.Record, d ticket.Detail) []string {
	var out []string
	keep := func(l string) {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-*"))
		if l != "" && !strings.EqualFold(l, "none") && !strings.HasPrefix(l, "(") {
			out = append(out, l)
		}
	}
	if r.Job == "review" {
		in := false
		for _, l := range strings.Split(r.Reply, "\n") {
			switch {
			case strings.HasPrefix(l, "## Out of scope"):
				in = true
			case strings.HasPrefix(l, "## "):
				in = false
			case in:
				keep(l)
			}
		}
		return out
	}
	for i := len(d.Comments) - 1; i >= 0; i-- {
		b := d.Comments[i].Body
		if !strings.HasPrefix(b, "QA:") && !strings.HasPrefix(b, "Kickback: QA fail") && !strings.HasPrefix(b, "Live run:") {
			continue
		}
		if d.Comments[i].CreatedAt.Before(r.StartedAt) {
			break
		}
		for _, l := range strings.Split(b, "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "New tickets:"); ok {
				keep(v)
			}
		}
		break
	}
	for _, l := range strings.Split(r.Reply, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "Open:"); ok && strings.TrimSpace(v) != "" && !strings.EqualFold(strings.TrimSpace(v), "none") {
			keep("Open: " + strings.TrimSpace(v))
		}
	}
	return out
}

// fileFollowUps puts what a run found outside its ticket on the
// "Follow-ups: <area>" ticket of its first area, making that ticket, parked
// deferred, when there is none: the planner's rule, done by code, so no bot
// reads the lines. A follow-up marked urgent shows in the planner's look.
// Idempotency keys make a second pickup of the run a replay.
func (c *call) fileFollowUps(ctx context.Context, s ticket.Store, r runs.Record, d ticket.Detail, out *routed) error {
	lines := followUpLines(r, d)
	if len(lines) == 0 {
		return nil
	}
	area := "general"
	if len(d.Areas) > 0 {
		area = d.Areas[0]
	}
	title := "Follow-ups: " + area
	list, err := s.List(ctx, ticket.ListFilter{Statuses: openStatuses})
	if err != nil {
		return err
	}
	var fu ticket.ID
	for _, t := range list {
		if t.Title == title {
			fu = t.ID
			break
		}
	}
	if fu == 0 {
		var areas []string
		if area != "general" {
			areas = []string{area}
		}
		// No goal: it is no part of one, and would keep its goal from
		// ever reading done. Keyed by area and day, and made by the store's
		// own actor so every bot's request is the same: pickups at once
		// make one. A key whose ticket closed since makes a new one.
		req := ticket.CreateRequest{
			Title: title, Status: ticket.StatusBacklog, Parked: ticket.ParkedDeferred, Role: ticket.RoleEngineer,
			Areas: areas, Actor: ticket.SystemActor, IdempotencyKey: "followups:" + area + ":" + time.Now().UTC().Format("2006-01-02"),
			Body: "Collects problems review and QA found in " + area + " outside their tickets. Plan it before it is built: it is never moved to todo as it is.",
		}
		t, err := s.Create(ctx, req)
		if err == nil && t.Status.Terminal() || errors.Is(err, ticket.ErrIdempotencyConflict) {
			req.IdempotencyKey = r.ID + "-followups-ticket"
			t, err = s.Create(ctx, req)
		}
		if err != nil {
			return err
		}
		fu = t.ID
	}
	body := fmt.Sprintf("From %s %s (%s):\n- %s", d.ID, r.Job, r.ID, strings.Join(lines, "\n- "))
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: fu, Body: body, Actor: c.actor, IdempotencyKey: r.ID + "-followups"}); err != nil {
		return err
	}
	out.Wrote = append(out.Wrote, fmt.Sprintf("%d follow-ups from %s on %s", len(lines), d.ID, fu))
	return nil
}
