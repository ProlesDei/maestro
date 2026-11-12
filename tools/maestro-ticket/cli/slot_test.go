//go:build unix

package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
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

// A command that handles the stop signal and exits keeps its exit code,
// and a slow one still stops.
func TestSlotStopsCommandOnCancel(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		script string
		want   int
	}{
		{`trap 'kill $!; exit 0' TERM; sleep 30 & echo up; wait`, 0},
		{`trap 'kill $!; exit 7' TERM; sleep 30 & echo up; wait`, 7},
		{`echo up; exec sleep 30`, 128 + 15},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan int, 1)
		var errb bytes.Buffer
		go func() {
			done <- Run(ctx, []string{"slot", "--", "sh", "-c", tc.script}, Env{
				Stdout: w, Stderr: &errb, Getenv: func(k string) string { return h.env[k] }, Dir: h.dir, DiskFree: halfFree,
			})
			w.Close()
		}()
		buf := make([]byte, 3)
		if _, err := r.Read(buf); err != nil { // the command is up and trapping
			t.Fatal(err)
		}
		cancel()
		select {
		case got := <-done:
			if got != tc.want {
				t.Errorf("%q: exit %d; want %d (%s)", tc.script, got, tc.want, errb.String())
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("%q: slot didn't stop", tc.script)
		}
		r.Close()
	}
}

// A stop reaches what the command started, not just the command, and a
// hangup stops it as SIGTERM does; else the slot frees while the work
// still loads the machine.
func TestSlotStopsTheWholeCommand(t *testing.T) {
	h := newHarness(t)
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		cmd := exec.Command(os.Args[0], "slot", "--", "sh", "-c", "sleep 30 & echo $!; wait")
		cmd.Env = append(os.Environ(), childEnv+"=1", envcheck.MinFreeEnv+"=0", "MAESTRO_TICKET_STORE="+h.env["MAESTRO_TICKET_STORE"])
		cmd.Dir = h.dir
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(out).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		grandchild, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			t.Fatal(err)
		}
		cmd.Process.Signal(sig)
		cmd.Wait()
		if got := cmd.ProcessState.ExitCode(); got != 128+int(syscall.SIGTERM) {
			t.Errorf("%v: exit %d; want %d", sig, got, 128+int(syscall.SIGTERM))
		}
		deadline := time.Now().Add(5 * time.Second)
		for alive(grandchild) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if alive(grandchild) {
			syscall.Kill(grandchild, syscall.SIGKILL)
			t.Errorf("%v: the command's own child %d outlived slot", sig, grandchild)
		}
	}
}

func TestSlotStoppedWhileWaitingIsNoSlot(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.dir, "slots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "checks.0.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var errb bytes.Buffer
	code := Run(ctx, []string{"slot", "--max", "1", "--", "true"}, Env{
		Stdout: &errb, Stderr: &errb, Getenv: func(k string) string { return h.env[k] }, Dir: h.dir, DiskFree: halfFree,
	})
	if code != 4 || !strings.Contains(errb.String(), "stopped while waiting") {
		t.Fatalf("exit %d: %s; want 4, stopped while waiting", code, errb.String())
	}
}

func TestSlotMissingCommandIs127(t *testing.T) {
	h := newHarness(t)
	if code, _, errs := h.run("", "slot", "--", "no-such-command-k3f9"); code != 127 {
		t.Fatalf("exit %d: %s; want 127", code, errs)
	}
}

// Each run gets a temp folder of its own and removes it when it ends, pass
// or fail: what pytest leaves under $TMPDIR/pytest-of-<user> goes with it,
// so 20 checks at once leave the shared temp dir as they found it.
func TestSlotGivesEachRunItsOwnTempFolder(t *testing.T) {
	h := newHarness(t)
	shared := filepath.Join(h.dir, "tmp")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	h.env["TMPDIR"] = shared
	// As pytest does, then fail on odd runs; a read-only folder, as Go's
	// module cache leaves, must not stop the removal.
	script := `d=$TMPDIR/pytest-of-u/pytest-0; mkdir -p $d/ro && touch $d/f $d/ro/g && chmod 555 $d/ro && echo $TMPDIR; exit $(($1 % 2))`
	var wg sync.WaitGroup
	dirs := make([]string, 20)
	for i := range dirs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, out, errs := h.run("", "slot", "--max", "20", "--", "sh", "-c", script, "sh", strconv.Itoa(i))
			if code != i%2 || errs != "" {
				t.Errorf("run %d: exit %d, stderr %q; want %d, none", i, code, errs, i%2)
			}
			dirs[i] = strings.TrimSpace(out)
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, d := range dirs {
		if filepath.Dir(d) != shared || seen[d] {
			t.Errorf("run %d: TMPDIR %q; want a folder of its own in %s", i, d, shared)
		}
		seen[d] = true
	}
	if left, _ := os.ReadDir(shared); len(left) != 0 {
		t.Fatalf("%d entries left in the shared temp dir, such as %s", len(left), left[0].Name())
	}
}

// A run stopped by a signal removes its folder too.
func TestSlotRemovesTempFolderOnSignal(t *testing.T) {
	h := newHarness(t)
	shared := filepath.Join(h.dir, "tmp")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	h.env["TMPDIR"] = shared
	ctx, cancel := context.WithCancel(context.Background())
	r, w, _ := os.Pipe()
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"slot", "--", "sh", "-c", "touch $TMPDIR/f; echo up; sleep 30"}, Env{
			Stdout: w, Stderr: io.Discard, Getenv: func(k string) string { return h.env[k] }, Dir: h.dir, DiskFree: halfFree,
		})
		w.Close()
	}()
	if _, err := r.Read(make([]byte, 3)); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	if left, _ := os.ReadDir(shared); len(left) != 0 {
		t.Fatalf("left %s after a stop", left[0].Name())
	}
}

// Under the margin, slot refuses before the command runs, and says how
// to recover.
func TestSlotRefusesOnLowDisk(t *testing.T) {
	h := newHarness(t)
	h.disk = func(string) (envcheck.Free, bool, error) { return envcheck.Free{Inodes: 4, Space: 60}, true, nil }
	mark := filepath.Join(h.dir, "ran")
	code, out, errs := h.run("", "slot", "--", "touch", mark)
	if code != 4 || !strings.Contains(errs, "refused: low disk") || !strings.Contains(errs, "4.0% of its inodes free") || !strings.Contains(errs, "infra block") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want 4, refused: low disk", code, out, errs)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("the command ran on a full disk")
	}
	if code := slotFails(h, 4, "--", "true"); code != "low_disk" {
		t.Fatalf("code %q; want low_disk", code)
	}
}
