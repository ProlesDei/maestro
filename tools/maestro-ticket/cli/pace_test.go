//go:build unix

package cli

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func TestConcurrentPacedAdmission(t *testing.T) {
	h := newRouteHarness(t)
	root := h.dir
	path := filepath.Join(root, "profiles.json")
	if err := writeWholeMode(path, []byte(`{"test":{"harness":"codex","jobs":{"review":{"model":"astra","effort":"medium","estimate_percent":1}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := saveObservation(root, "test", map[string]paceWindow{"weekly": {Used: 97, Reset: now.Add(time.Hour), Read: now, Source: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	c := h.inProc("reviewer")
	loc, err := c.location()
	if err != nil {
		t.Fatal(err)
	}
	loc.boardID = "scratch"
	c.loc.v = loc
	if _, _, err = c.storePath(); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 8)
	for range 8 {
		cc := *c
		go func() {
			_, err := cc.createPacedRun(filepath.Join(root, "runs"), runs.Record{Job: "review", Launcher: "reviewer", StartedAt: now, Limit: "1h"}, nil)
			errs <- err
		}()
	}
	started := 0
	for range 8 {
		err := <-errs
		if err == nil {
			started++
		} else if !errors.Is(err, ticket.ErrPaused) {
			t.Fatal(err)
		}
	}
	if started != 3 {
		t.Fatalf("97%% used and eight simultaneous one-point jobs: started %d, want 3", started)
	}
}

func TestRuntimeAdmissionFollowsUsage(t *testing.T) {
	h := newRouteHarness(t)
	c := h.inProc("reviewer")
	loc, err := c.location()
	if err != nil {
		t.Fatal(err)
	}
	loc.boardID = "scratch"
	c.loc.v = loc
	root := h.dir
	profiles := `{"max":{"harness":"claude","reserve_percent":2,"jobs":{"review":{"model":"opus","effort":"medium","estimate_percent":1}}},"pro":{"harness":"codex","jobs":{"review":{"model":"astra","effort":"medium","estimate_percent":1}}}}`
	if err := writeWholeMode(filepath.Join(root, "profiles.json"), []byte(profiles), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sample := func(alias string, used float64, at time.Time) {
		t.Helper()
		if err := saveObservation(root, alias, map[string]paceWindow{"weekly": {Used: used, Reset: now.Add(7 * 24 * time.Hour), Read: at, Source: "trial"}}); err != nil {
			t.Fatal(err)
		}
	}
	sample("max", 5, now.Add(-time.Hour))
	sample("pro", 20, now.Add(-time.Hour))
	job := func() (runs.Record, error) {
		return c.createPacedRun(filepath.Join(root, "runs"), runs.Record{Job: "review", Launcher: "reviewer", StartedAt: now, Limit: "1h"}, nil)
	}
	for i := 0; i < 10; i++ {
		r, err := job()
		if err != nil || r.Profile != "max" {
			t.Fatalf("admission %d: %s %v", i, r.Profile, err)
		}
	}
	// The same ten active reservations exist at both readings; only burn changes.
	sample("max", 95, now)
	r, err := job()
	if err != nil || r.Profile != "pro" {
		t.Fatalf("alternate: %s %v", r.Profile, err)
	}
	sample("pro", 100, now)
	if _, err := job(); !errors.Is(err, ticket.ErrPaused) {
		t.Fatalf("overspent: %v", err)
	}
}
