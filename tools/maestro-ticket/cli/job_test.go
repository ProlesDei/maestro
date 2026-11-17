//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// seen is what the stand-in claude got for a run.
type seen struct{ args, stdin, env []string }

func (s seen) flag(name string) string { return flagValue(s.args, name) }

func TestResumeCommand(t *testing.T) {
	cmd := []string{"env", "A=1", "claude", "-p", "--session-id", "S1", "--model", "sonnet", "--effort=high", "--output-format", "json"}
	got := resumeCommand(cmd, "S1", false)
	want := []string{"env", "A=1", "claude", "-p", "--resume", "S1", "--model", "sonnet", "--effort=high", "--output-format", "json"}
	if !slices.Equal(got, want) {
		t.Fatalf("resume: %q", got)
	}
	got = resumeCommand(got, "S1", true)
	want = []string{"env", "A=1", "claude", "-p", "--resume", "S1", "--model", "opus", "--effort", "high", "--output-format", "json"}
	if !slices.Equal(got, want) {
		t.Fatalf("resume --opus: %q", got)
	}
	if got := reviewLimit(0); got != 45*time.Minute {
		t.Fatal(got)
	}
	if got := reviewLimit(399); got != time.Hour {
		t.Fatal(got)
	}
	if got := reviewLimit(5000); got != 2*time.Hour {
		t.Fatal(got)
	}
}

func TestRouteSecondDeathBySession(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	dir := filepath.Join(h.dir, "runs")
	os.MkdirAll(dir, 0o755)
	// R2 resumed the session after a death; R3 resumed R2 with an answer.
	r2, _ := runs.Create(dir, runs.Record{Launcher: "senior-engineer", Limit: "2h", Command: []string{"claude", "-p", "--resume", "S9"}}, nil)
	r2.State, r2.AfterDeath = runs.Done, true
	runs.Save(dir, r2)
	rt := h.route(runs.Record{ID: "R-zzzzzzzz", Job: "build", Ticket: "T-1", State: runs.Died, Command: []string{"claude", "-p", "--resume", "S9"}})
	if !slices.Equal(sends(rt), []string{"send planner: died twice T-1"}) || len(rt.Do) != 0 {
		t.Fatalf("a second death in one session: %+v", rt)
	}
}
