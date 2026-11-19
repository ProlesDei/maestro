package cli

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// saveRun records r as an ended run in the runs folder, as its supervisor
// would have, and returns it with its id.
func (h *routeHarness) saveRun(r runs.Record) runs.Record {
	h.t.Helper()
	dir := filepath.Join(h.dir, "runs")
	os.MkdirAll(dir, 0o755)
	c, err := runs.Create(dir, runs.Record{Launcher: cmp.Or(r.Launcher, "senior-engineer"), Limit: cmp.Or(r.Limit, "2h"), Command: []string{"true"}}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	r.ID, r.Launcher, r.Limit = c.ID, c.Launcher, c.Limit
	if len(r.Command) == 0 {
		r.Command = c.Command
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now()
	}
	if r.EndedAt == nil {
		now := time.Now()
		r.EndedAt = &now
	}
	if r.PID == 0 {
		r.PID = 4242
	}
	if err := runs.Save(dir, r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *routeHarness) ticketOf(n ticket.ID) ticket.Detail {
	h.t.Helper()
	d, err := h.s.Get(context.Background(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	return d
}

// A build that runs out of time goes again once, in code, with twice the
// time, and the dead session's claim goes plainly; the second timeout at
// one head blocks the ticket for the judge. Claimed or not, in progress or
// still in todo.
func TestBuildTimeoutLoopBlocks(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.ok(nil, "create", "--title", "b")
	h.move("T-1", "in_progress")
	h.claim("T-1", "senior-engineer/s1")
	first := h.saveRun(runs.Record{Job: "build", Ticket: "T-1", State: runs.TimedOut})
	rt := h.route(first)
	if !slices.Equal(rt.Do, []string{"maestro-ticket job again " + first.ID + " --limit 4h"}) || len(rt.Send) != 0 || !slices.Equal(rt.Wrote, []string{"released T-1 for the retry"}) {
		t.Fatalf("first timeout: %+v", rt)
	}
	if d := h.ticketOf(1); d.LiveClaim() != nil || d.LeaseExpiries != 0 {
		t.Fatalf("claim %+v, expiries %d", d.Claim, d.LeaseExpiries)
	}
	second := h.saveRun(runs.Record{Job: "build", Ticket: "T-1", State: runs.TimedOut, Limit: "4h"})
	rt = h.route(second)
	if rt.Status != "blocked" || len(rt.Do) != 0 || len(rt.Send) != 1 || !strings.HasPrefix(rt.Send[0].Line, "block T-1: Escalation: build timed out or failed twice") {
		t.Fatalf("second timeout: %+v", rt)
	}
	if !slices.Contains(rt.Wrote, "T-1 blocked: its builds timed out or failed twice") {
		t.Fatalf("wrote: %q", rt.Wrote)
	}

	// T-2 timed out in todo, before any session claimed it.
	r := h.saveRun(runs.Record{Job: "build", Ticket: "T-2", State: runs.TimedOut})
	if rt := h.route(r); !slices.Equal(rt.Do, []string{"maestro-ticket job again " + r.ID + " --limit 4h"}) || len(rt.Wrote) != 0 {
		t.Fatalf("todo, first: %+v", rt)
	}
	r = h.saveRun(runs.Record{Job: "build", Ticket: "T-2", State: runs.TimedOut})
	if rt := h.route(r); rt.Status != "blocked" || len(rt.Do) != 0 {
		t.Fatalf("todo, second: %+v", rt)
	}
}

// Two builds that ended with nothing to show, then a timeout: the third
// stop blocks at once, with no retry.
func TestBuildTimeoutMixedStops(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress")
	for range 2 {
		h.saveRun(runs.Record{Job: "build", Ticket: "T-1", State: runs.Done, Reply: "build: done"})
	}
	rt := h.route(h.saveRun(runs.Record{Job: "build", Ticket: "T-1", State: runs.TimedOut}))
	if rt.Status != "blocked" || len(rt.Do) != 0 || !strings.HasPrefix(rt.Send[0].Line, "block T-1: Escalation: build stopped 3 times") {
		t.Fatalf("mixed stops: %+v", rt)
	}
}

// A timed-out build on a ticket another bot holds writes nothing and
// keeps its planner line.
func TestBuildTimeoutHeldByOther(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress")
	h.claim("T-1", "junior-engineer/s1")
	before := h.ticketOf(1)
	rt := h.route(h.saveRun(runs.Record{Job: "build", Ticket: "T-1", State: runs.TimedOut}))
	if !slices.Equal(sends(rt), []string{"send planner: timed_out T-1"}) || len(rt.Do) != 0 || len(rt.Wrote) != 0 {
		t.Fatalf("held by another: %+v", rt)
	}
	if after := h.ticketOf(1); after.Version != before.Version || after.LiveClaim() == nil {
		t.Fatalf("wrote under another's claim: v%d -> v%d", before.Version, after.Version)
	}
}

// A freeze reruns a build at its limit twice; the third goes with twice
// the time, as a timeout's one retry; the fourth blocks.
func TestBuildFrozenRerunsCapped(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress")
	want := []string{"", "", " --limit 4h", "block"}
	for i, w := range want {
		r := h.saveRun(runs.Record{Job: "build", Ticket: "T-1", State: runs.TimedOut, Frozen: true})
		rt := h.route(r)
		if w == "block" {
			if rt.Status != "blocked" || len(rt.Do) != 0 {
				t.Fatalf("freeze %d: %+v", i+1, rt)
			}
			continue
		}
		if !slices.Equal(rt.Do, []string{"maestro-ticket job again " + r.ID + w}) {
			t.Fatalf("freeze %d: %+v", i+1, rt)
		}
	}
}

// A build that failed goes again once at the same limit; a second failure
// blocks. A refusal that exited non-zero, a stop asked, a stopped launcher
// and a start that never ran go again never.
func TestBuildFailedRetriesOnce(t *testing.T) {
	h := newRouteHarness(t)
	for _, title := range []string{"a", "b", "c"} {
		h.ok(nil, "create", "--title", title)
	}
	h.move("T-1", "in_progress")
	h.claim("T-1", "senior-engineer/s1")
	fail := runs.Record{Job: "build", Ticket: "T-1", State: runs.Failed, IsError: true, Exit: exitCode(1), Reply: "Error: the session crashed"}
	first := h.saveRun(fail)
	rt := h.route(first)
	if !slices.Equal(rt.Do, []string{"maestro-ticket job again " + first.ID}) || len(rt.Send) != 0 || !slices.Contains(rt.Wrote, "released T-1 for the retry") {
		t.Fatalf("first failure: %+v", rt)
	}
	rt = h.route(h.saveRun(fail))
	if rt.Status != "blocked" || len(rt.Do) != 0 || !strings.HasPrefix(rt.Send[0].Line, "block T-1: Escalation: build timed out or failed twice") {
		t.Fatalf("second failure: %+v", rt)
	}

	h.move("T-2", "in_progress")
	dir := filepath.Join(h.dir, "runs")
	asked := h.saveRun(runs.Record{Job: "build", Ticket: "T-2", State: runs.Failed, Exit: exitCode(143), Error: "stopped by planner"})
	runs.AskStop(dir, asked.ID, "planner", time.Now())
	launcher := h.saveRun(runs.Record{Job: "build", Ticket: "T-2", State: runs.Failed, Exit: exitCode(143), Error: runs.LauncherStoppedText})
	if rt := h.route(asked); len(rt.Do) != 0 || len(rt.Send) != 1 || !strings.HasPrefix(rt.Send[0].Line, "build T-2 stopped") {
		t.Fatalf("a stop on purpose: %+v", rt)
	}
	// A shutdown stopped it mid-work: it goes on as a died run would, no
	// retry spent.
	if rt := h.route(launcher); !slices.Equal(rt.Do, []string{"maestro-ticket resume " + launcher.ID + " --died"}) || len(rt.Send) != 0 {
		t.Fatalf("stopped at a shutdown: %+v", rt)
	}
	refused := h.saveRun(runs.Record{Job: "build", Ticket: "T-2", State: runs.Failed, Exit: exitCode(1), Reply: "build: refused: tier 3 needs senior"})
	if rt := h.route(refused); len(rt.Do) != 0 || !slices.Equal(sends(rt), []string{"send planner: refused T-2: build: refused: tier 3 needs senior"}) {
		t.Fatalf("a refusal that exited 1: %+v", rt)
	}
	if rt := h.route(runs.Record{Job: "build", Ticket: "T-3", State: runs.Failed, Command: opusCmd()}); len(rt.Do) != 0 {
		t.Fatalf("a start that never ran: %+v", rt)
	}
}

// An API outage is the team's, not the build's: its run waits on a short
// pause for every job and goes on as it lifts, spending no retry, so a
// second one at the same head blocks nothing. Stats keep it failed.
func TestBuildAPIOutagePauses(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress")
	h.claim("T-1", "senior-engineer/s1")
	fail := runs.Record{Job: "build", Ticket: "T-1", State: runs.Failed, IsError: true, Exit: exitCode(1), Reply: "API Error: 529 overloaded"}
	for i := range 2 {
		r := h.saveRun(fail)
		rt := h.route(r)
		if rt.Outcome != outUsageLimit || rt.Status == "blocked" || !slices.Equal(rt.Do, []string{"maestro-ticket resume " + r.ID + " --go-on"}) {
			t.Fatalf("outage %d: %+v", i, rt)
		}
		p, ok := readPause(h.dir, time.Now())
		if !ok || !strings.HasPrefix(p.Why, "API outage resets ") || p.Until.Sub(*r.EndedAt) > apiPause+time.Second {
			t.Fatalf("outage %d: pause %+v %v", i, p, ok)
		}
		if i == 0 && (!slices.Equal(sends(rt), []string{"send planner: usage_limit T-1 resets " + p.Until.Format(time.RFC3339)}) || !slices.Contains(rt.Wrote, "released T-1 till the limit lifts")) {
			t.Fatalf("outage %d: %+v", i, rt)
		}
		if _, ok := bodyLines(rt); !ok {
			t.Fatalf("outage %d: held from the body: %+v", i, rt)
		}
		if out, _ := outcomeOf(r); out != outFailed {
			t.Fatalf("stats read outage %d as %s", i, out)
		}
		os.Remove(pausePath(h.dir))
	}
	for reply, want := range map[string]bool{
		"API Error: 500 Internal server error":      true,
		"API Error: Repeated 529 Overloaded errors": true,
		"API Error: 400 bad request":                false,
		"Error: the session crashed":                false,
	} {
		if got := apiDown(runs.Record{State: runs.Failed, IsError: true, Reply: reply}); got != want {
			t.Errorf("%q: outage %v", reply, got)
		}
	}
	if apiDown(runs.Record{State: runs.Done, Reply: "API Error: 500 is what the handler returns"}) {
		t.Error("a done reply about API errors is an outage")
	}
}

// A first failure after one stop on purpose still gets its retry.
func TestBuildStopAskedNotRetried(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress")
	dir := filepath.Join(h.dir, "runs")
	asked := h.saveRun(runs.Record{Job: "build", Ticket: "T-1", State: runs.Failed, Exit: exitCode(143), Error: "stopped by planner"})
	runs.AskStop(dir, asked.ID, "planner", time.Now())
	if rt := h.route(asked); len(rt.Do) != 0 || !strings.HasPrefix(sends(rt)[0], "send planner: build T-1 stopped") {
		t.Fatalf("asked: %+v", rt)
	}
	r := h.saveRun(runs.Record{Job: "build", Ticket: "T-1", State: runs.Failed, IsError: true, Exit: exitCode(1), Reply: "Error: the session crashed"})
	if rt := h.route(r); !slices.Equal(rt.Do, []string{"maestro-ticket job again " + r.ID}) {
		t.Fatalf("a failure after a stop asked: %+v", rt)
	}
}

// A freeze reruns a review at its limit twice per head, then a third
// with twice the time, then blocks; QA the same at a GO. With no
// Handoff:, the head is the move into review, which no comment moves.
func TestReviewFrozenCap(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	h.comment("T-1", "Handoff: PR https://x/pull/1 at "+sha)
	h.ok(nil, "create", "--title", "b")
	h.move("T-2", "in_progress", "in_review")
	h.comment("T-2", "Review: GO at "+sha)
	h.ok(nil, "create", "--title", "c")
	h.move("T-3", "in_progress", "in_review")
	for _, c := range []struct{ job, id, limit, twice string }{
		{"review", "T-1", "45m", "1h30m"},
		{"qa", "T-2", "2h", "4h"},
		{"review", "T-3", "45m", "1h30m"},
	} {
		for i, w := range []string{"", "", " --limit " + c.twice, "block"} {
			if c.id == "T-3" {
				h.comment(c.id, "a note between freezes") // moves updated_at, not the head
			}
			r := h.saveRun(runs.Record{Launcher: "reviewer", Job: c.job, Ticket: c.id, State: runs.TimedOut, Frozen: true, Limit: c.limit})
			rt := h.route(r)
			if w == "block" {
				if len(rt.Do) != 0 || len(rt.Send) != 1 || !strings.Contains(rt.Send[0].Line, c.id) {
					t.Fatalf("%s %s freeze %d: %+v", c.job, c.id, i+1, rt)
				}
				continue
			}
			if !slices.Equal(rt.Do, []string{"maestro-ticket job again " + r.ID + w}) {
				t.Fatalf("%s %s freeze %d: %+v", c.job, c.id, i+1, rt)
			}
		}
	}
	if d := h.ticketOf(1); d.Status != ticket.StatusBlocked {
		t.Fatalf("T-1 after four freezes: %s", d.Status)
	}
}

// The first review or QA run at a head that ends with no verdict runs
// again in code; the second goes to the planner as a mismatch; the third
// blocks for the judge.
func TestNoVerdictRetriesOnceThenMismatch(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	h.comment("T-1", "Handoff: PR x at "+sha)
	h.ok(nil, "create", "--title", "b")
	h.move("T-2", "in_progress", "in_review")
	h.comment("T-2", "Review: GO at "+sha)
	for _, c := range []struct{ job, id string }{{"review", "T-1"}, {"qa", "T-2"}} {
		r := h.saveRun(runs.Record{Launcher: "reviewer", Job: c.job, Ticket: c.id, State: runs.Done, Reply: "done, I think"})
		if rt := h.route(r); !slices.Equal(rt.Do, []string{"maestro-ticket job again " + r.ID}) || len(rt.Send) != 0 {
			t.Fatalf("%s, first: %+v", c.job, rt)
		}
		r = h.saveRun(runs.Record{Launcher: "reviewer", Job: c.job, Ticket: c.id, State: runs.Done, Reply: "done, I think"})
		if rt := h.route(r); len(rt.Do) != 0 || !slices.Equal(sends(rt), []string{"send planner: mismatch " + c.id}) {
			t.Fatalf("%s, second: %+v", c.job, rt)
		}
		r = h.saveRun(runs.Record{Launcher: "reviewer", Job: c.job, Ticket: c.id, State: runs.Done, Reply: "done, I think"})
		if rt := h.route(r); rt.Status != "blocked" {
			t.Fatalf("%s, third: %+v", c.job, rt)
		}
	}
	// A stop asked is no end to retry.
	h.ok(nil, "create", "--title", "c")
	h.move("T-3", "in_progress", "in_review")
	r := h.saveRun(runs.Record{Launcher: "reviewer", Job: "review", Ticket: "T-3", State: runs.Failed, Error: "stopped by planner"})
	runs.AskStop(filepath.Join(h.dir, "runs"), r.ID, "planner", time.Now())
	if rt := h.route(r); len(rt.Do) != 0 || !slices.Equal(sends(rt), []string{"send planner: mismatch T-3"}) {
		t.Fatalf("a review stopped on purpose: %+v", rt)
	}
}

// A run someone stopped on purpose, such as a QA run stopped while it
// waited for its slot, is no strike: stops never block the ticket, and
// the run after them is still the first with no verdict.
func TestStopsOnPurposeAreNoStrikes(t *testing.T) {
	h := newRouteHarness(t)
	rdir := filepath.Join(h.dir, "runs")
	stopped := func(job, id string) {
		r := h.saveRun(runs.Record{Launcher: "qa-lead", Job: job, Ticket: id, State: runs.Failed, Error: "stopped by chief-of-staff"})
		runs.AskStop(rdir, r.ID, "chief-of-staff", time.Now())
	}
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	h.comment("T-1", "Review: GO at "+sha)
	h.ok(nil, "create", "--title", "b")
	h.move("T-2", "in_progress", "in_review")
	h.comment("T-2", "Handoff: PR x at "+sha)
	for _, c := range []struct{ job, id string }{{"qa", "T-1"}, {"review", "T-2"}} {
		stopped(c.job, c.id)
		stopped(c.job, c.id)
		// A third stop routes as a stop, not a third strike.
		r := h.saveRun(runs.Record{Launcher: "qa-lead", Job: c.job, Ticket: c.id, State: runs.Failed, Error: "stopped by chief-of-staff"})
		runs.AskStop(rdir, r.ID, "chief-of-staff", time.Now())
		if rt := h.route(r); rt.Status == "blocked" {
			t.Fatalf("%s: three stops blocked it: %+v", c.job, rt)
		}
		// The run after the stops that ends with no verdict is the first.
		r = h.saveRun(runs.Record{Launcher: "qa-lead", Job: c.job, Ticket: c.id, State: runs.Done, Reply: "done, I think"})
		if rt := h.route(r); !slices.Equal(rt.Do, []string{"maestro-ticket job again " + r.ID}) || rt.Status == "blocked" {
			t.Fatalf("%s: first no verdict after three stops: %+v", c.job, rt)
		}
	}
	if d := h.ticketOf(1); d.Status != ticket.StatusInReview {
		t.Fatalf("T-1 after stops: %s", d.Status)
	}
}

// A plan, rule or finish run that failed or timed out with no card runs
// again once, in code, and keeps holding its ticket; a second end like
// it goes to the chief of staff as one fault line, and its ticket waits
// under Sent up:, once however often it is routed.
func TestRoutePlanFailRetriesOnce(t *testing.T) {
	h := newRouteHarness(t)
	hdir := filepath.Join(h.dir, "handouts")
	os.MkdirAll(hdir, 0o755)
	n := 0
	for _, job := range []string{"plan", "rule", "finish"} {
		for _, state := range []string{runs.Failed, runs.TimedOut} {
			for _, ticketed := range []bool{false, true} {
				id := ""
				if ticketed {
					n++
					h.ok(nil, "create", "--title", "x")
					id = "T-" + strconv.Itoa(n)
					filesCoord(filepath.Join(filepath.Dir(hdir), "runs")).recordHandouts([]send{{"planner-job", id}})
				}
				r := h.saveRun(runs.Record{Launcher: "planner", Job: job, Ticket: id, State: state, IsError: state == runs.Failed, Reply: "Error: the session crashed"})
				rt := h.route(r)
				if !slices.Equal(rt.Do, []string{"maestro-ticket job again " + r.ID}) || len(rt.Send) != 0 {
					t.Fatalf("%s %s %q first: %+v", job, state, id, rt)
				}
				if ticketed && !fileExists(handoutPath(hdir, id, "planner-job")) {
					t.Fatalf("%s %s %s: the retry's handout went", job, state, id)
				}
				again := h.saveRun(runs.Record{Launcher: "planner", Job: job, Ticket: id, State: state, IsError: state == runs.Failed, AgainOf: r.ID, Reply: "Error: the session crashed"})
				rt = h.route(again)
				want := strings.TrimSpace("failed twice " + job + " " + again.ID + " " + id)
				if len(rt.Do) != 0 || !slices.Equal(sends(rt), []string{"send chief-of-staff: " + want}) {
					t.Fatalf("%s %s %q second: %+v", job, state, id, rt)
				}
				if _, ok := bodyLines(rt); !ok {
					t.Fatalf("the fault line is held from the body: %+v", rt)
				}
				if !ticketed {
					continue
				}
				d := h.ticketOf(ticket.ID(n))
				if latest(d).Body != "Sent up: chief of staff: the "+job+" run "+again.ID+" failed twice\n"+postedBy(again) {
					t.Fatalf("park note: %q", latest(d).Body)
				}
				h.route(again)
				if d2 := h.ticketOf(ticket.ID(n)); len(d2.Comments) != len(d.Comments) {
					t.Fatalf("a second route parked again")
				}
				if fileExists(handoutPath(hdir, id, "planner-job")) {
					t.Fatalf("%s: a fault kept its handout", id)
				}
			}
		}
	}
}

// A refused planning run goes up at once and parks its ticket.
func TestRouteRefusedPlanGoesUp(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "x")
	r := h.saveRun(runs.Record{Launcher: "planner", Job: "rule", Ticket: "T-1", State: runs.Done, Reply: "rule: refused: no decision given"})
	rt := h.route(r)
	if len(rt.Do) != 0 || !slices.Equal(sends(rt), []string{"send chief-of-staff: refused rule " + r.ID + " T-1"}) {
		t.Fatalf("refused: %+v", rt)
	}
	if d := h.ticketOf(1); !strings.HasPrefix(latest(d).Body, "Sent up: chief of staff: the rule run "+r.ID+" refused") {
		t.Fatalf("park: %q", latest(d).Body)
	}
}

// Before a planning run goes again, its dead session's claim goes, keyed;
// a claim another bot holds stays, and the fault goes up instead. A
// ticket that moved on needs neither.
func TestPlanAgainReleasesDeadClaim(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.ok(nil, "create", "--title", "b")
	h.ok(nil, "create", "--title", "c")
	h.claim("T-1", "planner/s1")
	h.claim("T-2", "senior-engineer/s1")
	r := h.saveRun(runs.Record{Launcher: "planner", Job: "plan", Ticket: "T-1", State: runs.TimedOut})
	rt := h.route(r)
	if !slices.Equal(rt.Do, []string{"maestro-ticket job again " + r.ID}) || !slices.Equal(rt.Wrote, []string{"released T-1 for the retry"}) {
		t.Fatalf("own claim: %+v", rt)
	}
	if d := h.ticketOf(1); d.LiveClaim() != nil || d.LeaseExpiries != 0 {
		t.Fatalf("claim %+v, expiries %d", d.Claim, d.LeaseExpiries)
	}
	r = h.saveRun(runs.Record{Launcher: "planner", Job: "plan", Ticket: "T-2", State: runs.TimedOut})
	rt = h.route(r)
	if len(rt.Do) != 0 || !slices.Equal(sends(rt), []string{"send chief-of-staff: failed plan " + r.ID + " T-2"}) || len(rt.Wrote) != 0 {
		t.Fatalf("another's claim: %+v", rt)
	}
	if d := h.ticketOf(2); d.LiveClaim() == nil {
		t.Fatal("another's claim went")
	}
	h.move("T-3", "in_progress", "in_review")
	r = h.saveRun(runs.Record{Launcher: "planner", Job: "plan", Ticket: "T-3", State: runs.TimedOut})
	rt = h.route(r)
	if len(rt.Do)+len(rt.Send) != 0 || !slices.Equal(rt.Wrote, []string{"T-3 moved on"}) || !ackable(rt) {
		t.Fatalf("moved on: %+v", rt)
	}
}

// A goal plan's fault names its goal, read from the prompt job wrote.
func TestGoalSlugFromPrompt(t *testing.T) {
	h := newRouteHarness(t)
	r := h.saveRun(runs.Record{Launcher: "planner", Job: "plan", State: runs.Failed, IsError: true, AgainOf: "R-aaaaaaaa"})
	os.WriteFile(filepath.Join(h.dir, "runs", r.ID+".in"), []byte("/maestro-planner plan goal foo-bar: ship the cart\nStanding rules:\n- goal x: y\n"), 0o644)
	if rt := h.route(r); !slices.Equal(sends(rt), []string{"send chief-of-staff: failed twice plan " + r.ID + " goal foo-bar"}) {
		t.Fatalf("goal plan: %+v", rt)
	}
}

// Every end short of done gets a line, a command or a reply, unless its
// ticket closed; and only such a route is acked once nothing is left.
func TestRouteNonDoneNeverEmpty(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	h.ok(nil, "create", "--title", "b")
	h.move("T-2", "in_progress", "in_review", "done")
	ends := []runs.Record{
		{State: runs.Failed, IsError: true, Reply: "Error: the session crashed"},
		{State: runs.TimedOut},
		{State: runs.Done, Reply: "x: refused: no"},
		{State: runs.Done, Reply: "which one?"},
	}
	for _, k := range jobKinds {
		for _, e := range ends {
			for _, id := range []string{"", "T-1"} {
				r := e
				r.Job, r.Ticket, r.Launcher = k.name, id, "planner"
				r = h.saveRun(r)
				rt := h.route(r)
				if len(rt.Send)+len(rt.Do) == 0 && rt.Reply == "" {
					t.Errorf("%s %s on %q: nothing routed: %+v", k.name, rt.Outcome, id, rt)
				}
			}
		}
	}
	// A judge that failed with no line goes up as a fault.
	r := h.saveRun(runs.Record{Launcher: "planner", Job: "judge", Tickets: []string{"T-1"}, State: runs.Failed, IsError: true, Reply: "Error: the session crashed"})
	if rt := h.route(r); !slices.Equal(sends(rt), []string{"send chief-of-staff: judge failed " + r.ID + ": nothing routed"}) {
		t.Fatalf("judge: %+v", rt)
	}
	// A failed cos run's asker reads why.
	if rt := h.route(h.saveRun(runs.Record{Launcher: "planner", Job: "cos", State: runs.Failed, Error: "boom"})); rt.Reply != "boom" {
		t.Fatalf("cos: %+v", rt)
	}
	// QA that merged, then failed: its ticket closed, nothing to route.
	r = h.saveRun(runs.Record{Launcher: "qa-lead", Job: "qa", Ticket: "T-2", State: runs.Failed, IsError: true, Reply: "error_max_turns"})
	if rt := h.route(r); len(rt.Send) != 0 {
		t.Fatalf("closed: %+v", rt)
	}
	for _, c := range []struct {
		rt   routed
		want bool
	}{
		{routed{Outcome: outDone}, true},
		{routed{Outcome: outFailed}, false},
		{routed{Outcome: outFailed, Send: []send{{"planner", "x"}}}, true},
		{routed{Outcome: outTimedOut, Do: []string{"maestro-ticket job again R-aaaaaaaa"}}, true},
		{routed{Outcome: outFailed, Reply: "boom"}, true},
		{routed{Outcome: outFailed, quiet: true}, true},
	} {
		if got := ackable(c.rt); got != c.want {
			t.Errorf("ackable %+v: %v; want %v", c.rt, got, c.want)
		}
	}
}
