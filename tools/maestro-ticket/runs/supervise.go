package runs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// RunIDEnv names the variable every launched command gets, so a scan can
// tell a launched claude, and every session it starts, from one started
// outside launch.
const RunIDEnv = "MAESTRO_RUN_ID"

// killGrace is how long a stopped command's group has between SIGTERM and
// SIGKILL.
const killGrace = 10 * time.Second

// pickPoll is how often a ringing supervisor looks for a pickup.
const pickPoll = 2 * time.Second

// DefaultRings are when, after a run ends, the supervisor rings the
// launcher's webhook while no one has picked the result up.
var DefaultRings = []time.Duration{0, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute}

// Options configure a supervisor.
type Options struct {
	Env   []string        // the command's environment, before RunIDEnv
	Rings []time.Duration // DefaultRings if nil
	// NotifyURL returns the launcher's webhook, or "" for none.
	NotifyURL func(launcher string) (string, error)
	Now       func() time.Time
}

// Supervise runs the command of a starting record to its end within its
// time limit, writes the result, then rings the launcher's webhook until
// someone picks the result up or the rings run out. Stopping ctx stops the
// command and records it as failed.
func Supervise(ctx context.Context, dir, id string, o Options) error {
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC().Round(time.Millisecond) }
	}
	if o.Rings == nil {
		o.Rings = DefaultRings
	}
	lock, err := os.OpenFile(path(dir, id, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lockFile(lock); err != nil {
		return err
	}
	r, err := Load(dir, id)
	if err != nil {
		return err
	}
	if r.State != Starting {
		return fmt.Errorf("run %s is %s, not starting", id, r.State)
	}
	r = run(ctx, dir, r, o)
	if err := Save(dir, r); err != nil {
		return err
	}
	if r.Error != "" && r.PID == 0 {
		return nil // never started: launch reports it
	}
	ring(ctx, dir, r, o)
	return nil
}

// run starts the command, waits for it within its limit and returns the
// record with its result.
func run(ctx context.Context, dir string, r Record, o Options) Record {
	fail := func(err error) Record {
		r.State, r.Error = Failed, err.Error()
		now := o.Now()
		r.EndedAt = &now
		return r
	}
	limit := r.LimitDuration()
	if len(r.Command) == 0 || limit <= 0 {
		return fail(errors.New("record has no command or time limit"))
	}
	in, err := os.Open(path(dir, r.ID, ".in"))
	if err != nil {
		return fail(err)
	}
	defer in.Close()
	out, err := os.Create(path(dir, r.ID, ".out"))
	if err != nil {
		return fail(err)
	}
	defer out.Close()
	errf, err := os.Create(path(dir, r.ID, ".err"))
	if err != nil {
		return fail(err)
	}
	defer errf.Close()

	cmd := exec.Command(r.Command[0], r.Command[1:]...)
	cmd.Dir = r.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errf
	cmd.Env = append(append([]string{}, o.Env...), RunIDEnv+"="+r.ID, "MAESTRO_TICKET_ACTOR="+r.Launcher)
	// Its own process group, so a stop reaches all it started; on Linux it
	// also dies if this supervisor dies. That signal follows the thread
	// that started it, so keep this goroutine on one thread.
	cmd.SysProcAttr = procAttr()
	runtime.LockOSThread()
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	r.State, r.PID = Running, cmd.Process.Pid
	if err := Save(dir, r); err != nil {
		killGroup(r.PID, syscall.SIGKILL)
		cmd.Wait()
		r.PID = 0 // nothing ran: launch reports the error
		return fail(err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	why := ""
	select {
	case <-done:
	case <-timer.C:
		why = TimedOut
	case <-ctx.Done():
		why = "stopped"
	}
	if why != "" {
		killGroup(r.PID, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(killGrace):
			killGroup(r.PID, syscall.SIGKILL)
			<-done
		}
	}
	// Anything the command left in its group goes with it on a stop; on a
	// normal exit its children are its own business.
	now := o.Now()
	r.EndedAt = &now
	code := cmd.ProcessState.ExitCode()
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		code = 128 + int(ws.Signal())
	}
	r.Exit = &code
	outb, _ := ReadTail(path(dir, r.ID, ".out"), OutputWindow)
	r.Reply, r.SessionID, r.IsError = ParseOutput(outb)
	switch {
	case why == TimedOut:
		r.State = TimedOut
		r.Error = fmt.Sprintf("the %s time limit ended it", r.Limit)
	case why != "":
		r.State, r.Error = Failed, "the launcher was stopped, so it stopped the run"
	case code == 0:
		r.State = Done
	default:
		r.State = Failed
	}
	if r.State != Done {
		if errb, _ := ReadTail(path(dir, r.ID, ".err"), 2<<10); len(errb) > 0 {
			r.Error = join(r.Error, tail(string(errb), 2<<10))
		}
	}
	return r
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "\n" + b
}

// ring rings the launcher's webhook at each of o.Rings after the end
// while no one has picked the result up.
func ring(ctx context.Context, dir string, r Record, o Options) {
	if o.NotifyURL == nil {
		return
	}
	url, err := o.NotifyURL(r.Launcher)
	if err != nil {
		r.RingError = err.Error()
		Save(dir, r)
		return
	}
	if url == "" {
		return
	}
	end := *r.EndedAt
	for _, at := range o.Rings {
		// Look for a pickup every few seconds while it waits, so a
		// supervisor whose result was read exits soon after.
		for !Picked(dir, r.ID) {
			wait := time.Until(end.Add(at))
			if wait <= 0 {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(min(wait, pickPoll)):
			}
		}
		if Picked(dir, r.ID) {
			return
		}
		r.Rings++
		r.RingError = ""
		if err := Ring(ctx, url, Notice(r)); err != nil {
			r.RingError = err.Error()
		}
		Save(dir, r)
	}
}
