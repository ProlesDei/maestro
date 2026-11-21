//go:build unix

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/sqlitestore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// fakeSup stands in for a run's supervisor, in this process: it holds
// the run's lock, as a live supervisor does, and marks it running, so a
// start returns at once. With noStart set, the run's command never ran.
type fakeSup struct {
	mu      sync.Mutex
	noStart bool
	started []string
	dirs    []string // each supervisor's folder
	locks   []*os.File
}

// fakeSupervisor puts a fakeSup in startSupervisor's place till the test
// ends. A test that calls it must not run in parallel.
func fakeSupervisor(t *testing.T) *fakeSup {
	f := &fakeSup{}
	old := startSupervisor
	startSupervisor = f.start
	t.Cleanup(func() {
		startSupervisor = old
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, l := range f.locks {
			l.Close()
		}
	})
	return f
}

// start reads the run from its command: supervise <id> --store <path>.
func (f *fakeSup) start(sup *exec.Cmd) error {
	a := sup.Args
	if len(a) < 5 || a[1] != "supervise" || a[3] != "--store" {
		return errors.New("fake supervisor: unexpected command " + strings.Join(a, " "))
	}
	id, dir := a[2], filepath.Join(filepath.Dir(a[4]), "runs")
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := runs.Load(dir, id)
	if err != nil {
		return err
	}
	now := time.Now()
	if f.noStart {
		r.State, r.EndedAt, r.Error = runs.Failed, &now, "start the command: no such file"
		return runs.Save(dir, r)
	}
	lock, err := os.OpenFile(filepath.Join(dir, id+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return err
	}
	f.locks = append(f.locks, lock)
	f.started, f.dirs = append(f.started, id), append(f.dirs, sup.Dir)
	r.State, r.PID = runs.Running, os.Getpid()
	return runs.Save(dir, r)
}

func (f *fakeSup) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

// inProc is a call as actor would make it on h's store, in this process,
// as a supervisor's chain makes them.
func (h *routeHarness) inProc(actor string) *call {
	return &call{actor: actor, env: Env{Getenv: func(k string) string { return h.env[k] }, Dir: h.dir, Stdout: io.Discard, Stderr: io.Discard}}
}

// endedPlan saves an ended goal plan of planner's, with its prompt, in a
// chain three hops long.
func (h *routeHarness) endedPlan(state string) runs.Record {
	h.t.Helper()
	dir := filepath.Join(h.dir, "runs")
	r, err := runs.Create(dir, runs.Record{Launcher: "planner", Limit: "1m", Job: "plan",
		Command: []string{"sh", "-c", "true", "--session-id", "S1"}}, []byte("/maestro-planner plan goal x: y\n"))
	if err != nil {
		h.t.Fatal(err)
	}
	now := time.Now()
	r.State, r.PID, r.StartedAt, r.EndedAt, r.IsError = state, 99, now.Add(-time.Minute), &now, state == runs.Failed
	r.ChainOf, r.Hops = "R-cccccccc", 3
	if err := runs.Save(dir, r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

// endAs ends a run the fake supervisor started, as its command would.
func (h *routeHarness) endAs(id, state string) runs.Record {
	h.t.Helper()
	dir := filepath.Join(h.dir, "runs")
	r, err := runs.Load(dir, id)
	if err != nil {
		h.t.Fatal(err)
	}
	now := time.Now()
	r.State, r.EndedAt = state, &now
	if err := runs.Save(dir, r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

// againsOf are the runs that went again as r.
func againsOf(t *testing.T, dir, id string) []runs.Record {
	t.Helper()
	list, err := runs.List(dir, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	var out []runs.Record
	for _, r := range list {
		if r.AgainOf == id {
			out = append(out, r)
		}
	}
	return out
}

// Of 50 agains of one run at once, half as its end's do: line in code and
// half as its bot's, exactly one starts, carrying the run's chain; every
// other gets already going or a start under way. A late one, once the
// retry ended, names it as gone again.
func TestAgainOnceRace(t *testing.T) {
	sup := fakeSupervisor(t)
	h := newRouteHarness(t)
	dir := filepath.Join(h.dir, "runs")
	notify := filepath.Join(h.dir, "notify")
	r := h.endedPlan(runs.Failed)
	var wg sync.WaitGroup
	var mu sync.Mutex
	started, bad := 0, []error{}
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := h.inProc("planner")
			if i%2 == 0 {
				c.runDo(context.Background(), dir, notify, r, "maestro-ticket job again "+r.ID)
				return
			}
			_, err := c.again(context.Background(), againOpts{run: r.ID})
			var a alreadyGoing
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				started++
			case errors.As(err, &a), errors.Is(err, ticket.ErrClaimed):
			default:
				bad = append(bad, err)
			}
		}()
	}
	wg.Wait()
	if len(bad) > 0 {
		t.Fatalf("losers failed: %v", bad)
	}
	got := againsOf(t, dir, r.ID)
	if len(got) != 1 || started > 1 || sup.count() != 1 {
		t.Fatalf("%d runs went again (%d by again itself, %d supervisors); want 1", len(got), started, sup.count())
	}
	nr := got[0]
	if nr.ChainOf != "R-cccccccc" || nr.Hops != 3 || nr.Job != "plan" {
		t.Fatalf("the retry lost its chain: %+v", nr)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, r.ID+".again")); string(b) != nr.ID {
		t.Fatalf(".again holds %q; want %s", b, nr.ID)
	}
	if !runs.Acked(dir, r.ID) {
		t.Fatal("the retried run stayed unacked")
	}
	h.endAs(nr.ID, runs.Done)
	h.env["MAESTRO_TICKET_ACTOR"] = "planner"
	if code, out, errs := h.run("", "job", "again", r.ID); code != 0 || out != "went again as "+nr.ID+" already: nothing to do\n" {
		t.Fatalf("a stale again: %d %q %q", code, out, errs)
	}
	if n := len(againsOf(t, dir, r.ID)); n != 1 || sup.count() != 1 {
		t.Fatalf("a stale again started one: %d", n)
	}
}

// A start that fails takes its .again mark back, so the next again starts.
func TestAgainMarkFreedOnStartFail(t *testing.T) {
	sup := fakeSupervisor(t)
	h := newRouteHarness(t)
	dir := filepath.Join(h.dir, "runs")
	r := h.endedPlan(runs.Failed)
	sup.noStart = true
	if _, err := h.inProc("planner").again(context.Background(), againOpts{run: r.ID}); err == nil {
		t.Fatal("a command that can't start started")
	}
	if fileExists(filepath.Join(dir, r.ID+".again")) {
		t.Fatal("the failed start left its mark")
	}
	sup.noStart = false
	nr, err := h.inProc("planner").again(context.Background(), againOpts{run: r.ID})
	if err != nil || nr.AgainOf != r.ID || nr.State != runs.Running {
		t.Fatalf("again after a failed start: %v %+v", err, nr)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, r.ID+".again")); string(b) != nr.ID {
		t.Fatalf(".again holds %q; want %s", b, nr.ID)
	}
}

// Of 20 resumes of one died run at once, half as its end's do: line and
// half as pickups or a sweep, one resumes it, and the resume keeps the
// run's chain.
func TestDiedResumeRace(t *testing.T) {
	sup := fakeSupervisor(t)
	h := newRouteHarness(t)
	dir := filepath.Join(h.dir, "runs")
	notify := filepath.Join(h.dir, "notify")
	r := h.endedPlan(runs.Died)
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := []error{}
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := h.inProc("planner")
			if i%2 == 0 {
				c.runDo(context.Background(), dir, notify, r, "maestro-ticket resume "+r.ID+" --died")
				return
			}
			_, err := c.resume(context.Background(), resumeOpts{run: r.ID, died: true})
			var a alreadyGoing
			if err != nil && !errors.As(err, &a) && !errors.Is(err, ticket.ErrClaimed) {
				mu.Lock()
				bad = append(bad, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(bad) > 0 {
		t.Fatalf("losers failed: %v", bad)
	}
	list, _ := runs.List(dir, time.Now(), true)
	var got []runs.Record
	for _, o := range list {
		if o.ResumeOf == r.ID {
			got = append(got, o)
		}
	}
	if len(got) != 1 || sup.count() != 1 {
		t.Fatalf("%d resumes (%d supervisors); want 1", len(got), sup.count())
	}
	if !got[0].AfterDeath || got[0].ChainOf != "R-cccccccc" || got[0].Hops != 3 {
		t.Fatalf("the resume lost its chain: %+v", got[0])
	}
	if flagValue(got[0].Command, "--resume") != "S1" {
		t.Fatalf("the resume lost its session: %q", got[0].Command)
	}
}

// A resume or retry runs in the folder its run ran in, not the caller's:
// Claude Code keeps sessions per folder. One whose folder is gone runs in
// the caller's.
func TestResumeRunsWhereItsRunRan(t *testing.T) {
	sup := fakeSupervisor(t)
	h := newRouteHarness(t)
	dir := filepath.Join(h.dir, "runs")
	orig := t.TempDir()
	for i, start := range []func(c *call, id string) (runs.Record, error){
		func(c *call, id string) (runs.Record, error) {
			return c.resume(context.Background(), resumeOpts{run: id, died: true})
		},
		func(c *call, id string) (runs.Record, error) {
			return c.again(context.Background(), againOpts{run: id})
		},
	} {
		r := h.endedPlan(runs.Died)
		r.Dir = orig
		if err := runs.Save(dir, r); err != nil {
			t.Fatal(err)
		}
		nr, err := start(h.inProc("planner"), r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if nr.Dir != orig || sup.dirs[i] != orig {
			t.Fatalf("start %d ran in %q (supervisor %q); want %q", i, nr.Dir, sup.dirs[i], orig)
		}
		h.endAs(nr.ID, runs.Done)
	}
	r := h.endedPlan(runs.Died)
	r.Dir, r.SessionID = filepath.Join(orig, "gone"), "S2"
	runs.Save(dir, r)
	nr, err := h.inProc("planner").resume(context.Background(), resumeOpts{run: r.ID, died: true})
	if err != nil || nr.Dir != h.dir {
		t.Fatalf("a resume whose folder went: %v, ran in %q; want %q", err, nr.Dir, h.dir)
	}
}

// A retry or resume whose ticket moved on is refused for good: the bot's
// command acks the run, so neither ack nor pickup asks for it again, and
// a run's end drops the line rather than leave it to the bot.
func TestDoMovedOnAcks(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	dir := filepath.Join(h.dir, "runs")
	notify := filepath.Join(h.dir, "notify")
	ended := func(state, do string) runs.Record {
		r, err := runs.Create(dir, runs.Record{Launcher: "senior-engineer", Limit: "1h", Job: "build", Ticket: "T-1",
			Command: []string{"claude", "-p", "--session-id", "S1"}}, []byte("/maestro-software-engineer T-1\n"))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		r.State, r.StartedAt, r.EndedAt = state, now.Add(-time.Minute), &now
		r.Routed, _ = json.Marshal(routed{Run: r.ID, Job: "build", Ticket: "T-1", Outcome: state, Send: []send{}, Do: []string{do + r.ID}})
		if err := runs.Save(dir, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	h.env["MAESTRO_TICKET_ACTOR"] = "senior-engineer"
	again := ended(runs.TimedOut, "maestro-ticket job again ")
	died := ended(runs.Died, "maestro-ticket resume ")
	for _, c := range []struct {
		r    runs.Record
		args []string
	}{
		{again, []string{"job", "again", again.ID}},
		{died, []string{"resume", died.ID, "--died"}},
	} {
		code, out, errs := h.run("", c.args...)
		if code != 0 || !strings.Contains(out, "T-1 is in_review") || !strings.HasSuffix(out, ": acked "+c.r.ID+"\n") || !runs.Acked(dir, c.r.ID) {
			t.Fatalf("%v: %d %q %q", c.args, code, out, errs)
		}
	}
	if len(againsOf(t, dir, again.ID)) != 0 {
		t.Fatal("a retry started on a ticket that moved on")
	}
	if _, out, _ := h.run("", "pickup"); out != "no run of yours ended or running\n" {
		t.Fatalf("pickup: %q", out)
	}
	// In a run's end, the line goes for good.
	r := ended(runs.TimedOut, "maestro-ticket job again ")
	if on, gone := h.c.runDo(context.Background(), dir, notify, r, "maestro-ticket job again "+r.ID); on || !gone {
		t.Fatalf("end's do: on %v, gone %v", on, gone)
	}
	// A refusal that may lift, such as STOP, leaves it.
	h.move("T-1", "in_progress")
	os.WriteFile(filepath.Join(h.dir, sqlitestore.StopFile), nil, 0o644)
	if on, gone := h.c.runDo(context.Background(), dir, notify, r, "maestro-ticket job again "+r.ID); on || gone {
		t.Fatalf("under STOP: on %v, gone %v", on, gone)
	}
}

// runDo runs only the fixed grammar, for this run, for a served launcher,
// and keeps the line on a refusal or a start under way; once the
// pause lifts, the same line starts the retry.
func TestRunDoGrammar(t *testing.T) {
	sup := fakeSupervisor(t)
	h := newRouteHarness(t)
	dir := filepath.Join(h.dir, "runs")
	notify := filepath.Join(h.dir, "notify")
	r := h.saveRun(runs.Record{Launcher: "planner", Job: "plan", State: runs.Failed, IsError: true})
	for _, line := range []string{
		"maestro-ticket rules set limit 300",
		"maestro-ticket rules text",
		"maestro-ticket resume " + r.ID + " --opus",
		"maestro-ticket resume " + r.ID,
		"maestro-ticket job again R-zzzzzzzz",
		"maestro-ticket job again " + r.ID + "; rm -rf /",
		"maestro-ticket job again " + r.ID + " --limit 4h --model sonnet",
		"maestro-ticket job build T-1",
	} {
		if on, _ := h.c.runDo(context.Background(), dir, notify, r, line); on {
			t.Errorf("ran %q", line)
		}
	}
	// A person reads their pickup.
	o := h.saveRun(runs.Record{Launcher: "person", Job: "plan", State: runs.Failed, IsError: true})
	if on, _ := h.c.runDo(context.Background(), dir, notify, o, "maestro-ticket job again "+o.ID); on {
		t.Error("ran for a person")
	}
	// Another start of the same again under way: the line stays.
	os.WriteFile(filepath.Join(dir, r.ID+".again"), nil, 0o644)
	if on, gone := h.c.runDo(context.Background(), dir, notify, r, "maestro-ticket job again "+r.ID); on || gone {
		t.Errorf("beside a start under way: on %v, gone %v", on, gone)
	}
	os.Remove(filepath.Join(dir, r.ID+".again"))
	// Under a pause, the line stays for the launcher, which runs it once
	// the pause lifts.
	setPause(h.dir, pause{Until: time.Now().Add(time.Hour), Why: "usage limit"}, time.Now())
	if on, gone := h.c.runDo(context.Background(), dir, notify, r, "maestro-ticket job again "+r.ID); on || gone {
		t.Errorf("under a pause: on %v, gone %v", on, gone)
	}
	if n := len(againsOf(t, dir, r.ID)); n != 0 || sup.count() != 0 {
		t.Fatalf("%d runs started", n)
	}
	os.Remove(pausePath(h.dir))
	if on, _ := h.c.runDo(context.Background(), dir, notify, r, "maestro-ticket job again "+r.ID); !on {
		t.Fatal("the line ran nothing once the pause lifted")
	}
	if got := againsOf(t, dir, r.ID); len(got) != 1 || got[0].Launcher != "planner" || sup.count() != 1 {
		t.Fatalf("after the pause: %+v", got)
	}
}

// A build that runs out of time goes again in code, as its launcher, with
// twice the time, and its own end acks it, so no bot wakes; when the
// retry runs out of time too, its end blocks the ticket for the judge and
// leaves the line to the planner, and no third build starts.
func TestChainBuildTimeoutRetriesThenBlocks(t *testing.T) {
	sup := fakeSupervisor(t)
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress")
	dir := filepath.Join(h.dir, "runs")
	r1 := h.saveRun(runs.Record{Launcher: "senior-engineer", Limit: "2s", Job: "build", Ticket: "T-1", State: runs.TimedOut, Command: sonnetCmd()})
	h.inProc("senior-engineer").chain(context.Background(), dir, r1)
	builds := func() []runs.Record {
		list, _ := runs.List(dir, time.Now(), true)
		return slices.DeleteFunc(list, func(r runs.Record) bool { return r.Ticket != "T-1" || r.Job != "build" })
	}
	rs := builds()
	if len(rs) != 2 || sup.count() != 1 {
		t.Fatalf("%d builds on T-1 (%d started); want 2", len(rs), sup.count())
	}
	got1, _ := runs.Get(dir, r1.ID, time.Now())
	if got1.AckedBy != "senior-engineer" || len(got1.Chained) != 1 || !strings.HasPrefix(got1.Chained[0], "senior-engineer: maestro-ticket job again "+r1.ID+" --limit 4s") {
		t.Fatalf("first: %+v", got1)
	}
	r2 := h.endAs(rs[1].ID, runs.TimedOut)
	if r2.AgainOf != r1.ID || r2.Limit != "4s" || r2.Launcher != "senior-engineer" {
		t.Fatalf("retry: %+v", r2)
	}
	h.inProc("senior-engineer").chain(context.Background(), dir, r2)
	if r2, _ = runs.Get(dir, r2.ID, time.Now()); r2.Notify != "planner" || len(r2.Chained) != 0 || runs.Acked(dir, r2.ID) {
		t.Fatalf("retry's end: %+v", r2)
	}
	if d := h.ticketOf(1); d.Status != ticket.StatusBlocked {
		t.Fatalf("T-1 is %s", d.Status)
	}
	// The planner's ring carries the retry; the engineer's carries none.
	co := filesCoord(dir)
	if n, ids := ringDigest(co, dir, "planner", time.Now()); !slices.Contains(ids, r2.ID) {
		t.Fatalf("the planner's ring: %+v %v", n, ids)
	}
	if n, ids := ringDigest(co, dir, "senior-engineer", time.Now()); len(ids)+len(n.Runs) != 0 {
		t.Fatalf("the engineer's ring: %+v %v", n, ids)
	}
	h.env["MAESTRO_TICKET_ACTOR"] = "planner"
	if _, out, _ := h.run("", "pickup"); !strings.Contains(out, "send planner: block T-1: Escalation: build timed out or failed twice") {
		t.Fatalf("no block line for the planner: %q", out)
	}
	if n := len(builds()); n != 2 || sup.count() != 1 {
		t.Fatalf("%d builds on T-1 after the block", n)
	}
	if code, out, errs := h.run("", "ack", r2.ID); code != 0 {
		t.Fatalf("ack: %d %q %q", code, out, errs)
	}
}
