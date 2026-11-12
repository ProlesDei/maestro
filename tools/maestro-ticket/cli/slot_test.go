//go:build unix

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// slotFails is h.fails with --json before the --: after it, the flag is
// the command's. h.fails adds a second --json at the end, so every args
// here must hold a -- or no command at all.
func slotFails(h *harness, code int, args ...string) string {
	h.t.Helper()
	return h.fails(code, append([]string{"slot", "--json"}, args...)...)
}

func TestSlotRunsCommandAndKeepsItsExitCode(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("", "slot", "--", "sh", "-c", "echo hi; exit 3")
	if code != 3 || out != "hi\n" {
		t.Fatalf("exit %d, out %q; want 3, %q", code, out, "hi\n")
	}
	if code, _, _ := h.run("", "slot", "--", "true"); code != 0 {
		t.Fatalf("true: exit %d", code)
	}
}

func TestSlotRefusesBadInput(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{},
		{"--max", "0", "--", "true"},
		{"--name", "Bad/Name", "--", "true"},
		{"extra", "--", "true"},
	} {
		if code := slotFails(h, 2, args...); code != "usage" {
			t.Errorf("%v: code %q; want usage", args, code)
		}
	}
	if code, _, errs := h.run("", "slot", "--max", "1", "--"); code != 2 || !strings.Contains(errs, "after --") {
		t.Errorf("nothing after --: exit %d: %s", code, errs)
	}
}

// The command's own flags after -- are its, not slot's.
func TestSlotPassesFlagsAfterDashDash(t *testing.T) {
	h := newHarness(t)
	code, out, errs := h.run("", "slot", "--max", "1", "--", "sh", "-c", "echo $0", "--json")
	if code != 0 || out != "--json\n" {
		t.Fatalf("exit %d, out %q, err %q", code, out, errs)
	}
}

func TestSlotWaitsForAFreeSlot(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.dir, "slots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "checks.0.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if code := slotFails(h, 4, "--max", "1", "--wait", "200ms", "--", "true"); code != "no_slot" {
		t.Fatalf("code %q; want no_slot", code)
	}
	// A second slot is free.
	if code, _, errs := h.run("", "slot", "--max", "2", "--wait", "200ms", "--", "true"); code != 0 {
		t.Fatalf("max 2: exit %d: %s", code, errs)
	}
	f.Close()
	if code, _, errs := h.run("", "slot", "--max", "1", "--wait", "200ms", "--", "true"); code != 0 {
		t.Fatalf("after release: exit %d: %s", code, errs)
	}
}

// Many sessions at once: never more than --max run together.
func TestSlotCapsConcurrentRuns(t *testing.T) {
	h := newHarness(t)
	running := t.TempDir()
	// Each run marks itself running, counts the marks, holds, then unmarks.
	script := fmt.Sprintf(`touch %[1]s/$$; ls %[1]s | wc -l; sleep 0.2; rm %[1]s/$$`, running)
	const max, runs = 2, 8
	var wg sync.WaitGroup
	peaks := make([]int, runs)
	for i := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, out, errs := h.run("", "slot", "--max", strconv.Itoa(max), "--", "sh", "-c", script)
			if code != 0 {
				t.Errorf("run %d: exit %d: %s", i, code, errs)
				return
			}
			peaks[i], _ = strconv.Atoi(strings.TrimSpace(out))
		}()
	}
	wg.Wait()
	for i, p := range peaks {
		if p < 1 || p > max {
			t.Errorf("run %d saw %d running; want 1 to %d", i, p, max)
		}
	}
}
