package runs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"syscall"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/streamfilter"
)

// RunIDEnv names the variable every launched command gets, so a scan can
// tell a launched claude, and every session it starts, from one started
// outside launch.
const RunIDEnv = "MAESTRO_RUN_ID"

// KillGrace is how long a stopped command's group has between SIGTERM and
// SIGKILL.
const KillGrace = 10 * time.Second

// killGrace is KillGrace, a var only so tests can shorten it.
var killGrace = KillGrace

// watchEvery is how often a supervisor looks at the wall clock and for a
// stop asked while its command runs. A var only so tests can shorten it.
var watchEvery = 2 * time.Second

// FreezeGap is the least gap between the wall and monotonic clocks that
// counts as the machine frozen: a VM paused by its host stops the
// monotonic clock, and the wall clock jumps on waking.
const FreezeGap = time.Minute

// asked is why a run ends when someone asked stop.
const asked = "asked"

// pickPoll is how often a ringing supervisor looks for an ack.
const pickPoll = 2 * time.Second

// DefaultRings are when, after a run ends, the supervisor rings the
// launcher's webhook while no one has acked the result.
var DefaultRings = []time.Duration{0, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute}

// Options configure a supervisor.
type Options struct {
	Env   []string        // the command's environment, before RunIDEnv
	Rings []time.Duration // DefaultRings if nil
	// Hook returns the launcher's webhook; a zero Hook for none.
	Hook func(launcher string) (Hook, error)
	// OnStart, if set, runs once the command started, before the record
	// says running: it returns the run's launch id in the store.
	OnStart func(Record) int64
	// OnEnd, if set, runs once the result is saved, before the first ring:
	// to log the run's cost, free a ticket it left claimed, and start the
	// fixed next step. It may save the record; the rings read it after.
	OnEnd func(Record)
	// RingAgain, when set, names a time to ring once more while the run
	// isn't acked, such as the end of the usage limit that stopped it, so
	// its bot learns its run can go on; zero for none, 24h out at most.
	RingAgain func(Record) time.Time
	// AfterRing, if set, runs once after the first ring, with its error,
	// or with the error looking up the webhook, or with nil once the
	// supervisor knows no ring comes (no webhook, or the result acked
	// first), so work that must not hold up the ring, such
	// as a stats prune, waits for it.
	AfterRing func(Record, error)
	// Stream, when set, runs the command with stream-json in place of
	// its exact --output-format json, its stdout in <id>.stream, and
	// reads that file once the command is over: .out gets what it would
	// have held with json, and the record's Stream the counts.
	Stream *streamfilter.Names
	Now    func() time.Time
}

// Supervise runs the command of a starting record to its end within its
// time limit, writes the result, then rings the launcher's webhook until
// someone acks the result or the rings run out. Stopping ctx stops the
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
		DropStream(dir, id)
		return err
	}
	if r.Error != "" && r.PID == 0 {
		DropStream(dir, id)
		return nil // never started: launch reports it
	}
	if o.OnEnd != nil {
		o.OnEnd(r)
		// It may have handed the result on: ring as it left the record.
		if got, err := Load(dir, r.ID); err == nil {
			r = got
		}
	}
	DropStream(dir, id)
	// Stopped, as at a shutdown: leave the ring to the first reader after,
	// which Adopt lets in once this exits.
	if ctx.Err() == nil {
		ring(ctx, dir, r, o)
	}
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

	// The record keeps the command as given; only what runs changes.
	argv, stdout := r.Command, out
	var stream *os.File
	if a, ok := StreamArgv(r.Command); ok && o.Stream != nil {
		if f, err := os.OpenFile(path(dir, r.ID, streamExt), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600); err == nil {
			argv, stdout, stream = a, f, f
			defer f.Close()
		}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = r.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, stdout, errf
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
	if o.OnStart != nil {
		r.LaunchID = o.OnStart(r)
	}
	if err := Save(dir, r); err != nil {
		killGroup(r.PID, syscall.SIGKILL)
		cmd.Wait()
		r.PID = 0 // nothing ran: launch reports the error
		return fail(err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// The limit runs on the wall clock, from the record's start, as leases
	// and rings do: a machine frozen past a run's limit ends it at once on
	// waking, since everyone waiting on the run waited through the freeze
	// too. A Go timer runs on the monotonic clock, which stops while the
	// machine is frozen, so the supervisor looks at the wall clock each
	// watchEvery instead. The limit leaves out time the command spent
	// waiting for a slot, up to what Credit allows, so the deadline moves
	// on each time it comes.
	start := r.StartedAt
	if start.IsZero() {
		start = o.Now()
	}
	wall, mono := o.Now(), time.Now()
	why, stopper := "", ""
	var credit time.Duration
	var read time.Time // when Credit was last read, on the monotonic clock
	capped, freezeOnly := false, false
	timer := time.NewTimer(min(watchEvery, limit))
	defer timer.Stop()
wait:
	for {
		select {
		case <-done:
			break wait
		case <-ctx.Done():
			why = "stopped"
			break wait
		case <-timer.C:
		}
		if by, ok := StopAsked(dir, r.ID); ok {
			why, stopper = asked, by
			break wait
		}
		// Credit reads wait marks on the wall clock. The deadline goes by
		// the later of the wall clock and the monotonic one: a freeze moves
		// only the first, and a clock stepped back only the first too, so
		// neither stretches the limit.
		now := o.Now()
		at := later(now, wall.Add(time.Since(mono)))
		due := func() bool { return !at.Before(start.Add(limit + credit)) }
		// While a wait goes on, the deadline moves as fast as the clock:
		// read the waits no sooner than liveWait apart, not in a spin.
		if due() && (read.IsZero() || time.Since(read) >= liveWait) {
			credit, capped = Credit(dir, r.ID, start, now, limit)
			read = time.Now()
			if due() {
				// Read once more before it ends the run: a read that
				// raced a wait's rename can miss that wait.
				credit, capped = Credit(dir, r.ID, start, now, limit)
			}
			if due() {
				why = TimedOut
				// The freeze ended it if, by the monotonic clock alone, it
				// had time left, and the freeze took half its time or more:
				// a host that sleeps a few minutes each run doesn't hide a
				// run that hangs.
				fz := now.Sub(wall) - time.Since(mono)
				freezeOnly = fz >= FreezeGap && 2*fz >= now.Sub(start) &&
					wall.Add(time.Since(mono)).Before(start.Add(limit+credit))
				break wait
			}
		}
		next := watchEvery
		if left := start.Add(limit + credit).Sub(at); left > 0 {
			next = min(next, left)
		}
		timer.Reset(next)
	}
	// Frozen is the wall time the monotonic clock never saw.
	frozen := o.Now().Sub(wall) - time.Since(mono)
	// Read once more before the marks go: the run's row keeps it.
	wait, waitCapped := Credit(dir, r.ID, start, o.Now(), limit)
	removeWaits(dir, r.ID)
	end := &EndFacts{WaitMs: wait.Milliseconds(), WaitCapped: waitCapped, Stop: StopNone}
	if why != "" {
		// Anything the command left in its group goes with it on a stop,
		// even when the command exits on SIGTERM and a child ignores it;
		// on a normal exit its children are its own business.
		// What left the group goes too, found by the run's id in its
		// environment: Claude Code starts each tool command in a session
		// of its own, and slot puts its command in a group of its own, so
		// a hung test or server would outlive the limit, holding its port
		// or its slot's work. It gets the same SIGTERM and grace, so a git
		// stopped mid-write removes its lock.
		killGroup(r.PID, syscall.SIGTERM)
		end.Stop, end.Escaped = StopTerm, termRun(r.ID, r.PID)
		grace := time.After(killGrace)
		select {
		case <-done:
		case <-grace:
			killGroup(r.PID, syscall.SIGKILL)
			<-done
			grace, end.Stop = nil, StopKill
		}
		for grace != nil && (groupAlive(r.PID) || runAlive(r.ID)) {
			select {
			case <-grace:
				killGroup(r.PID, syscall.SIGKILL)
				grace, end.Stop = nil, StopKill
			case <-time.After(100 * time.Millisecond):
			}
		}
		KillRun(r.ID)
	}
	now := o.Now()
	r.EndedAt, r.End = &now, end
	code := cmd.ProcessState.ExitCode()
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		code = 128 + int(ws.Signal())
	}
	r.Exit = &code
	if stream != nil {
		r.Stream = scanStream(dir, r.ID, out, *o.Stream)
	}
	outb, _ := ReadTail(path(dir, r.ID, ".out"), OutputWindow)
	r.Reply, r.SessionID, r.IsError = ParseOutput(outb)
	switch {
	case why == TimedOut:
		r.State = TimedOut
		r.Error = fmt.Sprintf("the %s time limit ended it", r.Limit)
		if credit > 0 {
			r.Error += fmt.Sprintf(", which left out %s waiting for a slot", credit.Round(time.Second))
			if capped {
				r.Error += " (the most it leaves out)"
			}
		}
		if frozen >= FreezeGap {
			r.Error += fmt.Sprintf("; the machine was frozen %s of it", frozen.Round(time.Second))
			r.Frozen = freezeOnly
		}
	case why == asked:
		r.State, r.Error = Failed, "stopped by "+stopper
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

// streamExt ends the file a streamed run's stdout goes to while it runs.
const streamExt = ".stream"

// StreamArgv is cmd with its exact --output-format json pair made
// --output-format stream-json --verbose, and whether it had one. Any
// other command, such as one already on stream-json, has none.
func StreamArgv(cmd []string) ([]string, bool) {
	for i := 0; i+1 < len(cmd); i++ {
		if cmd[i] == "--output-format" && cmd[i+1] == "json" {
			out := append(slices.Clone(cmd[:i+1]), "stream-json", "--verbose")
			return append(out, cmd[i+2:]...), true
		}
	}
	return nil, false
}

// scanStream reads a streamed run's stdout, once its command is over,
// and writes to .out what json would have: the command's own text, then
// the result line. A grandchild that still holds the file
// may write on; what it writes after this is never read.
func scanStream(dir, id string, out *os.File, n streamfilter.Names) *streamfilter.Summary {
	f, err := os.Open(path(dir, id, streamExt))
	if err != nil {
		return nil
	}
	defer f.Close()
	s, kept := streamfilter.Scan(f, n)
	out.Write(kept)
	return &s
}

// RecoverStream does for a run whose supervisor died what that
// supervisor would have, so the result Claude printed still reaches the
// run's end: when its stream file is there and its .out is empty, it
// writes .out from the stream and returns the counts; otherwise nil. Its
// caller has adopted the run, and drops the stream after.
func RecoverStream(dir, id string, n streamfilter.Names) *streamfilter.Summary {
	out, err := os.OpenFile(path(dir, id, ".out"), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return nil
	}
	defer out.Close()
	if fi, err := out.Stat(); err != nil || fi.Size() > 0 {
		return nil
	}
	return scanStream(dir, id, out, n)
}

// DropStream removes a run's stream file: its supervisor once the run's
// end is recorded, and the reader that writes a died run's end.
func DropStream(dir, id string) { os.Remove(path(dir, id, streamExt)) }

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "\n" + b
}

// ring rings the launcher's webhook at each of o.Rings after the end
// while no one has acked the result.
func ring(ctx context.Context, dir string, r Record, o Options) {
	first := o.AfterRing
	after := func(err error) {
		if first != nil && ctx.Err() == nil {
			first(r, err)
		}
		first = nil
	}
	defer after(nil) // no ring came
	if o.Hook == nil {
		return
	}
	h, err := o.Hook(r.Owner())
	if err != nil {
		r.RingError = err.Error()
		Save(dir, r)
		after(err)
		return
	}
	if h.URL == "" {
		return
	}
	end := *r.EndedAt
	rings := o.Rings
	if o.RingAgain != nil && !o.RingAgain(r).IsZero() && len(rings) > 1 {
		// It waits on a time: one ring now and one then, not a ring each
		// few minutes that its bot can only answer with a refused retry.
		rings = rings[:1]
	}
	for _, at := range rings {
		// Look for an ack every few seconds while it waits, so a
		// supervisor whose result was acked exits soon after.
		for !Acked(dir, r.ID) {
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
		if Acked(dir, r.ID) {
			return
		}
		err := Ring(ctx, h, Notice(r))
		if ctx.Err() != nil {
			return // a ring cut short isn't one: a reader rings after
		}
		r.Rings++
		r.Rang, r.RingError = r.Rang || err == nil, ""
		if err != nil {
			r.RingError = err.Error()
		}
		Save(dir, r)
		after(err)
	}
	if o.RingAgain == nil {
		return
	}
	at := o.RingAgain(r)
	if at.IsZero() || time.Until(at) > 24*time.Hour {
		return
	}
	for !Acked(dir, r.ID) && time.Now().Before(at) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(min(time.Until(at), pickPoll)):
		}
	}
	if Acked(dir, r.ID) {
		return
	}
	if err := Ring(ctx, h, Notice(r)); err == nil && ctx.Err() == nil {
		r.Rings++
		Save(dir, r)
	}
}
