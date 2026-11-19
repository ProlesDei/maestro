//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// liveQARun saves a QA run on id that is going, its supervisor's lock
// held by this test until the cleanup, as a run waiting in its slot is.
func (h *routeHarness) liveQARun(id string) runs.Record {
	h.t.Helper()
	dir := filepath.Join(h.dir, "runs")
	os.MkdirAll(dir, 0o755)
	r, err := runs.Create(dir, runs.Record{Launcher: "qa-lead", Limit: "2h", Command: []string{"true"}}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, r.ID+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { lock.Close() })
	r.State, r.Ticket, r.Job, r.StartedAt, r.PID = runs.Running, id, "qa", time.Now(), os.Getpid()
	if err := runs.Save(dir, r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

// Look hands QA out only to free QA slots: a QA run going, or a QA line
// handed out and not yet run, holds one, and the rest wait for a later
// look instead of a run that would wait in line inside slot.
func TestLookHandsQAOnlyToFreeSlots(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "rules", "set", "qa_slots", "2", "--actor", "paul")
	for _, id := range []string{"T-1", "T-2", "T-3", "T-4"} {
		h.ok(nil, "create", "--title", "a")
		h.move(id, "in_progress", "in_review")
		h.comment(id, "Review: GO at "+sha)
	}
	r := h.liveQARun("T-1")
	qaSends := func(out lookOut) []string {
		var l []string
		for _, s := range out.Send {
			if s.Bot == "qa-lead" {
				l = append(l, s.Line)
			}
		}
		return l
	}
	var out lookOut
	h.ok(&out, "look", "--limit", "10", "--actor", "planner")
	if got := qaSends(out); !slices.Equal(got, []string{"qa T-2"}) || !strings.HasSuffix(out.Counts, "; qa 2/2, 2 waiting for a slot") {
		t.Fatalf("one slot free: %q, counts %q", got, out.Counts)
	}
	// T-2's line stands and holds the second slot: nothing more goes.
	h.ok(&out, "look", "--limit", "10", "--actor", "planner")
	if got := qaSends(out); len(got) != 0 || !strings.HasSuffix(out.Counts, "; qa 2/2, 2 waiting for a slot") {
		t.Fatalf("both slots held: %q, counts %q", got, out.Counts)
	}
	// T-1's run ends: its slot goes to the next in line.
	now := time.Now()
	r.State, r.EndedAt = runs.Done, &now
	runs.Save(filepath.Join(h.dir, "runs"), r)
	h.ok(&out, "look", "--limit", "10", "--actor", "planner")
	if got := qaSends(out); !slices.Equal(got, []string{"qa T-3"}) {
		t.Fatalf("a slot freed: %q, counts %q", got, out.Counts)
	}
	// With slots to spare, counts say nothing of QA.
	h.ok(nil, "rules", "set", "qa_slots", "6", "--actor", "paul")
	h.ok(&out, "look", "--limit", "10", "--actor", "planner")
	if got := qaSends(out); !slices.Equal(got, []string{"qa T-4"}) || strings.Contains(out.Counts, "qa ") {
		t.Fatalf("slots to spare: %q, counts %q", got, out.Counts)
	}
}
