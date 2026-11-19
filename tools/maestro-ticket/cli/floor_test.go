package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
)

// Past a floor rules set floor set, look hands out and starts nothing and
// says why in its paused line and its counts, and job refuses with
// paused; under it, or with no floor set, both go on as before.
func TestFloorsPauseStarts(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "doctor")
	inodes := 12.0
	h.disk = func(string) (envcheck.Free, bool, error) {
		return envcheck.Free{Inodes: inodes, Space: 50}, true, nil
	}
	load := 2.0
	h.load = func() (float64, bool) { return load, true }
	h.ok(nil, "create", "--title", "a")
	look := func() string {
		t.Helper()
		os.RemoveAll(filepath.Join(h.dir, "handouts")) // each look may hand T-1 out afresh
		code, out, errs := h.run("", "look", "--limit", "10", "--actor", "planner")
		if code != 0 {
			t.Fatalf("look: %d %s %s", code, out, errs)
		}
		return out
	}
	const sent = "send senior-engineer: build T-1"
	// No floor: 12% free stops nothing.
	if out := look(); !strings.Contains(out, sent) || strings.Contains(out, "paused") {
		t.Fatalf("no floor: %s", out)
	}
	h.ok(nil, "rules", "set", "floor", "inodes", "15%", "--actor", "paul", "--why", "inodes at 12%")
	if l := h.logLines(); len(l) == 0 || !strings.HasSuffix(l[len(l)-1], " paul floor_inodes none -> 15% why: inodes at 12%") {
		t.Fatalf("rules.log keeps a floor's --why: %q", l)
	}
	out := look()
	if !strings.HasPrefix(out, "paused: inodes: ") || !strings.Contains(out, "has 12.0% of its inodes free, under the 15% floor: look hands out and starts nothing") ||
		strings.Contains(out, sent) || !strings.Contains(out, "; paused: inodes\n") {
		t.Fatalf("under the inode floor: %s", out)
	}
	if msg := h.fails(4, "job", "plan", "--actor", "planner"); msg != "paused" {
		t.Fatalf("job under the inode floor: %q", msg)
	}
	inodes = 20
	if out := look(); !strings.Contains(out, sent) || strings.Contains(out, "paused") {
		t.Fatalf("over the inode floor: %s", out)
	}
	h.ok(nil, "rules", "set", "floor", "load", "24", "--actor", "paul")
	load = 30
	if out := look(); !strings.HasPrefix(out, "paused: load: the 1-minute load average is 30.0, over the 24 floor") || strings.Contains(out, sent) || !strings.Contains(out, "; paused: load\n") {
		t.Fatalf("over the load floor: %s", out)
	}
	if msg := h.fails(4, "job", "plan", "--actor", "planner"); msg != "paused" {
		t.Fatalf("job over the load floor: %q", msg)
	}
	if _, out, _ := h.run("", "rules", "show"); !strings.Contains(out, "floor: inodes 15%, load 24\n") {
		t.Fatalf("rules show: %s", out)
	}
	// 0 drops a floor.
	h.ok(nil, "rules", "set", "floor", "load", "0", "--actor", "paul")
	h.ok(nil, "rules", "set", "floor", "inodes", "0", "--actor", "paul")
	if out := look(); !strings.Contains(out, sent) || strings.Contains(out, "paused") {
		t.Fatalf("floors dropped: %s", out)
	}
	if _, out, _ := h.run("", "rules", "show"); !strings.Contains(out, "floor: none\n") {
		t.Fatalf("rules show, floors dropped: %s", out)
	}
	// Bad values, and a bot it isn't for, change nothing.
	for _, args := range [][]string{
		{"rules", "set", "floor", "inodes", "150", "--actor", "paul"},
		{"rules", "set", "floor", "load", "-1", "--actor", "paul"},
		{"rules", "set", "floor", "disk", "5", "--actor", "paul"},
		{"rules", "set", "floor", "inodes", "15%", "--actor", "qa-lead"},
	} {
		if code, _, _ := h.run("", args...); code == 0 {
			t.Fatalf("%v: exit 0", args)
		}
	}
	if b := h.board(); b.FloorInodes != nil || b.FloorLoad != nil {
		t.Fatalf("board after refused sets: %+v", b)
	}
}

// A floor stops only new starts: a session going already is still held to
// its verdict by the Stop hook, as it would be under no floor.
func TestFloorLeavesTheStopHook(t *testing.T) {
	s := newStopScene(t, "build", modelOpus)
	inProgress(s)
	s.h.disk = func(string) (envcheck.Free, bool, error) { return envcheck.Free{Inodes: 12, Space: 50}, true, nil }
	s.h.ok(nil, "rules", "set", "floor", "inodes", "15%", "--actor", "paul")
	if !s.stop(s.r.ID, stopIn("S1", "done", false)) {
		t.Fatal("the Stop hook let a session end with no note past a floor")
	}
}
