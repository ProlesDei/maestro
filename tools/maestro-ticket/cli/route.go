package cli

import (
	"context"
	"encoding/json"
	"fmt"
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
	// A failed run's usage or rate limit, or Claude Code's own words for
	// one at the start or end of any reply. A done reply that only talks
	// about rate limits, as a build that adds one does, is no limit.
	usageLimitText  = regexp.MustCompile(`(?i)(usage limit|rate limit|hit your limit|limit reached|out of (extra )?usage)`)
	usageLimitExact = regexp.MustCompile(`(?i)(claude ai usage limit reached|you've hit your (session |weekly |usage )?limit|usage limit reached)`)
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
	ends := firstLine(r.Reply) + "\n" + lastLine(r.Reply)
	switch {
	case r.State == runs.Died:
		return outDied, ""
	case r.State == runs.TimedOut:
		return outTimedOut, ""
	case failed && usageLimitText.MatchString(text) || usageLimitExact.MatchString(ends):
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
	case strings.HasSuffix(lastLine(r.Reply), "?"):
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
	l, err := s.List(ctx, ticket.ListFilter{Goal: goal, Limit: 1, Statuses: []ticket.Status{
		ticket.StatusBacklog, ticket.StatusTodo, ticket.StatusInProgress, ticket.StatusInReview, ticket.StatusBlocked}})
	return len(l) == 0, err
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
	planner := func(line string) { out.Send = append(out.Send, send{"planner", line}) }
	id := r.Ticket

	switch out.Outcome {
	case outUsageLimit:
		planner(strings.TrimSpace("usage_limit " + id + " " + resetSuffix(out.Reset)))
		return out, nil
	case outDiskFull:
		planner(strings.TrimSpace("disk_full " + id))
		return out, nil
	case outDied:
		if r.AfterDeath {
			planner(strings.TrimSpace("died twice " + id))
		} else {
			out.Do = append(out.Do, "maestro-ticket resume "+r.ID+" --died")
		}
		return out, nil
	case outFailed:
		out.Reply = r.Reply + joinNL(r.Error)
	}
	if textJobs[r.Job] {
		out.Reply = r.Reply
		if r.Job == "judge" {
			out.Send = append(out.Send, judgeSends(r.Reply)...)
			out.Reply = ""
		}
		return out, nil
	}
	if out.Outcome == outQuestion {
		out.Reply = r.Reply
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

	switch r.Job {
	case "build":
		return c.routeBuild(ctx, s, r, d, out)
	case "review":
		switch {
		case out.Outcome == outTimedOut:
			return c.reviewTimedOut(ctx, s, r, d, out)
		case fullModelText.MatchString(out.First):
			out.Do = append(out.Do, "maestro-ticket job review "+id+" --full")
		case t.Status == ticket.StatusInReview && strings.HasPrefix(last.Body, "Review: GO"):
			out.Send = append(out.Send, send{"qa-lead", "qa " + id + " at " + shaIn(last.Body)})
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
			planner("timed_out " + id)
		case t.Status == ticket.StatusDone:
			done, err := goalDone(ctx, s, t.Goal)
			if err != nil {
				return out, err
			}
			if done {
				planner("goal " + t.Goal + " done")
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
	case (out.Outcome == outQuestion || escalated) && !outside && flagValue(r.Command, "--model") == modelSonnet:
		// The junior's session holds the ticket; on Opus it settles the
		// question itself or sends it up (owner's ruling, Nov 14 2026).
		out.Do = append(out.Do, "maestro-ticket resume "+r.ID+" --opus")
	case escalated || out.Outcome == outQuestion:
		planner("block " + id + ": " + firstLine(last.Body))
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
	body := fmt.Sprintf("Escalation: review at %s: ran past %s\nReturn-role: %s", sha, r.Limit, role)
	if _, err := s.Transition(ctx, ticket.TransitionRequest{
		ID: t.ID, From: ticket.StatusInReview, To: ticket.StatusBlocked, Actor: c.actor,
		Note: "review ran past its limit", IdempotencyKey: r.ID + "-timeout-move",
	}); err != nil {
		return out, err
	}
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: t.ID, Body: body, Actor: c.actor, IdempotencyKey: r.ID + "-timeout-note"}); err != nil {
		return out, err
	}
	out.Status = string(ticket.StatusBlocked)
	out.Wrote = append(out.Wrote, id+" blocked: Escalation: review at "+sha+": ran past "+r.Limit)
	planner("block " + id + ": review ran past " + r.Limit)
	return out, nil
}

// judgeSends reads a judge reply's send and up lines; an up line goes to
// the chief of staff.
func judgeSends(reply string) []send {
	out := []send{}
	for _, l := range strings.Split(reply, "\n") {
		l = strings.TrimSpace(l)
		if rest, ok := strings.CutPrefix(l, "send "); ok {
			if bot, line, ok := strings.Cut(rest, ": "); ok && bot != "" && !strings.ContainsAny(bot, " \t") {
				out = append(out, send{bot, line})
			}
		}
		if rest, ok := strings.CutPrefix(l, "up: "); ok {
			out = append(out, send{"chief-of-staff", rest})
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
