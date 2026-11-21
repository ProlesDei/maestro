//go:build unix

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// newRefillHarness is a board with the planner's limit set to limit and n
// todo tickets, T-1 junior and the rest senior.
func newRefillHarness(t *testing.T, limit string, n int) *routeHarness {
	h := newRouteHarness(t)
	h.env["MAESTRO_TICKET_ACTOR"] = "planner"
	h.ok(nil, "rules", "set", "limit", limit)
	for i := range n {
		tier := "senior"
		if i == 0 {
			tier = "junior"
		}
		h.ok(nil, "create", "--title", "t", "--tier", tier)
	}
	return h
}

// builds are the build runs on each ticket, by launcher.
func builds(t *testing.T, dir string) map[string]string {
	t.Helper()
	list, err := runs.List(dir, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range list {
		if r.Job == "build" {
			if _, dup := out[r.Ticket]; dup {
				t.Fatalf("two builds on %s", r.Ticket)
			}
			out[r.Ticket] = r.Launcher
		}
	}
	return out
}

// A look starts each build itself, as its engineer.
func TestLookStartsWorkerJobs(t *testing.T) {
	sup := fakeSupervisor(t)
	h := newRefillHarness(t, "2", 2)
	code, out, errs := h.run("", "look")
	if code != 0 {
		t.Fatalf("look: %d %q %q", code, out, errs)
	}
	if !regexp.MustCompile(`(?m)^started: R-[a-z2-7]{8} build T-1 as junior-engineer$`).MatchString(out) ||
		!regexp.MustCompile(`(?m)^started: R-[a-z2-7]{8} build T-2 as senior-engineer$`).MatchString(out) || strings.Contains(out, "send ") {
		t.Fatalf("look:\n%s", out)
	}
	if got := builds(t, filepath.Join(h.dir, "runs")); sup.count() != 2 || got["T-1"] != "junior-engineer" || got["T-2"] != "senior-engineer" {
		t.Fatalf("builds %v, %d started", got, sup.count())
	}
}

// A build's end that freed its slot starts the next build itself and
// leaves the planner nothing; the review a board with no PR can't start
// stays for the engineer's pickup.
func TestChainRefillsAFreedSlot(t *testing.T) {
	sup := fakeSupervisor(t)
	h := newRefillHarness(t, "1", 3)
	h.move("T-1", "in_progress", "in_review")
	dir := filepath.Join(h.dir, "runs")
	r := h.endedRun("build", "junior-engineer", "build: done")
	h.inProc("junior-engineer").chain(context.Background(), dir, r)
	got, _ := runs.Get(dir, r.ID, time.Now())
	var rt routed
	json.Unmarshal(got.Routed, &rt)
	b := builds(t, dir)
	if got.Notify != "" || sup.count() != 1 || b["T-2"] != "senior-engineer" || b["T-3"] != "" ||
		!slices.Equal(sends(rt), []string{"send reviewer: review T-1"}) {
		t.Fatalf("refill: notify %q, %d started, builds %v, sends %v", got.Notify, sup.count(), b, sends(rt))
	}
}

// Of 8 refills at once on a board with one free slot, one build starts:
// every look takes the tickets in one order, and a ticket's handout and
// its one live build let only one look start it.
func TestRefillsAtOnceKeepTheLimit(t *testing.T) {
	sup := fakeSupervisor(t)
	h := newRefillHarness(t, "1", 4)
	dir := filepath.Join(h.dir, "runs")
	os.MkdirAll(dir, 0o755)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.inProc("reviewer").refill(context.Background(), dir)
		}()
	}
	wg.Wait()
	b := builds(t, dir)
	if sup.count() != 1 || len(b) != 1 {
		t.Fatalf("%d started, builds %v; want one", sup.count(), b)
	}
}
