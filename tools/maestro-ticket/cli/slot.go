package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

var slotName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// runSlot runs a command once one of --max slots named --name is free, so
// hundreds of sessions sharing one ticket store don't run a heavy check at
// once and fail it under load. A slot is a file lock in slots/ next to the
// store, which every worktree shares; the kernel frees it when this
// process exits, however it exits, so a killed run never holds one. It
// exits with the command's own code.
func runSlot(ctx context.Context, c *call) error {
	var command []string
	if i := slices.Index(c.args, "--"); i >= 0 {
		c.args, command = c.args[:i], c.args[i+1:]
	}
	name := c.fs.String("name", "checks", "slot group: commands that share a name share its slots")
	max := c.fs.Int("max", 4, "how many commands in the group run at once")
	wait := c.fs.Duration("wait", 0, "give up after this long with no free slot (0: wait until one frees)")
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	if len(command) == 0 {
		return usagef("slot: name the command to run after --")
	}
	if !slotName.MatchString(*name) {
		return usagef("slot: --name %q: use lowercase letters, digits and single hyphens", *name)
	}
	if *max < 1 || *max > 256 {
		return usagef("slot: --max %d: want 1 to 256", *max)
	}
	if *wait < 0 {
		return usagef("slot: --wait %s: want 0 or more", *wait)
	}
	min, err := c.minFree()
	if err != nil {
		return err
	}
	path, _, err := c.storePath()
	if err != nil {
		return err
	}
	// A lock on a network filesystem may not exclude another machine's.
	if err := envcheck.Err(c.envChecks(path)); err != nil {
		return err
	}
	dir := filepath.Join(filepath.Dir(path), "slots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock, err := takeSlot(ctx, dir, *name, *max, *wait, c.env.Stderr)
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("%w: stopped while waiting for a %q slot", ticket.ErrNoSlot, *name)
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	// Checked once the slot is free, since the wait can be long.
	if err := c.headroom(path, min); err != nil {
		return err
	}
	// The command gets a temp folder of its own, removed when it ends, pass
	// or fail, so what a test runner leaves in the shared one (pytest's
	// pytest-of-<user>, one numbered folder a run) never piles up when
	// many sessions run checks at once.
	tmp, err := os.MkdirTemp(c.tempDir(), "maestro-slot-")
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			return fmt.Errorf("%w: slot: %v", ticket.ErrLowDisk, err)
		}
		return fmt.Errorf("%w: slot: make the command's temp folder in %s (set TMPDIR to a writable folder): %v", ticket.ErrInvalid, c.tempDir(), err)
	}
	defer removeTemp(tmp, c.env.Stderr)

	// A hangup would end this process with the command still running and
	// its slot free; stop the command first, as on SIGINT and SIGTERM.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGHUP)
	defer stop()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = c.env.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.env.Stdin, c.env.Stdout, c.env.Stderr
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp) // the last TMPDIR wins
	// The command runs in a process group of its own, so a stop reaches
	// everything it started (make, a test runner and its workers), not just
	// its first process. On SIGINT, SIGTERM or SIGHUP the group gets
	// SIGTERM, then SIGKILL once the command is gone or WaitDelay passes.
	ownGroup(cmd)
	cmd.Cancel = func() error { return signalGroup(cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
	err = cmd.Run()
	if ctx.Err() != nil && cmd.Process != nil {
		signalGroup(cmd.Process.Pid, syscall.SIGKILL)
	}
	// Once the command ran, its own exit is the answer, even when a signal
	// cancelled ctx and Run reports that instead.
	if ps := cmd.ProcessState; ps != nil {
		if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return silentExit{128 + int(ws.Signal())}
		}
		if ps.ExitCode() == 0 {
			return nil
		}
		return silentExit{ps.ExitCode()}
	}
	if errors.Is(err, exec.ErrNotFound) {
		fmt.Fprintf(c.env.Stderr, "maestro-ticket: slot: %v\n", err)
		return silentExit{127} // as a shell exits for a missing command
	}
	return fmt.Errorf("%w: slot: %v", ticket.ErrInvalid, err)
}

// takeSlot locks the first free file of name.0.lock to name.<max-1>.lock
// and returns it open; closing it frees the slot. With all busy, it says so
// once on w and tries again every second or so until ctx ends or wait
// passes.
func takeSlot(ctx context.Context, dir, name string, max int, wait time.Duration, w io.Writer) (*os.File, error) {
	start := time.Now()
	said := false
	for {
		for i := range max {
			f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%s.%d.lock", name, i)), os.O_CREATE|os.O_RDWR, 0o644)
			if err != nil {
				return nil, err
			}
			ok, err := tryLock(f)
			if ok {
				return f, nil
			}
			f.Close()
			if err != nil {
				return nil, err
			}
		}
		if wait > 0 && time.Since(start) >= wait {
			return nil, fmt.Errorf("%w: all %d %q slots stayed busy for %s", ticket.ErrNoSlot, max, name, wait)
		}
		if !said {
			fmt.Fprintf(w, "maestro-ticket: slot: all %d %q slots busy; waiting\n", max, name)
			said = true
		}
		// Jitter, so waiters that start together don't retry together.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500*time.Millisecond + rand.N(time.Second)):
		}
	}
}

// removeTemp removes the command's temp folder. A tree left read-only, such
// as a Go module cache, is made writable first, and a process the command
// left behind may still be writing there, so it tries a few times. A
// failure is reported, not returned: the command's own exit is the answer.
func removeTemp(dir string, w io.Writer) {
	var err error
	for try := range 3 {
		if try > 0 {
			time.Sleep(time.Duration(try) * 200 * time.Millisecond)
			filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
				if err == nil && d.IsDir() {
					os.Chmod(p, 0o700)
				}
				return nil
			})
		}
		if err = os.RemoveAll(dir); err == nil {
			return
		}
	}
	fmt.Fprintf(w, "maestro-ticket: slot: remove the command's temp folder: %v\n", err)
}
