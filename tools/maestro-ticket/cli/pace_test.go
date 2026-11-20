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
