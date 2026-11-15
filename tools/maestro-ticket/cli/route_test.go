package cli

import (
	"context"
	"os"
	"path/filepath"
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
	env := Env{Getenv: func(k string) string { return h.env[k] }, Dir: h.dir}
	return &routeHarness{harness: h, s: s, c: &call{actor: "reviewer", env: env}}
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

// timedOut records an earlier run of job on the ticket that ran out of
// time, as the supervisor would have, starting now.
func (h *routeHarness) timedOut(id, job string) {
	h.t.Helper()
	dir := filepath.Join(h.dir, "runs")
	os.MkdirAll(dir, 0o755)
	r, err := runs.Create(dir, runs.Record{Launcher: "reviewer", Limit: "45m", Command: []string{"true"}}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	now := time.Now()
	r.State, r.Ticket, r.Job, r.StartedAt, r.EndedAt = runs.TimedOut, id, job, now, &now
	if err := runs.Save(dir, r); err != nil {
		h.t.Fatal(err)
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
	h.c.runCache = nil // as each pickup lists the runs afresh
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
	if !slices.Equal(rt.Do, []string{"maestro-ticket resume R-aaaaaaaa --escalate"}) || len(rt.Send) != 0 {
		t.Fatalf("Opus asked but posted nothing: it writes the question up: %+v", rt)
	}

	// Escalated, an Opus session's block goes to the planner, and its claim
	// goes with it, so the judge needn't wait out the lease.
	h.ok(nil, "create", "--title", "esc")
	h.move("T-3", "in_progress")
	var cl struct{ Claim struct{ Token int64 } }
	h.ok(&cl, "claim", "T-3", "--holder", "senior-engineer/s7", "--status", "in_progress")
	tok := strconv.FormatInt(cl.Claim.Token, 10)
	if code, out, errs := h.run("Escalation: build at abc: which key?\nReturn-role: engineer", "comment", "T-3", "--body-file", "-", "--holder", "senior-engineer/s7", "--token", tok); code != 0 {
		t.Fatalf("comment: %s %s", out, errs)
	}
	h.ok(nil, "transition", "T-3", "--to", "blocked", "--holder", "senior-engineer/s7", "--token", tok)
	rt = h.route(runs.Record{ID: "R-bbbbbbbb", Job: "build", Ticket: "T-3", Launcher: "senior-engineer", Command: opusCmd(), Reply: "build: blocked: T-3"})
	if got := sends(rt); len(got) != 1 || !strings.HasPrefix(got[0], "send planner: block T-3: Escalation: build at abc") || !slices.Equal(rt.Wrote, []string{"released T-3 for the planner"}) {
		t.Fatalf("escalated: %+v", rt)
	}
	if d, _ := h.s.Get(context.Background(), 3); d.LiveClaim() != nil {
		t.Fatalf("the claim stayed: %+v", d.Claim)
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
	if got := sends(h.route(runs.Record{Job: "review", Ticket: "T-1"})); !slices.Equal(got, []string{"send qa-lead: qa T-1"}) {
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
	if !slices.Equal(rt.Do, []string{"maestro-ticket job again R-aaaaaaaa --full"}) {
		t.Fatalf("a light review that needs the full model: %+v", rt)
	}
	rt = h.route(runs.Record{Job: "review", Ticket: "T-4", Reply: "review: refused: T-4 is claimed by reviewer/x"})
	if rt.Outcome != outRefused || len(rt.Send) != 1 || !strings.HasPrefix(rt.Send[0].Line, "refused T-4") {
		t.Fatalf("refused: %+v", rt)
	}

	// A review that ran past its limit blocks the ticket, once.
	h.comment("T-4", "Handoff: PR https://x/pull/4 at "+sha+"\nchecks: 0")
	h.timedOut("T-4", "review") // once already at this head
	timedOut := runs.Record{ID: "R-bbbbbbbb", Job: "review", Ticket: "T-4", State: runs.TimedOut, Limit: "45m", StartedAt: time.Now()}
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
	if got := sends(h.route(runs.Record{Job: "qa", Ticket: "T-1"})); !slices.Equal(got, []string{"send planner: merged T-1"}) {
		t.Fatalf("a pass with the goal still open wakes the planner: %v", got)
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
	// The reset passed long ago: no pause, and the job simply runs again.
	if rt.Outcome != outUsageLimit || rt.Reset != "2025-11-14T20:00:00Z" || len(rt.Send) != 0 || !slices.Equal(rt.Do, []string{"maestro-ticket resume R-aaaaaaaa --go-on"}) {
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
	if rt.Outcome != outDiskFull || !slices.Equal(sends(rt), []string{"send planner: disk_full T-1: infra block: disk full at /tmp"}) {
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
	if rt.Reply != "" || len(rt.Send) != 0 || rt.First != "plan: done: T-7 T-8" {
		t.Fatalf("the planner reads a plan's first line only: %+v", rt)
	}
	rt = h.route(runs.Record{ID: "R-pppppppp", Job: "plan", Reply: "plan: draft: goal demo\n...\nup: What: a paid API | Question: pay $20?"})
	if got := sends(rt); !slices.Equal(got, []string{"send chief-of-staff: plan R-pppppppp: What: a paid API | Question: pay $20?"}) {
		t.Fatalf("a plan's owner call goes up, naming its run: %v", got)
	}
	rt = h.route(runs.Record{ID: "R-pppppppp", Job: "plan", Reply: "plan: stopped: which area is the cart in?"})
	if !slices.Equal(rt.Do, []string{"maestro-ticket resume R-pppppppp --decide"}) {
		t.Fatalf("a plan's question: the session decides: %+v", rt)
	}
	if rt := h.route(runs.Record{Job: "cos", Reply: "card: ..."}); rt.Reply == "" {
		t.Fatalf("a cos reply is the answer: %+v", rt)
	}
}

// Review and QA put what they found outside the ticket on its area's
// Follow-ups ticket, made once; no bot reads the lines.
func TestRouteFilesFollowUps(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a", "--areas", "api/auth,web", "--goal", "demo")
	h.move("T-1", "in_progress", "in_review")
	h.comment("T-1", "Review: GO at "+sha)
	reply := "# Code review: T-1 a\nVerdict: GO\n## Findings\n| F1 | minor |\n## Out of scope   (already on the base)\napi/auth:\n- urgent: tokens never expire (api/auth/token.go:40)\n- logs print emails\n## Outcome  comment 4"
	run := runs.Record{ID: "R-cccccccc", Job: "review", Ticket: "T-1", Reply: reply}
	rt := h.route(run)
	if len(rt.Wrote) != 1 || rt.Wrote[0] != "3 follow-ups from T-1 on T-2" {
		t.Fatalf("follow-ups: %+v", rt)
	}
	var d struct {
		Title, Status, Parked, Goal string
		Areas                       []string
		Comments                    []struct{ Body string }
	}
	h.ok(&d, "get", "T-2", "--full")
	if d.Title != "Follow-ups: api/auth" || d.Status != "backlog" || d.Parked != "deferred" || d.Goal != "" || len(d.Areas) != 1 {
		t.Fatalf("follow-ups ticket: %+v", d)
	}
	want := "From T-1 review (R-cccccccc):\n- api/auth:\n- urgent: tokens never expire (api/auth/token.go:40)\n- logs print emails"
	if len(d.Comments) != 1 || d.Comments[0].Body != want {
		t.Fatalf("follow-ups note: %q", d.Comments)
	}
	h.route(run) // a second pickup
	h.ok(&d, "get", "T-2", "--full")
	if len(d.Comments) != 1 {
		t.Fatalf("a second pickup filed again: %q", d.Comments)
	}

	// QA: the verdict's New tickets line and the reply's Open line, on the
	// same Follow-ups ticket.
	h.move("T-1", "done")
	h.comment("T-1", "QA: pass at "+sha+"\n- 1 pass: ok\nNew tickets: the login page flickers on Safari")
	rt = h.route(runs.Record{ID: "R-dddddddd", Job: "qa", Ticket: "T-1", Reply: "test: pass: T-1 at x\nOpen: none\nNext: none"})
	if len(rt.Wrote) != 1 || rt.Wrote[0] != "1 follow-ups from T-1 on T-2" {
		t.Fatalf("QA follow-ups: %+v", rt)
	}
	if rt := h.route(runs.Record{ID: "R-eeeeeeee", Job: "qa", Ticket: "T-1", Reply: "test: pass\nOpen: none", StartedAt: time.Now().Add(time.Hour)}); len(rt.Wrote) != 0 {
		t.Fatalf("a QA run files only its own verdict's lines: %+v", rt)
	}
}

// A ready design goes up as the card its session wrote, once.
func TestRouteDesignReady(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "Design: demo", "--type", "scout", "--role", "designer", "--owner", "planner")
	h.move("T-1", "in_progress", "blocked")
	h.comment("T-1", "Design spec: pick B\nCriteria: ...")
	h.comment("T-1", "Escalation: design ready: PR https://x/pull/9: owner's call\nWhat: two directions\nQuestion: approved: A or B, or changes?")
	run := runs.Record{ID: "R-ffffffff", Job: "design", Ticket: "T-1", Reply: "design: ready: T-1 at abc1234"}
	rt := h.route(run)
	want := "send chief-of-staff: T-1 Escalation: design ready: PR https://x/pull/9: owner's call | What: two directions | Question: approved: A or B, or changes?"
	if got := sends(rt); !slices.Equal(got, []string{want}) || len(rt.Wrote) != 1 {
		t.Fatalf("design ready: %v %v", got, rt.Wrote)
	}
	if rt := h.route(run); !slices.Equal(sends(rt), []string{want}) {
		t.Fatalf("a second pickup of the run, before its ack, shows the card again: %+v", rt)
	}
	d0, _ := h.s.Get(context.Background(), 1)
	var rt2 routed
	if err := h.c.designUp(context.Background(), h.s, d0, "look", &rt2); err != nil || len(rt2.Send) != 0 {
		t.Fatalf("a look sends the card pickup sent: %+v %v", rt2, err)
	}
	d, _ := h.s.Get(context.Background(), 1)
	if last := latest(d).Body; last != "Sent up: chief of staff: approve the design on T-1?" {
		t.Fatalf("Sent up: %q", last)
	}
}

// A run out of time goes again once with twice the time; a second at the
// same head blocks the ticket for the planner.
func TestRouteTimeouts(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	h.comment("T-1", "Handoff: PR https://x/pull/1 at "+sha)
	first := runs.Record{ID: "R-tttttttt", Job: "review", Ticket: "T-1", State: runs.TimedOut, Limit: "45m", StartedAt: time.Now().Add(-time.Minute), Command: opusCmd()}
	rt := h.route(first)
	if !slices.Equal(rt.Do, []string{"maestro-ticket job again R-tttttttt --limit 1h30m"}) || len(rt.Wrote) != 0 {
		t.Fatalf("first timeout: %+v", rt)
	}
	h.timedOut("T-1", "review")
	second := runs.Record{ID: "R-uuuuuuuu", Job: "review", Ticket: "T-1", State: runs.TimedOut, Limit: "1h30m", StartedAt: time.Now(), Command: opusCmd()}
	rt = h.route(second)
	if rt.Status != "blocked" || len(rt.Wrote) != 1 {
		t.Fatalf("second timeout: %+v", rt)
	}

	// QA the same way, at a GO.
	h.ok(nil, "create", "--title", "b")
	h.move("T-2", "in_progress", "in_review")
	h.comment("T-2", "Review: GO at "+sha)
	q := runs.Record{ID: "R-vvvvvvvv", Job: "qa", Ticket: "T-2", State: runs.TimedOut, Limit: "2h", StartedAt: time.Now().Add(-time.Minute), Command: opusCmd()}
	if rt := h.route(q); !slices.Equal(rt.Do, []string{"maestro-ticket job again R-vvvvvvvv --limit 4h"}) {
		t.Fatalf("QA's first timeout: %+v", rt)
	}
	h.timedOut("T-2", "qa")
	q.ID, q.Limit = "R-wwwwwwww", "4h"
	rt = h.route(q)
	if rt.Status != "blocked" || !strings.HasPrefix(rt.Send[0].Line, "block T-2: QA ran past 4h twice") {
		t.Fatalf("QA's second timeout: %+v", rt)
	}
	d, _ := h.s.Get(context.Background(), 2)
	if !strings.HasPrefix(latest(d).Body, "Escalation: QA at "+sha+": ran past 4h twice") {
		t.Fatalf("QA escalation: %q", latest(d).Body)
	}
}

// A usage limit pauses every job until it resets.
func TestRouteUsageLimitPauses(t *testing.T) {
	h := newRouteHarness(t)
	reset := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	rt := h.route(runs.Record{Job: "build", Ticket: "", State: runs.Failed, IsError: true, Reply: "Claude AI usage limit reached|" + strconv.FormatInt(reset.Unix(), 10)})
	if rt.Outcome != outUsageLimit || len(rt.Wrote) != 1 {
		t.Fatalf("usage limit: %+v", rt)
	}
	p, ok := readPause(h.dir, time.Now())
	if !ok || !p.Until.Equal(reset) {
		t.Fatalf("pause: %+v %v", p, ok)
	}
	if msg := h.fails(4, "job", "plan", "--actor", "planner"); msg != "paused" {
		t.Fatalf("job while paused: %q", msg)
	}
	if code, out, _ := h.run("", "look", "--limit", "2", "--actor", "planner"); code != 0 || !strings.HasPrefix(out, "paused: usage limit") {
		t.Fatalf("look while paused: %d %s", code, out)
	}
	os.WriteFile(filepath.Join(h.dir, "STOP"), nil, 0o644)
	if msg := h.fails(4, "job", "plan", "--actor", "planner"); msg != "stopped" {
		t.Fatalf("job under STOP: %q", msg)
	}
}

func TestRouteNamesAKilledRun(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	code := 137
	rt := h.route(runs.Record{Job: "build", Ticket: "T-1", State: runs.Failed, Exit: &code, Command: opusCmd()})
	if rt.First != "exit 137, no reply" || rt.Outcome != outFailed {
		t.Fatalf("killed: %+v", rt)
	}
}

func TestRouteRoundTwo(t *testing.T) {
	h := newRouteHarness(t)
	// Only Claude Code's own words are a usage limit.
	for _, r := range []runs.Record{
		{Job: "build", State: runs.Failed, Error: "gh: API rate limit exceeded", Reply: "build: failed"},
		{Job: "build", Reply: "Handoff: the banner shows 'usage limit reached' when over"},
	} {
		if rt := h.route(r); rt.Outcome == outUsageLimit {
			t.Fatalf("no limit: %+v", rt)
		}
	}
	// A plan whose reply ends in a card sends it once, and decides nothing.
	rt := h.route(runs.Record{ID: "R-pppppppp", Job: "plan", Reply: "plan: draft: demo\nup: What: x | Question: a or b?"})
	if len(rt.Send) != 1 || len(rt.Do) != 0 {
		t.Fatalf("a plan's last card: %+v", rt)
	}
	// A pause whose reset passed is set no more.
	past := time.Now().Add(-time.Minute)
	h.route(runs.Record{Job: "build", State: runs.Failed, IsError: true, EndedAt: &past, Reply: "Claude AI usage limit reached|" + strconv.FormatInt(past.Unix(), 10)})
	if _, ok := readPause(h.dir, time.Now()); ok {
		t.Fatal("paused after the reset")
	}
	// A claimed Follow-ups ticket doesn't stop the route.
	h.ok(nil, "create", "--title", "a", "--areas", "api")
	h.ok(nil, "create", "--title", "Follow-ups: api", "--status", "backlog", "--parked", "deferred", "--areas", "api")
	h.ok(nil, "claim", "T-2", "--holder", "planner/s9", "--status", "backlog")
	h.move("T-1", "in_progress", "in_review")
	h.comment("T-1", "Review: GO at "+sha)
	rt = h.route(runs.Record{ID: "R-qqqqqqqq", Job: "review", Ticket: "T-1", Reply: "## Out of scope\n- logs print emails\n## Outcome"})
	if !slices.Equal(sends(rt), []string{"send qa-lead: qa T-1"}) || len(rt.Wrote) != 1 || !strings.HasPrefix(rt.Wrote[0], "follow-ups not filed") {
		t.Fatalf("follow-ups failed: %+v", rt)
	}
}

// A usage limit loses no work: the job runs again once it lifts, and the
// stopped session's claim goes, counting no expiry.
func TestRouteUsageLimitKeepsWork(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress")
	h.ok(nil, "claim", "T-1", "--holder", "junior-engineer/s1", "--status", "in_progress")
	future := time.Now().Add(time.Hour)
	rt := h.route(runs.Record{ID: "R-llllllll", Job: "build", Ticket: "T-1", Launcher: "junior-engineer", State: runs.Failed, IsError: true, EndedAt: &future,
		Reply: "Claude AI usage limit reached|" + strconv.FormatInt(future.Unix(), 10)})
	if !slices.Equal(rt.Do, []string{"maestro-ticket resume R-llllllll --go-on"}) || !slices.Contains(rt.Wrote, "released T-1 till the limit lifts") {
		t.Fatalf("usage limit: %+v", rt)
	}
	if d, _ := h.s.Get(context.Background(), 1); d.LiveClaim() != nil || d.LeaseExpiries != 0 {
		t.Fatalf("claim: %+v, expiries %d", d.Claim, d.LeaseExpiries)
	}
}

// A session told once to post its question that asks again goes up.
func TestRouteNudgeOnce(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress")
	rt := h.route(runs.Record{ID: "R-nnnnnnnn", Job: "build", Ticket: "T-1", Nudge: "escalate", Command: opusCmd(), Reply: "Which key should I use?"})
	if len(rt.Do) != 0 || !slices.Equal(sends(rt), []string{"send planner: block T-1: Escalation: build: Which key should I use?"}) || rt.Status != "blocked" {
		t.Fatalf("asked again: %+v", rt)
	}
	if d, _ := h.s.Get(context.Background(), 1); latest(d).Body != "Escalation: build: Which key should I use?\nPosted-by: pickup R-nnnnnnnn\nReturn-role: engineer" {
		t.Fatalf("posted: %q", latest(d).Body)
	}
	// A second pickup of the run routes the same, and resumes nothing.
	again := h.route(runs.Record{ID: "R-nnnnnnnn", Job: "build", Ticket: "T-1", Nudge: "escalate", Command: sonnetCmd(), Reply: "Which key should I use?"})
	if len(again.Do) != 0 || !slices.Equal(sends(again), sends(rt)) {
		t.Fatalf("second pickup: %+v", again)
	}
	h.ok(nil, "create", "--title", "b")
	h.move("T-2", "in_progress")
	rt = h.route(runs.Record{ID: "R-oooooooo", Job: "build", Ticket: "T-2", Nudge: "opus", Command: opusCmd(), Reply: "Which key?"})
	if !slices.Equal(rt.Do, []string{"maestro-ticket resume R-oooooooo --escalate"}) {
		t.Fatalf("after --opus, once --escalate: %+v", rt)
	}
}

// A usage limit resumes a session where it stopped, and a judge's rows
// stay held for it.
func TestRouteUsageLimitGoesOn(t *testing.T) {
	h := newRouteHarness(t)
	hdir := filepath.Join(h.dir, "handouts")
	os.MkdirAll(hdir, 0o755)
	os.WriteFile(handoutPath(hdir, "T-4", "judge"), []byte("blocked v1 1\n"), 0o644)
	rt := h.route(runs.Record{ID: "R-gggggggg", Job: "judge", Tickets: []string{"T-4"}, State: runs.Failed, IsError: true,
		Command: opusCmd(), Reply: "Claude AI usage limit reached|" + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)})
	if !slices.Equal(rt.Do, []string{"maestro-ticket resume R-gggggggg --go-on"}) {
		t.Fatalf("a limited session goes on: %+v", rt)
	}
	if _, err := os.Stat(handoutPath(hdir, "T-4", "judge")); err != nil {
		t.Fatal("a limited judge freed its rows")
	}
}

// Three builds that stop with nothing to show block the ticket.
func TestRouteStoppedBuilds(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress")
	dir := filepath.Join(h.dir, "runs")
	os.MkdirAll(dir, 0o755)
	for range 2 {
		r, _ := runs.Create(dir, runs.Record{Launcher: "senior-engineer", Limit: "2h", Command: []string{"true"}}, nil)
		now := time.Now()
		r.State, r.Ticket, r.Job, r.StartedAt, r.EndedAt = runs.Done, "T-1", "build", now, &now
		runs.Save(dir, r)
	}
	rt := h.route(runs.Record{ID: "R-ssssssss", Job: "build", Ticket: "T-1", StartedAt: time.Now(), Command: opusCmd(), Reply: "build: waiting_on_deps: T-1"})
	if rt.Status != "blocked" || len(rt.Send) != 1 || !strings.HasPrefix(rt.Send[0].Line, "block T-1: Escalation: build stopped 3 times") {
		t.Fatalf("stopped thrice: %+v", rt)
	}
}

func TestRouteRoundFive(t *testing.T) {
	h := newRouteHarness(t)
	// A review that timed out after its verdict sends nothing to rerun.
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review", "in_progress")
	h.comment("T-1", "Kickback: at abc1234")
	rt := h.route(runs.Record{ID: "R-r1r1r1r1", Job: "review", Ticket: "T-1", State: runs.TimedOut, Limit: "45m", StartedAt: time.Now().Add(-time.Hour), Command: opusCmd()})
	if len(rt.Do) != 0 {
		t.Fatalf("a rerun after the verdict: %+v", rt)
	}
	// With no Handoff:, the second timeout stops rerunning.
	h.ok(nil, "create", "--title", "b")
	h.move("T-2", "in_progress", "in_review")
	h.timedOut("T-2", "review")
	rt = h.route(runs.Record{ID: "R-r2r2r2r2", Job: "review", Ticket: "T-2", State: runs.TimedOut, Limit: "45m", StartedAt: time.Now(), Command: opusCmd()})
	if len(rt.Do) != 0 {
		t.Fatalf("no Handoff:, timed out twice: %+v", rt)
	}
	// A planning session that asks again after --decide goes up.
	rt = h.route(runs.Record{ID: "R-r3r3r3r3", Job: "plan", Nudge: "decide", Command: opusCmd(), Reply: "plan: stopped\nWhich area is the cart in?"})
	if len(rt.Do) != 0 || !slices.Equal(sends(rt), []string{"send chief-of-staff: plan R-r3r3r3r3: Question: Which area is the cart in?"}) {
		t.Fatalf("asked again after --decide: %+v", rt)
	}
	// pickup writes under no other bot's lease.
	h.ok(nil, "create", "--title", "c")
	h.move("T-3", "in_progress")
	h.ok(nil, "claim", "T-3", "--holder", "senior-engineer/other", "--status", "in_progress")
	rt = h.route(runs.Record{ID: "R-r4r4r4r4", Job: "build", Ticket: "T-3", Launcher: "junior-engineer", Nudge: "escalate", Command: opusCmd(), Reply: "Which key?"})
	if len(rt.Wrote) != 0 {
		t.Fatalf("wrote under another's lease: %+v", rt)
	}
	if d, _ := h.s.Get(context.Background(), 3); d.LiveClaim() == nil || d.Status != ticket.StatusInProgress {
		t.Fatalf("another's claim was touched: %+v", d.Ticket)
	}
}

// Builds that crash before a session claims the ticket count too.
func TestRouteCrashedBuildsInTodo(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	dir := filepath.Join(h.dir, "runs")
	os.MkdirAll(dir, 0o755)
	code := 1
	for range 2 {
		r, _ := runs.Create(dir, runs.Record{Launcher: "senior-engineer", Limit: "2h", Command: []string{"true"}}, nil)
		now := time.Now()
		r.State, r.Ticket, r.Job, r.StartedAt, r.EndedAt, r.Exit, r.PID = runs.Failed, "T-1", "build", now, &now, &code, 4242
		runs.Save(dir, r)
	}
	rt := h.route(runs.Record{ID: "R-cccccccc", Job: "build", Ticket: "T-1", State: runs.Failed, Exit: &code, StartedAt: time.Now(), Command: opusCmd(), Error: "boom"})
	if rt.Status != "blocked" {
		t.Fatalf("crashed thrice in todo: %+v", rt)
	}
}
