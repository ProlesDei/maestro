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
	"slices"
	"strings"
	"sync"
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
	// Landed: the run ended short after its work landed, so it routes as
	// one that ended done, and its end starts its next step the same way.
	Landed bool `json:"landed,omitempty"`
	// Reply is the whole reply, shown only when the bot must read it: a
	// question, a failure, or a text job whose reply is the answer.
	Reply string `json:"reply,omitempty"`
	// quiet: the run ended short, but its ticket moved on without it, so
	// nothing is left to route. Never stored: only its own end reads it.
	quiet bool
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
	// Claude Code's error reply once the API stayed down or overloaded
	// past its own retries: the whole team's outage, not the run's.
	apiDownText   = regexp.MustCompile(`(?i)^API Error: (?:5[0-9]{2}\b|.*\boverloaded)`)
	fullModelText = regexp.MustCompile(`^review: stopped: tier \d+ needs the full model`)
	hexSHA        = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	shortSHA      = regexp.MustCompile(`\bat ([0-9a-f]{7,40})\b`)
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

// apiPause is how long every job waits once an API outage ended a run.
const apiPause = 10 * time.Minute

// apiDown reports whether an API outage failed r.
func apiDown(r runs.Record) bool {
	out, _ := outcomeOf(r)
	return out == outFailed && apiDownText.MatchString(firstLine(r.Reply))
}

// routeOutcome sorts an ended run for routing: as outcomeOf, but a run an
// API outage failed waits as one a usage limit stopped does, on a short
// pause for the whole team, and its session goes on as it lifts. A retry
// spent inside the outage would only fail again and block its ticket.
// Stats keep outcomeOf's failed.
func admissionWait(r runs.Record) bool {
	if r.Profile == "" || r.State != runs.Failed {
		return false
	}
	msg := strings.TrimSpace(r.Error)
	return msg == "maestro-ticket: worker: reset passed; refresh usage" || msg == "maestro-ticket: worker: missing usage reading or job estimate" || strings.HasPrefix(msg, "maestro-ticket: worker admission:") || strings.HasPrefix(msg, "maestro-ticket: worker: projected usage exceeds allowance until ") || msg == "maestro-ticket: worker: subscription login could not be verified; refresh the bound account"
}

func routeOutcome(r runs.Record) (outcome, reset string) {
	if admissionWait(r) {
		_, until, _ := strings.Cut(r.Error, " until ")
		return outUsageLimit, strings.TrimSpace(until)
	}
	if !apiDown(r) {
		return outcomeOf(r)
	}
	ended := time.Now()
	if r.EndedAt != nil {
		ended = *r.EndedAt
	}
	return outUsageLimit, ended.Add(apiPause).UTC().Format(time.RFC3339)
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

// readyWork reports whether a build waits for a free slot: new work, or
// work sent back or stopped with no claim on it and no run that still
// holds it, which look hands a slot first.
func (c *call) readyWork(ctx context.Context, s ticket.Store) (bool, error) {
	l, err := s.List(ctx, ticket.ListFilter{Statuses: []ticket.Status{ticket.StatusTodo}, Ready: true, Limit: 1})
	if err != nil || len(l) > 0 {
		return len(l) > 0, err
	}
	if l, err = s.List(ctx, ticket.ListFilter{Statuses: []ticket.Status{ticket.StatusInProgress}, Ready: true}); err != nil || len(l) == 0 {
		return false, err
	}
	held := map[string]bool{}
	now := time.Now()
	for _, r := range c.allRuns() { // none read: each counts as waiting
		if holds, _ := runHolds(r, now, ticket.DefaultStall); holds && r.Ticket != "" {
			held[r.Ticket] = true
		}
	}
	return slices.ContainsFunc(l, func(t ticket.Ticket) bool { return !held[t.ID.String()] }), nil
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

// route sorts one ended job and says what comes next. Its writes to the
// ticket are keyed, so a second pickup of the same run writes nothing new.
// A run that ended short always gets a line, a command or a reply.
func (c *call) route(ctx context.Context, s ticket.Store, r runs.Record) (routed, error) {
	out, err := c.routeRun(ctx, s, r)
	if err == nil {
		ensureRouted(r, &out)
	}
	return out, err
}

// ensureRouted gives a run that ended short, and that its route gave no
// line, command or reply, one fault line to the chief of staff, so no such
// end is acked unseen. A ticket closed or moved on has nothing to route.
func ensureRouted(r runs.Record, out *routed) {
	if out.Outcome == outDone || len(out.Send)+len(out.Do) > 0 || out.Reply != "" || out.quiet || ticket.Status(out.Status).Terminal() {
		return
	}
	line := strings.TrimSpace(r.Job + " " + out.Outcome + " " + r.ID + " " + r.Ticket)
	out.Send = append(out.Send, send{"chief-of-staff", line + ": nothing routed"})
}

func (c *call) routeRun(ctx context.Context, s ticket.Store, r runs.Record) (routed, error) {
	out := routed{Run: r.ID, Job: r.Job, Ticket: r.Ticket, First: headLine(r.Reply), Send: []send{}, Do: []string{}, Wrote: []string{}}
	out.Outcome, out.Reset = routeOutcome(r)
	if out.First == "" {
		out.First = headLine(r.Error)
	}
	if out.First == "" && r.Exit != nil {
		out.First = fmt.Sprintf("exit %d, no reply", *r.Exit)
	}
	planner := func(line string) { out.Send = append(out.Send, send{"planner", line}) }
	id := r.Ticket
	if admissionWait(r) {
		// Eligibility and the meter already hold the affected account or role. The
		// refused wrapper never touched the ticket or acquired an engineer claim.
		out.Do = []string{"maestro-ticket job again " + r.ID}

		return out, nil
	}
	// A judge run that gave no answer frees its rows for the next look,
	// unless it died: then its resume works them.
	// A usage limit doesn't free them either: the run goes on once it lifts.
	if r.Job == "judge" && out.Outcome != outDone && out.Outcome != outUsageLimit && !(out.Outcome == outDied && !r.AfterDeath) {
		if co, err := c.coord(); err == nil {
			for _, tk := range append(slices.Clone(r.Tickets), "cause") {
				co.dropHandout(tk, "judge")
			}
		}
	}

	// A limit or a death after the work landed loses none of it: the run
	// routes on its ticket, as one that ended done would.
	var d ticket.Detail
	loaded := false
	if (out.Outcome == outUsageLimit || out.Outcome == outDied) && chainJobs[r.Job] && id != "" {
		tid, err := ticket.ParseID(id)
		if err != nil {
			return out, err
		}
		if d, err = s.Get(ctx, tid); err != nil {
			return out, err
		}
		loaded = true
	}
	moved := loaded && landed(r, d)

	switch out.Outcome {
	case outUsageLimit:
		// The limit is the team's plan's: no bot starts a run until it
		// lifts. A reset pickup can't read waits an hour.
		until := limitLifts(r, out.Reset)
		// One whose work landed sets no pause: its next step, refused
		// under its own pause, would be lost. Under another's pause it
		// waits and rings as it lifts; else that step runs, or meets the
		// limit and pauses the team itself.
		if co, err := c.coord(); err == nil && until.After(time.Now()) && !moved {
			// One line to the planner per pause, not one per stopped run.
			why := "usage limit"
			if apiDown(r) {
				why = "API outage"
			}
			if r.Profile != "" {
				if err := c.holdSubscription(r, until, apiDown(r)); err != nil {
					return out, err
				}
				scope := r.Profile
				if apiDown(r) {
					scope = r.Harness
				}
				out.Wrote = append(out.Wrote, "paused "+scope+" until "+until.Format(time.RFC3339))
			} else if co.setPause(pause{Until: until, Why: strings.TrimSpace(why + " " + resetSuffix(out.Reset))}, time.Now()) {
				out.Wrote = append(out.Wrote, "paused every job until "+until.Format(time.RFC3339))
				planner(strings.Join(strings.Fields("usage_limit "+id+" "+resetSuffix(out.Reset)), " "))
			}
		}
		if r.Job != "" && !moved {
			// The work isn't lost: once the pause lifts, the session goes
			// on where it stopped, answers it was given and all; one with
			// no session starts afresh. Both refuse till then, and the bot
			// leaves the run unacked.
			out.Do = append(out.Do, "maestro-ticket resume "+r.ID+" --go-on")
		}
		// A limit is no death: free the claim the stopped session left, so
		// it counts no lease expiry and the rerun, or the next step of
		// work that landed, can take the ticket.
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
		if !moved {
			return out, nil
		}
	case outDiskFull:
		planner(strings.Join(strings.Fields("disk_full "+id), " ") + ": " + out.First)
		return out, nil
	case outDied:
		if moved {
			break
		}
		if r.AfterDeath || c.diedBefore(r) {
			planner(strings.TrimSpace("died twice " + id))
		} else {
			out.Do = append(out.Do, "maestro-ticket resume "+r.ID+" --died")
		}
		return out, nil
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
			if len(sent) > 0 && r.Ticket != "" {
				// Its ticket waits for the answer, sent up, so no look
				// plans it again however long the owner takes.
				if err := c.parkForAnswer(ctx, s, r, cardWait, &out); err != nil {
					out.Wrote = append(out.Wrote, "not parked for the answer: "+err.Error())
				}
			}
			// A question beside cards waits on their answers; alone, the
			// session decides. One that gave no result runs again once.
			retry := false
			switch {
			case len(sent) > 0:
			case out.Outcome == outQuestion && !c.nudged(r, "decide"):
				out.Do = append(out.Do, "maestro-ticket resume "+r.ID+" --decide")
			case out.Outcome == outQuestion: // it asked again: the question goes up as a card
				out.Send = append(out.Send, send{"chief-of-staff", r.Job + " " + r.ID + ": What: a planning session asked twice | Question: " + headLine(lastLine(r.Reply))})
				if r.Ticket != "" {
					if err := c.parkForAnswer(ctx, s, r, cardWait, &out); err != nil {
						out.Wrote = append(out.Wrote, "not parked for the answer: "+err.Error())
					}
				}
			case out.Outcome != outDone:
				var err error
				if retry, err = c.planFailed(ctx, s, r, &out); err != nil {
					return out, err
				}
			}
			// One that gave no result and won't run again frees its ticket
			// for the next look to start again.
			if out.Outcome != outDone && out.Outcome != outQuestion && r.Ticket != "" && !retry {
				if co, err := c.coord(); err == nil {
					co.dropHandout(r.Ticket, "planner-job")
				}
			}
		default: // cos, qa-ask: the asker reads the answer
			out.Reply = cmp.Or(r.Reply, out.First)
		}
		return out, nil
	}
	if id == "" {
		if out.Outcome != outDone {
			planner(out.Outcome + " " + r.ID)
		}
		return out, nil
	}
	if !loaded {
		tid, err := ticket.ParseID(id)
		if err != nil {
			return out, err
		}
		if d, err = s.Get(ctx, tid); err != nil {
			return out, err
		}
		moved = landed(r, d)
	}
	t := d.Ticket
	// cut: the run ended short after its work landed.
	cut := moved && (out.Outcome == outTimedOut || out.Outcome == outDied || out.Outcome == outUsageLimit)
	out.Landed = cut
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
		case out.Outcome == outTimedOut && !moved:
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
		case cut:
			planner(out.Outcome + " " + id + " after its verdict")
		case out.Outcome == outRefused:
			planner("refused " + id + ": " + out.First)
		case t.Status == ticket.StatusInReview && c.noVerdictRuns(r, d, verdictMark(r.Job)) >= 2:
			if err := c.blockNoVerdict(ctx, s, r, d, &out); err != nil {
				return out, err
			}
		case c.stoppedMidWork(r, id, &out):
		case t.Status == ticket.StatusInReview && c.noVerdictRuns(r, d, verdictMark(r.Job)) == 0 && !neverRan(r) && !c.stoppedOnPurpose(r):
			// The first end with no verdict at a head runs again, in code.
			out.Do = append(out.Do, "maestro-ticket job again "+r.ID)
		default:
			out.Mismatch = "a Review: GO, a Kickback: or an Escalation:, found " + string(t.Status) + " " + quoteFirst(last.Body)
			planner("mismatch " + id)
		}
	case "qa", "live":
		switch {
		case out.Outcome == outTimedOut && !moved:
			return c.qaTimedOut(ctx, s, r, d, out)
		case r.Job == "live" && t.Status == ticket.StatusBacklog && strings.HasPrefix(last.Body, "Hold: set by qa-lead: live run failed"):
			planner("live fail " + id)
		case t.Status == ticket.StatusDone:
			done, err := goalDone(ctx, s, t.Goal)
			if err != nil {
				return out, err
			}
			ready, err := c.readyWork(ctx, s)
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
		case cut:
			planner(out.Outcome + " " + id + " after its verdict")
		case r.Job == "live":
			planner("live " + id + ": " + out.First)
		case out.Outcome == outRefused:
			planner("refused " + id + ": " + out.First)
		case t.Status == ticket.StatusInReview && c.noVerdictRuns(r, d, verdictMark(r.Job)) >= 2:
			if err := c.blockNoVerdict(ctx, s, r, d, &out); err != nil {
				return out, err
			}
		case c.stoppedMidWork(r, id, &out):
		case t.Status == ticket.StatusInReview && c.noVerdictRuns(r, d, verdictMark(r.Job)) == 0 && !neverRan(r) && !c.stoppedOnPurpose(r):
			out.Do = append(out.Do, "maestro-ticket job again "+r.ID) // the first with no verdict, as for review
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
	// ownClaim is the live claim if this run's bot holds it: pickup writes
	// under no other bot's lease.
	ownClaim := func() *ticket.Claim {
		if cl := t.LiveClaim(); cl != nil && ticket.HolderBot(cl.Holder) == ticket.HolderBot(r.Launcher) {
			return cl
		}
		return nil
	}
	switch {
	case escalated && strings.Contains(last.Body, postedBy(r)):
		// pickup blocked it itself, on an earlier pickup of this run: the
		// same route again, never a resume on a ticket the judge holds.
		planner("block " + id + ": " + headLine(last.Body))
	case t.Status == ticket.StatusInReview:
		out.Send = append(out.Send, send{"reviewer", "review " + id})
		ready, err := c.readyWork(ctx, s)
		if err != nil {
			return out, err
		}
		if ready {
			planner("slot free " + id)
		}
	case (out.Outcome == outTimedOut || c.retryableFail(r, out.Outcome)) && (t.Status == ticket.StatusInProgress || t.Status == ticket.StatusTodo) && (t.LiveClaim() == nil || ownClaim() != nil):
		// A build that ran out of time or failed runs again once, in
		// code; one a freeze ended, twice at its limit.
		if err := c.buildStopped(ctx, s, r, d, &out); err != nil {
			return out, err
		}
	case out.Outcome == outTimedOut:
		planner("timed_out " + id) // the ticket moved on, or another bot holds it
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
		body := "Escalation: build: " + q
		if t.LiveClaim() != nil && ownClaim() == nil {
			planner("block " + id + ": " + body) // another holds it: no write under its lease
			break
		}
		req := ticket.CommentRequest{ID: t.ID, Body: body + "\n" + postedBy(r) + "\nReturn-role: engineer", Actor: c.actor, IdempotencyKey: r.ID + "-question"}
		move := ticket.TransitionRequest{ID: t.ID, To: ticket.StatusBlocked, Actor: c.actor, Note: "a question the session didn't post", IdempotencyKey: r.ID + "-question-move"}
		if cl := ownClaim(); cl != nil {
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
		planner("block " + id + ": " + headLine(body))
	case out.Outcome == outRefused || refusedText.MatchString(firstLine(r.Reply)):
		planner("refused " + id + ": " + out.First) // a refusal that exited non-zero too
	// A build that crashed before its session claimed the ticket leaves it
	// in todo: those count too.
	case (t.Status == ticket.StatusInProgress || t.Status == ticket.StatusTodo) && c.stoppedBuilds(r, d) >= 2 && (t.LiveClaim() == nil || ownClaim() != nil):
		// The third build since its last handoff or send-back that stopped
		// with nothing to show: no fourth by rote. The judge looks.
		if err := c.blockStopped(ctx, s, r, d, stoppedThrice, &out); err != nil {
			return out, err
		}
	case c.stoppedMidWork(r, id, &out):
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
// its limit with no verdict since it began: it blocks the ticket with an
// Escalation the planner answers. Idempotency keys make a
// second pickup of the same run a replay.
func (c *call) reviewTimedOut(ctx context.Context, s ticket.Store, r runs.Record, d ticket.Detail, out routed) (routed, error) {
	t, id := d.Ticket, d.ID.String()
	planner := func(line string) { out.Send = append(out.Send, send{"planner", line}) }
	if t.Status == ticket.StatusBlocked && strings.HasPrefix(latest(d).Body, "Escalation: review at ") && strings.Contains(firstLine(latest(d).Body), ": ran past "+r.Limit) {
		planner("block " + id + ": review ran past " + r.Limit) // as the first pickup of this run said
		return out, nil
	}
	if t.Status != ticket.StatusInReview || t.LiveClaim() != nil {
		planner("timed_out " + id)
		return out, nil
	}
	// Once at a head: again, with twice the time; after a freeze, at the
	// same limit, twice. The head's start is its Handoff:, or with none,
	// the ticket's latest move into review: never a time a write moves.
	h, ok := latestWith(d, "Handoff:")
	since := h.CreatedAt
	if !ok {
		var err error
		if since, err = movedIntoReview(ctx, s, d); err != nil {
			return out, err
		}
	}
	if r.Frozen && c.frozenRuns(r, since) < maxFrozenReruns {
		out.Do = append(out.Do, "maestro-ticket job again "+r.ID) // a freeze ended it: the same limit again
		return out, nil
	}
	if !c.timedOutBefore(r, since) {
		out.Do = append(out.Do, fmt.Sprintf("maestro-ticket job again %s --limit %s", r.ID, short(min(2*r.LimitDuration(), 4*time.Hour))))
		return out, nil
	}
	sha := ""
	if ok {
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

// landed reports whether r's work reached its ticket before r ended, its
// note posted and its move made, as a route for a run that ended done
// reads them: a build's Handoff: in review or Escalation: blocked, a
// review's GO in review or send-back in progress, QA's merge, send-back
// in progress or Rereview: in review. Such a run routes on its ticket
// however it ended, so a timeout, a limit or a death after the work never
// drops its next step. A note whose move never came is half the work: the
// session goes on and makes the move. What a pickup wrote itself is no
// work of the run's.
func landed(r runs.Record, d ticket.Detail) bool {
	t := d.Ticket
	if (r.Job == "qa" || r.Job == "live") && t.Status == ticket.StatusDone {
		return true
	}
	if r.Job == "build" {
		for _, cm := range d.Comments {
			b := cm.Body
			if cm.CreatedAt.Before(r.StartedAt) || strings.Contains(b, postedBy(r)) {
				continue
			}
			if t.Status == ticket.StatusInReview && strings.HasPrefix(b, "Handoff:") || t.Status == ticket.StatusBlocked && strings.HasPrefix(b, "Escalation:") {
				return true
			}
		}
		return false
	}
	last := latest(d)
	b := last.Body
	if last.CreatedAt.Before(r.StartedAt) || strings.Contains(b, postedBy(r)) {
		return false
	}
	switch r.Job {
	case "review":
		return t.Status == ticket.StatusInReview && strings.HasPrefix(b, "Review: GO") ||
			t.Status == ticket.StatusInProgress && strings.HasPrefix(b, "Kickback:") && !strings.HasPrefix(b, "Kickback: QA fail")
	case "qa", "live":
		return t.Status == ticket.StatusInProgress && strings.HasPrefix(b, "Kickback: QA fail") ||
			t.Status == ticket.StatusInReview && strings.HasPrefix(b, "Rereview:")
	}
	return false
}

// limitLifts is when the usage limit that stopped r lifts: the reset its
// reply named, or an hour after it ended, so every pickup of the run
// reads the same time.
func limitLifts(r runs.Record, reset string) time.Time {
	if t, err := time.Parse(time.RFC3339, reset); err == nil {
		return t
	}
	ended := time.Now()
	if r.EndedAt != nil {
		ended = *r.EndedAt
	}
	return ended.Add(time.Hour)
}

// noVerdictRuns counts the other runs of r's job on its ticket since its
// latest mark (a Handoff: for review, a Review: GO for QA) that ended
// with no verdict, deaths and limits aside. A run that ended past the Stop
// hook's blocks counts even if it then timed out: the hook kept it going.
func (c *call) noVerdictRuns(r runs.Record, d ticket.Detail, mark string) int {
	return len(c.noVerdicts(r, d, mark))
}

func (c *call) noVerdicts(r runs.Record, d ticket.Detail, mark string) []runs.Record {
	since := d.CreatedAt
	if cm, ok := latestWith(d, mark); ok {
		since = cm.CreatedAt
	}
	var list []runs.Record
	for _, o := range c.allRuns() {
		if o.ID != r.ID && o.Job == r.Job && o.Ticket == r.Ticket && o.Ended() && !neverRan(o) && o.StartedAt.After(since) {
			if out, _ := routeOutcome(o); out != outUsageLimit && out != outDied && (out != outTimedOut || c.stopCapped(o.ID)) && !o.LauncherStopped() {
				list = append(list, o)
			}
		}
	}
	return list
}

// verdictMark is the note a review or QA run's count of runs with no
// verdict starts from.
func verdictMark(job string) string {
	if job == "qa" || job == "live" {
		return "Review: GO"
	}
	return "Handoff:"
}

// blockNoVerdict blocks a ticket whose review or QA keeps ending with no
// verdict, for the judge, as blockStopped does for builds.
func (c *call) blockNoVerdict(ctx context.Context, s ticket.Store, r runs.Record, d ticket.Detail, out *routed) error {
	t, id := d.Ticket, d.ID.String()
	role := ticket.RoleReviewer
	if r.Job == "qa" {
		role = ticket.RoleQALead
	}
	// Say how many ended past the Stop hook: those sessions were told to
	// post a verdict and still didn't.
	capped := 0
	for _, o := range append(c.noVerdicts(r, d, verdictMark(r.Job)), r) {
		if c.stopCapped(o.ID) {
			capped++
		}
	}
	hook := ""
	if capped > 0 {
		hook = fmt.Sprintf(", %d of them past the Stop hook's blocks", capped)
	}
	body := fmt.Sprintf("Escalation: %s ended 3 times with no verdict%s; the last said: %s\n%s\nReturn-role: %s", r.Job, hook, trim(out.First, 200), postedBy(r), role)
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: t.ID, Body: body, Actor: c.actor, IdempotencyKey: r.ID + "-noverdict"}); err != nil {
		return err
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: t.ID, From: ticket.StatusInReview, To: ticket.StatusBlocked, Actor: c.actor,
		Note: r.Job + " keeps ending with no verdict", IdempotencyKey: r.ID + "-noverdict-move"}); err != nil {
		return err
	}
	out.Status = string(ticket.StatusBlocked)
	out.Wrote = append(out.Wrote, id+" blocked: its "+r.Job+" ended 3 times with no verdict")
	out.Send = append(out.Send, send{"planner", "block " + id + ": " + headLine(body)})
	return nil
}

// cardWait ends the note that parks a ticket whose planning run sent a
// card up.
const cardWait = "waits on its card"

// parkForAnswer marks a ticket a planner session sent a card up for, or
// whose planning runs keep failing, Sent up, so it waits for the answer;
// keyed by the run. why ends the note.
func (c *call) parkForAnswer(ctx context.Context, s ticket.Store, r runs.Record, why string, out *routed) error {
	tid, err := ticket.ParseID(r.Ticket)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, tid)
	if err != nil {
		return err
	}
	t := d.Ticket
	if t.Status.Terminal() {
		return nil
	}
	if cl := t.LiveClaim(); cl != nil && ticket.HolderBot(cl.Holder) != ticket.HolderBot(r.Launcher) {
		return fmt.Errorf("%s is held by %s", r.Ticket, cl.Holder)
	}
	// The note alone parks it: look leaves a ticket under Sent up: be,
	// and the session the answer resumes finds it as it left it.
	req := ticket.CommentRequest{ID: tid, Body: "Sent up: chief of staff: the " + r.Job + " run " + r.ID + " " + why + "\n" + postedBy(r), Actor: c.actor, IdempotencyKey: r.ID + "-park"}
	if cl := t.LiveClaim(); cl != nil {
		req.Holder, req.Token = cl.Holder, cl.Token
	}
	if _, err := s.Comment(ctx, req); err != nil {
		return err
	}
	if why == cardWait {
		out.Wrote = append(out.Wrote, r.Ticket+" waits on the card, sent up")
	} else {
		out.Wrote = append(out.Wrote, r.Ticket+" sent up: "+why)
	}
	return nil
}

// buildHead is when a ticket's build last began afresh: its latest
// Handoff:, Kickback: or Answer:, else its making.
func buildHead(d ticket.Detail) time.Time {
	since := d.CreatedAt
	for _, p := range []string{"Handoff:", "Kickback:", "Answer:"} {
		if cm, ok := latestWith(d, p); ok && cm.CreatedAt.After(since) {
			since = cm.CreatedAt
		}
	}
	return since
}

// stoppedBuilds counts the other build runs on r's ticket that ended
// with no handoff since its build head.
func (c *call) stoppedBuilds(r runs.Record, d ticket.Detail) int {
	since := buildHead(d)
	n := 0
	for _, o := range c.allRuns() {
		if o.ID != r.ID && o.Job == "build" && o.Ticket == r.Ticket && o.Ended() && !neverRan(o) && o.StartedAt.After(since) {
			if out, _ := routeOutcome(o); out != outUsageLimit && out != outDied && !o.Frozen && !o.LauncherStopped() {
				n++
			}
		}
	}
	return n
}

// Why blockStopped blocks: the builds' stops, as its note and write say.
const (
	stoppedThrice = "stopped 3 times"
	stoppedTwice  = "timed out or failed twice"
)

// blockStopped blocks a ticket whose builds keep stopping short, with an
// Escalation the judge reads; why says how they stopped.
func (c *call) blockStopped(ctx context.Context, s ticket.Store, r runs.Record, d ticket.Detail, why string, out *routed) error {
	t, id := d.Ticket, d.ID.String()
	body := fmt.Sprintf("Escalation: build %s with no handoff; the last said: %s\n%s\nReturn-role: engineer", why, trim(out.First, 200), postedBy(r))
	req := ticket.CommentRequest{ID: t.ID, Body: body, Actor: c.actor, IdempotencyKey: r.ID + "-stopped"}
	move := ticket.TransitionRequest{ID: t.ID, From: t.Status, To: ticket.StatusBlocked, Actor: c.actor, Note: "its builds keep stopping", IdempotencyKey: r.ID + "-stopped-move"}
	if cl := t.LiveClaim(); cl != nil && ticket.HolderBot(cl.Holder) == ticket.HolderBot(r.Launcher) {
		req.Holder, req.Token = cl.Holder, cl.Token
		move.Holder, move.Token, move.Release = cl.Holder, cl.Token, true
	}
	if _, err := s.Comment(ctx, req); err != nil {
		return err
	}
	if _, err := s.Transition(ctx, move); err != nil {
		return err
	}
	out.Status = string(ticket.StatusBlocked)
	out.Wrote = append(out.Wrote, id+" blocked: its builds "+why)
	out.Send = append(out.Send, send{"planner", "block " + id + ": " + headLine(body)})
	return nil
}

// maxFrozenReruns is how often a run a freeze ended goes again at its own
// limit, per head: a host that sleeps every night reruns no job forever.
const maxFrozenReruns = 2

// frozenRuns counts the other runs of r's job on its ticket since since
// that a freeze ended.
func (c *call) frozenRuns(r runs.Record, since time.Time) int {
	n := 0
	for _, o := range c.allRuns() {
		if o.ID != r.ID && o.Job == r.Job && o.Ticket == r.Ticket && o.State == runs.TimedOut && o.Frozen && o.StartedAt.After(since) {
			n++
		}
	}
	return n
}

// hardStops counts the other builds on r's ticket since since that ran
// out of time or failed as a retry would answer: never a stop on purpose,
// a refusal or a start that never ran. A freeze past its reruns counts.
func (c *call) hardStops(r runs.Record, since time.Time) int {
	n := 0
	for _, o := range c.allRuns() {
		if o.ID == r.ID || o.Job != "build" || o.Ticket != r.Ticket || !o.Ended() || !o.StartedAt.After(since) {
			continue
		}
		if out, _ := routeOutcome(o); (o.State == runs.TimedOut && !o.Frozen) || c.retryableFail(o, out) {
			n++
		}
	}
	return n + max(c.frozenRuns(r, since)-maxFrozenReruns, 0)
}

// stoppedOnPurpose: someone stopped the run (maestro-ticket stop), or its
// launcher was stopped. No retry answers either.
func (c *call) stoppedOnPurpose(r runs.Record) bool {
	if dir, err := c.besideStore("runs"); err == nil {
		if _, ok := runs.StopAsked(dir, r.ID); ok {
			return true
		}
	}
	return r.LauncherStopped()
}

// retryableFail: r failed in a way a fresh run may not, such as an API
// error or a crash, not a refusal, a stop on purpose or a start that never
// ran.
func (c *call) retryableFail(r runs.Record, outcome string) bool {
	return outcome == outFailed && !neverRan(r) && !refusedText.MatchString(firstLine(r.Reply)) && !c.stoppedOnPurpose(r)
}

// buildStopped answers a build that ran out of time or failed: it runs
// again once at its head, with twice the time after a timeout, or at its
// limit after a freeze, twice. A second such stop at one head, a freeze
// past its reruns, or a third stop of any kind blocks the ticket for the
// judge. The dead session's claim goes first, plainly (no expiry), or the
// new session would find the ticket held.
func (c *call) buildStopped(ctx context.Context, s ticket.Store, r runs.Record, d ticket.Detail, out *routed) error {
	since := buildHead(d)
	again := "maestro-ticket job again " + r.ID
	switch {
	case r.Frozen && c.frozenRuns(r, since) < maxFrozenReruns:
	case c.stoppedBuilds(r, d) >= 2:
		return c.blockStopped(ctx, s, r, d, stoppedThrice, out)
	case c.hardStops(r, since) >= 1:
		return c.blockStopped(ctx, s, r, d, stoppedTwice, out)
	case out.Outcome == outTimedOut:
		again += " --limit " + short(min(2*r.LimitDuration(), 4*time.Hour))
	}
	if err := c.releaseOwn(ctx, s, d.Ticket, r, r.ID+"-release", out); err != nil {
		return err
	}
	out.Do = append(out.Do, again)
	return nil
}

// releaseOwn frees the live claim on t that r's bot holds, plainly, keyed
// by key, for the run that goes again. A claim that moved on meanwhile is
// no longer the dead session's: it stays.
func (c *call) releaseOwn(ctx context.Context, s ticket.Store, t ticket.Ticket, r runs.Record, key string, out *routed) error {
	cl := t.LiveClaim()
	if cl == nil || ticket.HolderBot(cl.Holder) != ticket.HolderBot(r.Launcher) {
		return nil
	}
	_, err := s.Release(ctx, ticket.ReleaseRequest{ID: t.ID, Holder: cl.Holder, Token: cl.Token, Actor: c.actor, IdempotencyKey: key})
	switch {
	case err == nil:
		out.Wrote = append(out.Wrote, "released "+t.ID.String()+" for the retry")
	case errors.Is(err, ticket.ErrLeaseLost), errors.Is(err, ticket.ErrClaimed), errors.Is(err, ticket.ErrIdempotencyConflict), errors.Is(err, ticket.ErrVersionConflict):
	default:
		return err
	}
	return nil
}

// planFailed answers a plan, rule or finish run that failed, timed out or
// refused with no card: it runs again once, in code. A second such end,
// a refusal, or a ticket another bot holds goes to the chief of staff as
// one fault line, and the ticket waits under Sent up: for the answer. A
// ticket that moved on needs neither. It reports whether the run goes
// again.
func (c *call) planFailed(ctx context.Context, s ticket.Store, r runs.Record, out *routed) (bool, error) {
	var t ticket.Ticket
	if r.Ticket != "" {
		tid, err := ticket.ParseID(r.Ticket)
		if err != nil {
			return false, err
		}
		d, err := s.Get(ctx, tid)
		if err != nil {
			return false, err
		}
		if t = d.Ticket; t.Status.Terminal() || t.Status == ticket.StatusInReview {
			out.Wrote = append(out.Wrote, r.Ticket+" moved on")
			out.quiet = true
			return false, nil
		}
	}
	cl := t.LiveClaim()
	held := cl != nil && ticket.HolderBot(cl.Holder) != ticket.HolderBot(r.Launcher)
	if out.Outcome != outRefused && !held && !c.retriedBefore(r) {
		if err := c.releaseOwn(ctx, s, t, r, r.ID+"-again-release", out); err != nil {
			return false, err
		}
		out.Do = append(out.Do, "maestro-ticket job again "+r.ID)
		return true, nil
	}
	what := "failed twice"
	switch {
	case out.Outcome == outRefused:
		what = "refused"
	case held:
		what = "failed"
	}
	line := what + " " + r.Job + " " + r.ID
	switch {
	case r.Ticket != "":
		line += " " + r.Ticket
	case r.Job == "plan":
		if g := c.goalSlug(r); g != "" {
			line += " goal " + g
		}
	}
	out.Send = append(out.Send, send{"chief-of-staff", line})
	if r.Ticket != "" && !held {
		if err := c.parkForAnswer(ctx, s, r, what, out); err != nil {
			out.Wrote = append(out.Wrote, "not parked for the answer: "+err.Error())
		} else if err := c.releaseOwn(ctx, s, t, r, r.ID+"-again-release", out); err != nil {
			return false, err
		}
	}
	return false, nil
}

// retriedBefore reports whether r, or a run its session resumed from, was
// a job again: a run goes again once.
func (c *call) retriedBefore(r runs.Record) bool {
	byID := map[string]runs.Record{}
	for _, o := range c.allRuns() {
		byID[o.ID] = o
	}
	for range 50 {
		if r.AgainOf != "" {
			return true
		}
		p, ok := byID[r.ResumeOf]
		if !ok {
			return false
		}
		r = p
	}
	return false
}

// goalText is how a goal plan's prompt names its goal.
var goalText = regexp.MustCompile(`\bgoal ([a-z0-9][a-z0-9-]{0,59}):`)

// goalSlug is the goal a goal plan's first prompt names, or "".
func (c *call) goalSlug(r runs.Record) string {
	dir, err := c.besideStore("runs")
	if err != nil {
		return ""
	}
	byID := map[string]runs.Record{}
	for _, o := range c.allRuns() {
		byID[o.ID] = o
	}
	for range 50 {
		p, ok := byID[r.ResumeOf]
		if !ok {
			break
		}
		r = p
	}
	b, _ := os.ReadFile(filepath.Join(dir, r.ID+".in"))
	if m := goalText.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

// postedBy marks a note pickup wrote for a run, so a later pickup of the
// same run knows it and routes as the first did.
func postedBy(r runs.Record) string { return "Posted-by: pickup " + r.ID }

// sessionOf is the Claude session a run worked in.
func sessionOf(r runs.Record) string {
	return cmp.Or(r.SessionID, flagValue(r.Command, "--session-id"), flagValue(r.Command, "--resume"))
}

// allRuns lists every run, acked ones too, once per call: a pickup asks
// for it for each run it routes.
func (c *call) allRuns() []runs.Record {
	list, _ := c.pastRuns()
	return list
}

// pastRuns is allRuns with the error that listing met, for a check that
// must refuse rather than pass on a list it couldn't read.
func (c *call) pastRuns() ([]runs.Record, error) {
	if c.runCache == nil {
		c.runCache = &pastRuns{}
	}
	l := c.runCache
	l.once.Do(func() {
		var dir string
		if dir, l.err = c.besideStore("runs"); l.err == nil {
			l.list, l.err = runs.List(dir, time.Now(), true)
		}
	})
	return l.list, l.err
}

// pastRuns is allRuns' list, read once however many copies of a call share
// it: every record of the last days, each up to a whole reply, so a sweep
// that routes many runs at once reads them once, not once a run.
type pastRuns struct {
	once sync.Once
	list []runs.Record
	err  error
}

// sessionHad reports whether a run that resumed r's session, and started,
// matches has.
func (c *call) sessionHad(r runs.Record, has func(runs.Record) bool) bool {
	session := sessionOf(r)
	if session == "" {
		return false
	}
	for _, o := range c.allRuns() {
		if flagValue(o.Command, "--resume") == session && !neverRan(o) && has(o) {
			return true
		}
	}
	return false
}

// nudged reports whether r's session had the fixed resume nudge once.
func (c *call) nudged(r runs.Record, nudge string) bool {
	return r.Nudge == nudge || c.sessionHad(r, func(o runs.Record) bool { return o.Nudge == nudge })
}

// diedBefore reports whether r's session was resumed once after a death
// already, as resume counts it: a second death goes up, not to a resume
// resume refuses.
func (c *call) diedBefore(r runs.Record) bool {
	return c.sessionHad(r, func(o runs.Record) bool { return o.AfterDeath })
}

// stoppedMidWork offers a resume to a run its supervisor stopped as the
// machine shut down, before its work landed: like a died run's, its
// session goes on where it stopped, once, and a second stop goes up.
func (c *call) stoppedMidWork(r runs.Record, id string, out *routed) bool {
	if !r.LauncherStopped() {
		return false
	}
	if r.AfterDeath || c.diedBefore(r) {
		out.Send = append(out.Send, send{"planner", strings.TrimSpace("died twice " + id)})
	} else {
		out.Do = append(out.Do, "maestro-ticket resume "+r.ID+" --died")
	}
	return true
}

// neverRan: a run whose supervisor never started it counts for nothing.
func neverRan(r runs.Record) bool { return r.State == runs.Failed && r.PID == 0 }

// timedOutBefore reports whether another run of r's job on its ticket ran
// out of time since since: a second timeout at one head goes up. One a
// freeze caused (Frozen) counts as none, up to its reruns.
func (c *call) timedOutBefore(r runs.Record, since time.Time) bool {
	for _, o := range c.allRuns() {
		if o.ID != r.ID && o.Ticket == r.Ticket && o.Job == r.Job && o.State == runs.TimedOut && !o.Frozen && o.StartedAt.After(since) {
			return true
		}
	}
	return c.frozenRuns(r, since) > maxFrozenReruns
}

// movedIntoReview is when d last moved into review, from its events, else
// when it was made.
func movedIntoReview(ctx context.Context, s ticket.Store, d ticket.Detail) (time.Time, error) {
	evs, err := s.Events(ctx, ticket.EventFilter{TicketID: d.ID, Type: ticket.EventTransitioned})
	if err != nil {
		return time.Time{}, err
	}
	for i := len(evs) - 1; i >= 0; i-- {
		var data struct{ To ticket.Status }
		if json.Unmarshal(evs[i].Data, &data) == nil && data.To == ticket.StatusInReview {
			return evs[i].CreatedAt, nil
		}
	}
	return d.CreatedAt, nil
}

// qaTimedOut blocks a ticket whose QA ran out of time twice at one GO,
// with an Escalation the planner answers, unless a verdict landed.
func (c *call) qaTimedOut(ctx context.Context, s ticket.Store, r runs.Record, d ticket.Detail, out routed) (routed, error) {
	t, id := d.Ticket, d.ID.String()
	planner := func(line string) { out.Send = append(out.Send, send{"planner", line}) }
	gate, ok := latestWith(d, "Review: GO")
	if t.Status == ticket.StatusBlocked && strings.Contains(latest(d).Body, postedBy(r)) {
		planner("block " + id + ": QA ran past " + r.Limit + " twice") // as the first pickup of this run said
		return out, nil
	}
	if t.Status != ticket.StatusInReview || t.LiveClaim() != nil || !ok {
		planner("timed_out " + id)
		return out, nil
	}
	if r.Frozen && c.frozenRuns(r, gate.CreatedAt) < maxFrozenReruns {
		out.Do = append(out.Do, "maestro-ticket job again "+r.ID) // a freeze ended it: the same limit again
		return out, nil
	}
	if !c.timedOutBefore(r, gate.CreatedAt) {
		out.Do = append(out.Do, fmt.Sprintf("maestro-ticket job again %s --limit %s", r.ID, short(min(2*r.LimitDuration(), 4*time.Hour))))
		return out, nil
	}
	sha := shaIn(gate.Body)
	body := fmt.Sprintf("Escalation: QA at %s: ran past %s twice: a hung app or a test too big for one run?\n%s\nReturn-role: qa-lead", sha, r.Limit, postedBy(r))
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
	co, err := c.coord()
	if err != nil {
		return err
	}
	mine, hid := key+" "+sender, fmt.Sprintf("%s-design%d", d.ID, e.ID)
	if !co.claimHandout(hid, "chief-of-staff", mine, 24*time.Hour, time.Now()) {
		if co.handoutBody(hid, "chief-of-staff") != mine {
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
		co.dropHandout(hid, "chief-of-staff") // the next one tries again
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

func quoteFirst(body string) string {
	if body == "" {
		return "with no note"
	}
	return "under " + strings.SplitN(firstLine(body), ":", 2)[0] + ":"
}

// followUpLines are the problems a review or QA run let ship or found
// outside its ticket: a review's "## Follow-ups" section ("## Out of
// scope" in a report from before the rename), and the "New tickets:" line
// of QA's verdict and the "Open:" line of its reply.
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
			case strings.HasPrefix(l, "## Follow-ups"), strings.HasPrefix(l, "## Out of scope"):
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

// fileFollowUps puts what a run let ship or found outside its ticket on the
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
