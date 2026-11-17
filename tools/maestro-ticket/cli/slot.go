package cli

import (
	"context"
	"errors"
	"flag"
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
	"strings"
	"syscall"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

var slotName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// slotKillGrace is how long a stopped slot command has between SIGTERM
// and SIGKILL: under a supervisor's grace, so the group dies first.
const slotKillGrace = runs.KillGrace / 2

// sharedHeldEvery is how often a session waiting for a slot reads a
// shared store's pause and halt.
const sharedHeldEvery = 15 * time.Second

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
	status := c.fs.Bool("status", false, "print who holds and who waits for each group's slots, and run nothing")
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	if *status {
		if len(command) > 0 {
			return usagef("slot: --status runs nothing: drop the command after --")
		}
		only := ""
		c.fs.Visit(func(f *flag.Flag) {
			if f.Name == "name" {
				only = *name
			}
		})
		return c.slotStatus(only)
	}
	if len(command) == 0 {
		return usagef("slot: name the command to run after --")
	}
	if err := checkSlotFlags("slot", *name, *max, *wait); err != nil {
		return err
	}
	return c.inSlot(ctx, *name, *max, *wait, command)
}

func checkSlotFlags(cmd, name string, max int, wait time.Duration) error {
	if !slotName.MatchString(name) {
		return usagef("%s: --name %q: use lowercase letters, digits and single hyphens", cmd, name)
	}
	if max < 1 || max > 256 {
		return usagef("%s: --max %d: want 1 to 256", cmd, max)
	}
	if wait < 0 {
		return usagef("%s: --wait %s: want 0 or more", cmd, wait)
	}
	return nil
}

// inSlot runs command once one of max slots named name is free, as slot
// does, and returns its exit as slot's.
func (c *call) inSlot(ctx context.Context, name string, max int, wait time.Duration, command []string) error {
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
	// A run that launch started leaves its slot wait out of its time
	// limit: the mark tells its supervisor how long it waited.
	var mark *runs.Wait
	tick := func() {
		now := time.Now()
		if mark == nil {
			// Best effort: a lost mark costs the run its credit, not its slot.
			mark, _ = runs.StartWait(filepath.Join(filepath.Dir(path), "runs"), c.env.Getenv(runs.RunIDEnv), now, filepath.Base(command[0]) == "claude")
		}
		mark.Touch(now)
	}
	// A Claude session waiting in line holds while the line is stopped or
	// a usage limit holds: started then, it only stops or fails again.
	sdir := filepath.Dir(path)
	co, err := c.coord()
	if err != nil {
		return err
	}
	// On a shared store each read is a round trip, and a line of sessions
	// held for hours would ask every second: read at most every
	// sharedHeldEvery.
	var heldAt time.Time
	var wasHeld bool
	held := func() bool {
		if filepath.Base(command[0]) != "claude" {
			return false
		}
		if co.host != "" && !heldAt.IsZero() && time.Since(heldAt) < sharedHeldEvery {
			return wasHeld
		}
		_, paused := co.readPause(time.Now())
		heldAt, wasHeld = time.Now(), paused || co.s.stopped()
		return wasHeld
	}
	use := c.slotUser(sdir, command)
	lock, err := takeSlot(ctx, dir, name, max, wait, c.env.Stderr, tick, held, use)
	mark.End(time.Now())
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("%w: stopped while waiting for a %q slot", ticket.ErrNoSlot, name)
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
	// WaitDelay stays under a supervisor's grace, so a run's stop kills
	// the group before it kills this process and frees the slot.
	ownGroup(cmd)
	cmd.Cancel = func() error { return signalGroup(cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = slotKillGrace
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
// once on w, joins the line, and tries again every second or so until ctx
// ends or wait passes, calling tick before each pause. Waiters take slots
// in the order they came: one tries only while fewer than max live
// waiters are ahead of it, so slots that free together fill together, and
// no one is passed by a waiter more than max-1 places behind it.
func takeSlot(ctx context.Context, dir, name string, max int, wait time.Duration, w io.Writer, tick func(), held func() bool, use slotUse) (*os.File, error) {
	start := time.Now()
	var place *waiter                // our place in line, once we wait
	defer func() { place.leave() }() // place as it is at the end, not now
	for {
		ahead, err := waitsAhead(dir, name, place, max)
		if err != nil {
			return nil, err
		}
		if ahead < max && (held == nil || !held()) {
			if f, err := freeSlot(dir, name, max); f != nil || err != nil {
				if f != nil {
					use.Since = time.Now().UTC()
					use.write(f)
				}
				return f, err
			}
		}
		if place == nil {
			use.Since = time.Now().UTC()
			if place, err = joinLine(dir, name, use); err != nil {
				return nil, err
			}
			fmt.Fprintf(w, "maestro-ticket: slot: all %d %q slots busy; waiting\n", max, name)
		}
		if wait > 0 && time.Since(start) >= wait {
			return nil, fmt.Errorf("%w: all %d %q slots stayed busy for %s", ticket.ErrNoSlot, max, name, wait)
		}
		tick()
		// Jitter, so waiters that start together don't retry together.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500*time.Millisecond + rand.N(time.Second)):
		}
	}
}

// freeSlot locks the first free slot of name and returns it open, or nil
// with all busy.
func freeSlot(dir, name string, max int) (*os.File, error) {
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
	return nil, nil
}

// The line for name's slots is one file per waiter in the slots folder,
// name.q.<start in unix nanoseconds, 20 digits>-<pid>, which the waiter
// holds locked while it waits. The names sort in the order the waiters
// came, and a file anyone can lock belongs to a waiter that died, so
// whoever finds one removes it.

// waiter is one place in a line: its file, held open and locked, and its
// name in the line.
type waiter struct {
	f    *os.File
	name string
}

// joinLine adds a waiter to the end of name's line. It locks the file
// under a name outside the line, then renames it in, which keeps the
// lock, so no one finds the new waiter unlocked and takes it for dead.
func joinLine(dir, name string, use slotUse) (*waiter, error) {
	f, err := os.CreateTemp(dir, name+".t.*")
	if err != nil {
		return nil, err
	}
	// Readable, as the slot files are, so a waiter of another user can
	// test its lock.
	err = f.Chmod(0o644)
	ok := false
	if err == nil {
		ok, err = tryLock(f)
	}
	if ok {
		use.write(f)
	}
	if !ok && err == nil {
		err = fmt.Errorf("slot: lock a new waiter's file %s: held elsewhere", f.Name())
	}
	if err == nil {
		place := fmt.Sprintf("%s.q.%020d-%d", name, time.Now().UnixNano(), os.Getpid())
		if err = os.Rename(f.Name(), filepath.Join(dir, place)); err == nil {
			return &waiter{f, place}, nil
		}
	}
	os.Remove(f.Name())
	f.Close()
	return nil, err
}

// leave takes the waiter out of the line: removes its file, then lets go
// of the lock.
func (p *waiter) leave() {
	if p != nil {
		os.Remove(filepath.Join(filepath.Dir(p.f.Name()), p.name))
		p.f.Close()
	}
}

// waitsAhead counts the live waiters ahead of place in name's line, or
// in it at all when place is nil, up to max. It removes the files of dead
// waiters it passes, and the files of waiters that died while joining.
func waitsAhead(dir, name string, place *waiter, max int) (int, error) {
	ents, err := os.ReadDir(dir) // sorted by name, so oldest first
	if err != nil {
		return 0, err
	}
	line, joining := name+".q.", name+".t."
	n := 0
	for _, e := range ents {
		base := e.Name()
		if strings.HasPrefix(base, joining) {
			// A join takes a moment; one a minute old died halfway.
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Minute {
				dropDead(filepath.Join(dir, base))
			}
			continue
		}
		if !strings.HasPrefix(base, line) {
			continue
		}
		if place != nil && base >= place.name {
			break
		}
		live, err := dropDead(filepath.Join(dir, base))
		if err != nil {
			return 0, err
		}
		if live {
			if n++; n >= max {
				break
			}
		}
	}
	return n, nil
}

// dropDead removes a waiter's file if no one holds its lock, and reports
// whether someone does.
func dropDead(file string) (live bool, err error) {
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil // it left since ReadDir
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	ok, err := tryLock(f)
	if ok {
		os.Remove(file)
	}
	return !ok && err == nil, err
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
