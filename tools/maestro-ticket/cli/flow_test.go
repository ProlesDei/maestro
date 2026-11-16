package cli

import (
	"strings"
	"testing"
	"time"
)

// flow counts each stage from the status changes, splits a stay in review
// at review's GO and at QA's Rereview:, counts send-backs by who sent
// them, and leaves out open tickets, scouts and time in backlog.
func TestFlowStagesAndSendBacks(t *testing.T) {
	h := newRouteHarness(t)
	clock := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return clock }
	step := func(d time.Duration) { clock = clock.Add(d) }

	// T-1: 10m queue, 30m build, 20m review, kicked back, 10m build, 5m
	// review, 15m QA, a rereview of 10m, 5m QA, done.
	h.ok(nil, "create", "--title", "one", "--goal", "speed")
	step(10 * time.Minute)
	h.move("T-1", "in_progress")
	step(30 * time.Minute)
	h.move("T-1", "in_review")
	step(20 * time.Minute)
	h.comment("T-1", "Kickback: 1 blockers at aaa\n- F1 [blocker, auto-fix] a.go:1: x")
	h.move("T-1", "in_progress")
	step(10 * time.Minute)
	h.move("T-1", "in_review")
	step(5 * time.Minute)
	h.comment("T-1", "Review: GO at bbb")
	step(15 * time.Minute)
	h.comment("T-1", "Rereview: QA at ccc: review passed bbb")
	step(10 * time.Minute)
	h.comment("T-1", "Review: GO at ccc")
	step(5 * time.Minute)
	h.move("T-1", "done")

	// T-2: parked an hour, then straight through, with a QA fail.
	h.ok(nil, "create", "--title", "two", "--status", "backlog", "--parked", "deferred")
	step(time.Hour)
	h.move("T-2", "todo", "in_progress", "in_review")
	h.comment("T-2", "Review: GO at ddd")
	step(20 * time.Minute)
	h.comment("T-2", "Kickback: QA fail at ddd (PR u), tier 1, on main at eee")
	h.move("T-2", "in_progress", "in_review")
	h.comment("T-2", "Review: GO at fff")
	h.move("T-2", "done")

	// Left out: an open ticket, a scout and a cancelled one.
	h.ok(nil, "create", "--title", "open")
	h.ok(nil, "create", "--title", "scout", "--type", "scout", "--owner", "planner")
	h.ok(nil, "create", "--title", "dropped")
	h.move("T-5", "cancelled")

	var got flowReport
	h.ok(&got, "flow")
	if got.Tickets != 2 || got.ReviewSendBacks != 1 || got.QASendBacks != 1 || got.FirstPassPercent != 0 {
		t.Fatalf("totals: %+v", got)
	}
	want := map[string]float64{"queue": 600, "build": 2400, "review": 2100, "qa": 2400, "blocked": 0}
	for _, s := range got.Stages {
		if s.Total != want[s.Stage] {
			t.Errorf("%s: total %v; want %v", s.Stage, s.Total, want[s.Stage])
		}
	}
	// T-1 ran 105 minutes from its start; T-2's hour in backlog counts
	// nowhere, so its 20 minutes start when it left backlog.
	if len(got.Slowest) != 2 || got.Slowest[0].ID != 1 || got.Slowest[0].Lead != 6300 || got.Slowest[1].Lead != 1200 || got.Slowest[0].SendBacks != 1 {
		t.Fatalf("slowest: %+v", got.Slowest)
	}

	// --goal and --since narrow it; the text names the worst stage.
	h.ok(&got, "flow", "--goal", "speed")
	if got.Tickets != 1 || got.Slowest[0].ID != 1 {
		t.Fatalf("by goal: %+v", got)
	}
	h.ok(&got, "flow", "--since", "10m")
	if got.Tickets != 1 || got.Slowest[0].ID != 2 {
		t.Fatalf("since 10m: %+v", got)
	}
	_, out, _ := h.run("", "flow")
	for _, s := range []string{"flow: 2 ship tickets done; start to done:", "sent back: 1 by review, 1 by QA; 0% of tickets passed both first time", "slowest: T-1 1h45m (1 sent back), T-2 20m (1 sent back)"} {
		if !strings.Contains(out, s) {
			t.Errorf("text lacks %q:\n%s", s, out)
		}
	}
	h.ok(&got, "flow", "--goal", "none-such")
	if _, out, _ := h.run("", "flow", "--goal", "none-such"); got.Tickets != 0 || !strings.Contains(out, "flow: no ship ticket done of goal none-such") {
		t.Fatalf("empty: %+v %s", got, out)
	}
	if code := h.fails(2, "flow", "--since", "soon"); code == "" {
		t.Fatal("a bad --since passed")
	}
	h.ok(&got, "flow", "--since", "7d")
	if got.Tickets != 2 {
		t.Fatalf("since 7d: %+v", got)
	}
}

// QA's time after its escalation is answered is QA's, though no new GO is
// posted; a stay in backlog mid-life counts nowhere, the lead included;
// and a send-back whose note was posted twice counts once.
func TestFlowBlockedBacklogAndReposts(t *testing.T) {
	h := newRouteHarness(t)
	clock := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return clock }
	step := func(d time.Duration) { clock = clock.Add(d) }

	h.ok(nil, "create", "--title", "one")
	h.move("T-1", "in_progress", "in_review")
	step(5 * time.Minute)
	h.comment("T-1", "Review: GO at aaa")
	step(10 * time.Minute)
	h.comment("T-1", "Escalation: QA at aaa: no browser\nReturn-role: engineer")
	h.move("T-1", "blocked")
	step(20 * time.Minute)
	h.comment("T-1", "Answer: it is there now")
	h.move("T-1", "in_review")
	step(30 * time.Minute)
	h.comment("T-1", "Kickback: QA fail at aaa (PR u), tier 1, on main at g")
	h.comment("T-1", "Kickback: QA fail at aaa (PR u), tier 1, on main at g")
	h.move("T-1", "in_progress", "todo")
	h.ok(nil, "transition", "T-1", "--to", "backlog", "--parked", "hold")
	step(time.Hour)
	h.move("T-1", "todo", "in_progress", "in_review")
	h.comment("T-1", "Review: GO at bbb")
	h.move("T-1", "done")

	var got flowReport
	h.ok(&got, "flow")
	want := map[string]float64{"queue": 0, "build": 0, "review": 300, "qa": 2400, "blocked": 1200}
	for _, s := range got.Stages {
		if s.Total != want[s.Stage] {
			t.Errorf("%s: total %v; want %v", s.Stage, s.Total, want[s.Stage])
		}
	}
	if got.QASendBacks != 1 || got.ReviewSendBacks != 0 || got.Slowest[0].Lead != 3900 {
		t.Fatalf("send-backs or lead: %+v", got)
	}
}
