package cli

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/sqlitestore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// routeHarness builds tickets with the CLI and routes made-up runs against
// them, as pickup does.
type routeHarness struct {
	*harness
	s ticket.Store
	c *call
}

func newRouteHarness(t *testing.T) *routeHarness {
	h := newHarness(t)
	h.ok(nil, "doctor")
	s, err := sqlitestore.Open(context.Background(), h.env["MAESTRO_TICKET_STORE"], sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return &routeHarness{harness: h, s: s, c: &call{actor: "reviewer"}}
}

// comment posts a note on a ticket.
func (h *routeHarness) comment(id, body string) {
	h.t.Helper()
	if code, out, errs := h.run(body, "comment", id, "--body-file", "-"); code != 0 {
		h.t.Fatalf("comment %s: %d %s %s", id, code, out, errs)
	}
}

// claimed records that bot claimed the ticket once, then let go.
func (h *routeHarness) claimed(id, bot string) {
	h.t.Helper()
	var cl struct{ Claim struct{ Token int64 } }
	h.ok(&cl, "claim", id, "--holder", bot+"/s1", "--status", "todo,in_progress,in_review")
	h.ok(nil, "release", id, "--holder", bot+"/s1", "--token", strconv.FormatInt(cl.Claim.Token, 10))
}

// move walks a ticket through statuses.
func (h *routeHarness) move(id string, to ...string) {
	h.t.Helper()
	for _, st := range to {
		args := []string{"transition", id, "--to", st}
		if st == "done" {
			args = append(args, "--actor-role", "qa-lead")
		}
		h.ok(nil, args...)
	}
}

func (h *routeHarness) route(r runs.Record) routed {
	h.t.Helper()
	if r.State == "" {
		r.State = runs.Done
	}
	if r.ID == "" {
		r.ID = "R-aaaaaaaa"
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now().Add(-time.Hour)
	}
	rt, err := h.c.route(context.Background(), h.s, r)
	if err != nil {
		h.t.Fatalf("route %+v: %v", r, err)
	}
	return rt
}

func sends(rt routed) []string {
	out := []string{}
	for _, s := range rt.Send {
		out = append(out, s.String())
	}
	return out
}

func sonnetCmd() []string { return claudeCommand("S1", modelSonnet, effortHigh, false) }
func opusCmd() []string   { return claudeCommand("S1", modelOpus, effortHigh, false) }

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestRouteBuild(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a", "--tier", "junior")
	h.ok(nil, "create", "--title", "b")
	h.move("T-1", "in_progress", "in_review")

	rt := h.route(runs.Record{Job: "build", Ticket: "T-1", Command: sonnetCmd(), Reply: "build: done: T-1 PR https://x/pull/1 at abc"})
	if got := sends(rt); !slices.Equal(got, []string{"send reviewer: review T-1", "send planner: slot free T-1"}) || rt.Outcome != outDone || rt.Status != "in_review" {
		t.Fatalf("handoff with work waiting: %+v", rt)
	}
	h.move("T-2", "in_progress")
	if got := sends(h.route(runs.Record{Job: "build", Ticket: "T-1", Command: sonnetCmd()})); !slices.Equal(got, []string{"send reviewer: review T-1"}) {
		t.Fatalf("no ready work, no slot message: %v", got)
	}

	rt = h.route(runs.Record{Job: "build", Ticket: "T-2", Command: sonnetCmd(), Reply: "Plan...\nShould the cache be per user?"})
	if !slices.Equal(rt.Do, []string{"maestro-ticket resume R-aaaaaaaa --opus"}) || len(rt.Send) != 0 || rt.Reply != "" || rt.Outcome != outQuestion {
		t.Fatalf("a junior's question goes to Opus, unread: %+v", rt)
	}
	rt = h.route(runs.Record{Job: "build", Ticket: "T-2", Command: opusCmd(), Reply: "Plan...\nShould the cache be per user?"})
	if got := sends(rt); len(got) != 1 || !strings.HasPrefix(got[0], "send planner: block T-2") || len(rt.Do) != 0 {
		t.Fatalf("Opus's question goes up: %+v", rt)
	}
	h.move("T-2", "blocked")
	h.comment("T-2", "Escalation: outside step: make a Stripe test account: owner's call\nReturn-role: engineer")
	rt = h.route(runs.Record{Job: "build", Ticket: "T-2", Command: sonnetCmd(), Reply: "build: blocked: T-2"})
	if got := sends(rt); len(got) != 1 || !strings.Contains(got[0], "block T-2: Escalation: outside step") || len(rt.Do) != 0 {
		t.Fatalf("an outside step goes up at once: %+v", rt)
	}

	h.ok(nil, "create", "--title", "c")
	h.move("T-3", "in_progress")
	rt = h.route(runs.Record{Job: "build", Ticket: "T-3", Command: opusCmd(), Reply: "build: waiting_on_deps: T-3"})
	if got := sends(rt); len(got) != 1 || !strings.HasPrefix(got[0], "send planner: build T-3 stopped in_progress") || rt.Mismatch == "" {
		t.Fatalf("a build that stopped short: %+v", rt)
	}
}

func TestRouteReview(t *testing.T) {
	h := newRouteHarness(t)
	for _, tk := range []string{"junior", "junior", "senior", ""} {
		args := []string{"create", "--title", "x"}
		if tk != "" {
			args = append(args, "--tier", tk)
		}
		h.ok(nil, args...)
	}
	h.move("T-1", "in_progress", "in_review")
	h.comment("T-1", "Review: GO at "+sha+"\nFor QA: the login form")
	if got := sends(h.route(runs.Record{Job: "review", Ticket: "T-1"})); !slices.Equal(got, []string{"send qa-lead: qa T-1 at " + sha}) {
		t.Fatalf("GO: %v", got)
	}

	// T-2, a junior's ticket: sent back once, then twice.
	h.claimed("T-2", "junior-engineer")
	h.move("T-2", "in_progress", "in_review", "in_progress")
	h.comment("T-2", "Kickback: at abc1234\nF1 major: ...")
	if got := sends(h.route(runs.Record{Job: "review", Ticket: "T-2"})); !slices.Equal(got, []string{"send junior-engineer: build T-2"}) {
		t.Fatalf("first kickback: same engineer: %v", got)
	}
	h.move("T-2", "in_review", "in_progress")
	h.comment("T-2", "Kickback: at abc1235")
	if got := sends(h.route(runs.Record{Job: "review", Ticket: "T-2"})); !slices.Equal(got, []string{"send senior-engineer: build T-2"}) {
		t.Fatalf("a junior's second kickback goes to the senior: %v", got)
	}
	h.move("T-2", "in_review", "in_progress")
	h.comment("T-2", "Kickback: at abc1236")
	if got := sends(h.route(runs.Record{Job: "review", Ticket: "T-2"})); !slices.Equal(got, []string{"send planner: kickback T-2 tries 3"}) {
		t.Fatalf("TRIES 3: the planner: %v", got)
	}

	// T-3, a senior's: its second kickback goes to the planner.
	h.claimed("T-3", "senior-engineer")
	h.move("T-3", "in_progress", "in_review", "in_progress", "in_review", "in_progress")
	h.comment("T-3", "Kickback: at abc1234")
	if got := sends(h.route(runs.Record{Job: "review", Ticket: "T-3"})); !slices.Equal(got, []string{"send planner: kickback T-3 tries 2"}) {
		t.Fatalf("a senior's second kickback: %v", got)
	}

	h.move("T-4", "in_progress", "in_review")
	rt := h.route(runs.Record{Job: "review", Ticket: "T-4", Reply: "review: stopped: tier 1 needs the full model at abc"})
	if !slices.Equal(rt.Do, []string{"maestro-ticket job review T-4 --full"}) {
		t.Fatalf("a light review that needs the full model: %+v", rt)
	}
	rt = h.route(runs.Record{Job: "review", Ticket: "T-4", Reply: "review: refused: T-4 is claimed by reviewer/x"})
	if rt.Outcome != outRefused || len(rt.Send) != 1 || !strings.HasPrefix(rt.Send[0].Line, "refused T-4") {
		t.Fatalf("refused: %+v", rt)
	}

	// A review that ran past its limit blocks the ticket, once.
	h.comment("T-4", "Handoff: PR https://x/pull/4 at "+sha+"\nchecks: 0")
	timedOut := runs.Record{ID: "R-bbbbbbbb", Job: "review", Ticket: "T-4", State: runs.TimedOut, Limit: "45m", StartedAt: time.Now().Add(-time.Minute)}
	rt = h.route(timedOut)
	if rt.Status != "blocked" || len(rt.Wrote) != 1 || !strings.HasPrefix(rt.Send[0].Line, "block T-4") {
		t.Fatalf("timed out: %+v", rt)
	}
	d, _ := h.s.Get(context.Background(), 4)
	want := "Escalation: review at " + sha + ": ran past 45m\nReturn-role: engineer"
	if last := latest(d).Body; last != want || d.Status != ticket.StatusBlocked {
		t.Fatalf("escalation %q %s", last, d.Status)
	}
	h.route(timedOut) // a second pickup of the same run
	if d2, _ := h.s.Get(context.Background(), 4); len(d2.Comments) != len(d.Comments) || d2.Version != d.Version {
		t.Fatalf("the second pickup wrote again: %d comments, v%d", len(d2.Comments), d2.Version)
	}
}

func TestRouteQA(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a", "--goal", "demo")
	h.ok(nil, "create", "--title", "b", "--goal", "demo")
	h.ok(nil, "create", "--title", "c", "--tier", "junior")
	h.move("T-1", "in_progress", "in_review", "done")
	if got := sends(h.route(runs.Record{Job: "qa", Ticket: "T-1"})); len(got) != 0 {
		t.Fatalf("a pass with the goal still open: %v", got)
	}
	h.move("T-2", "cancelled")
	if got := sends(h.route(runs.Record{Job: "qa", Ticket: "T-1"})); !slices.Equal(got, []string{"send planner: goal demo done"}) {
		t.Fatalf("the merge that finished its goal: %v", got)
	}
	h.claimed("T-3", "junior-engineer")
	h.move("T-3", "in_progress", "in_review", "in_progress")
	h.comment("T-3", "Kickback: QA fail at abc1234 (PR x), tier 1, on main at def")
	if got := sends(h.route(runs.Record{Job: "qa", Ticket: "T-3"})); !slices.Equal(got, []string{"send junior-engineer: build T-3"}) {
		t.Fatalf("QA fail: %v", got)
	}
	h.move("T-3", "in_review")
	h.comment("T-3", "Rereview: QA at abc1240: review passed abc1234")
	if got := sends(h.route(runs.Record{Job: "qa", Ticket: "T-3", Reply: "test: refused"})); !slices.Equal(got, []string{"send reviewer: review T-3"}) {
		t.Fatalf("Rereview: %v", got)
	}
}

func TestRouteOutcomes(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")

	rt := h.route(runs.Record{Job: "build", Ticket: "T-1", State: runs.Failed, IsError: true, Reply: "Claude AI usage limit reached|1763150400"})
	if rt.Outcome != outUsageLimit || rt.Reset != "2025-11-14T20:00:00Z" || !slices.Equal(sends(rt), []string{"send planner: usage_limit T-1 resets 2025-11-14T20:00:00Z"}) {
		t.Fatalf("usage limit: %+v", rt)
	}
	rt = h.route(runs.Record{Job: "build", Ticket: "T-1", Reply: "You've hit your limit · resets 3pm (UTC)"})
	if rt.Outcome != outUsageLimit || rt.Reset != "3pm (UTC)" {
		t.Fatalf("usage limit text: %+v", rt)
	}
	if rt := h.route(runs.Record{Job: "build", Ticket: "T-1", Reply: "build: done: T-1\nAdded a rate limit of 5 per minute, as the criteria ask."}); rt.Outcome != outDone {
		t.Fatalf("a build about rate limits is no limit: %+v", rt)
	}
	rt = h.route(runs.Record{Job: "review", Ticket: "T-1", Reply: "infra block: disk full at /tmp"})
	if rt.Outcome != outDiskFull || !slices.Equal(sends(rt), []string{"send planner: disk_full T-1"}) {
		t.Fatalf("disk full: %+v", rt)
	}
	rt = h.route(runs.Record{Job: "build", Ticket: "T-1", State: runs.Died})
	if !slices.Equal(rt.Do, []string{"maestro-ticket resume R-aaaaaaaa --died"}) {
		t.Fatalf("died: %+v", rt)
	}
	rt = h.route(runs.Record{Job: "build", Ticket: "T-1", State: runs.Died, AfterDeath: true})
	if !slices.Equal(sends(rt), []string{"send planner: died twice T-1"}) {
		t.Fatalf("died after a resume: %+v", rt)
	}
	rt = h.route(runs.Record{Job: "judge", Reply: "judge: done: 2 decided, 1 up\nsend senior-engineer: build T-4\nsend reviewer: review T-1 --drop F2: the test covers it\nup: T-5 What: x | Why now: y | Options: a; b | Default: a | Question: a?"})
	if got := sends(rt); !slices.Equal(got, []string{"send senior-engineer: build T-4", "send reviewer: review T-1 --drop F2: the test covers it", "send chief-of-staff: T-5 What: x | Why now: y | Options: a; b | Default: a | Question: a?"}) || rt.Reply != "" {
		t.Fatalf("judge: %v", got)
	}
	rt = h.route(runs.Record{Job: "plan", Reply: "plan: done: T-7 T-8\nT-7 junior ..."})
	if rt.Reply == "" || len(rt.Send) != 0 {
		t.Fatalf("a text job's reply is the answer: %+v", rt)
	}
}
