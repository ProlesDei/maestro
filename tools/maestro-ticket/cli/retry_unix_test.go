//go:build unix

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// inProc is a call as actor would make it on j's store, in this process,
// as a supervisor's chain makes them.
func (j *jobHarness) inProc(actor string) *call {
	h := j.h
	return &call{actor: actor, env: Env{Getenv: func(k string) string { return h.env[k] }, Dir: h.dir, Stdout: io.Discard, Stderr: io.Discard}}
}

// endedPlan saves an ended goal plan of planner's, with its prompt, whose
// command runs for a second and replies nothing.
func (j *jobHarness) endedPlan(state string, extra func(*runs.Record)) runs.Record {
	j.t.Helper()
	dir := filepath.Join(j.h.dir, "runs")
	r, err := runs.Create(dir, runs.Record{Launcher: "planner", Limit: "1m", Job: "plan",
		Command: []string{"sh", "-c", "sleep 1", "--session-id", "S1"}}, []byte("/maestro-planner plan goal x: y\n"))
	if err != nil {
		j.t.Fatal(err)
	}
	now := time.Now()
	r.State, r.PID, r.StartedAt, r.EndedAt, r.IsError = state, 99, now.Add(-time.Minute), &now, state == runs.Failed
	r.ChainOf, r.Hops = "R-cccccccc", 3
	if extra != nil {
		extra(&r)
	}
	if err := runs.Save(dir, r); err != nil {
		j.t.Fatal(err)
	}
	return r
}

// ackEvery acks every run on the store as its owner once the test ends,
// so each supervisor stops ringing and exits before the temp dir goes.
func (j *jobHarness) ackEvery() {
	j.t.Cleanup(func() {
		dir := filepath.Join(j.h.dir, "runs")
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			list, _ := runs.List(dir, time.Now(), false)
			live := 0
			for _, r := range list {
				if r.Ended() && !r.Settling {
					runs.Ack(dir, r.ID, r.Owner(), time.Now())
				} else {
					live++
				}
			}
			if live == 0 {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
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
	j := newJobHarness(t, "planner")
	j.setHook("planner")
	j.ackEvery()
	dir := filepath.Join(j.h.dir, "runs")
	notify := filepath.Join(j.h.dir, "notify")
	r := j.endedPlan(runs.Failed, nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	started, bad := 0, []error{}
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := j.inProc("planner")
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
	if len(got) != 1 || started > 1 {
		t.Fatalf("%d runs went again (%d by again itself); want 1", len(got), started)
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
	j.waitEnded(nr.ID, 20*time.Second)
	c := j.cmd("", "job", "again", r.ID)
	c.Env = append(c.Env, "MAESTRO_TICKET_ACTOR=planner")
	out, err := c.CombinedOutput()
	if err != nil || string(out) != "went again as "+nr.ID+" already: nothing to do\n" {
		t.Fatalf("a stale again: %v %s", err, out)
	}
	if n := len(againsOf(t, dir, r.ID)); n != 1 {
		t.Fatalf("a stale again started one: %d", n)
	}
}

// A start that fails takes its .again mark back, so the next again starts.
func TestAgainMarkFreedOnStartFail(t *testing.T) {
	j := newJobHarness(t, "planner")
	j.ackEvery()
	dir := filepath.Join(j.h.dir, "runs")
	r := j.endedPlan(runs.Failed, func(r *runs.Record) { r.Command = []string{filepath.Join(t.TempDir(), "nothing-here")} })
	c := j.inProc("planner")
	if _, err := c.again(context.Background(), againOpts{run: r.ID}); err == nil {
		t.Fatal("a command that can't start started")
	}
	if fileExists(filepath.Join(dir, r.ID+".again")) {
		t.Fatal("the failed start left its mark")
	}
	r.Command = []string{"sh", "-c", "true"}
	if err := runs.Save(dir, r); err != nil {
		t.Fatal(err)
	}
	nr, err := j.inProc("planner").again(context.Background(), againOpts{run: r.ID})
	if err != nil || nr.AgainOf != r.ID {
		t.Fatalf("again after a failed start: %v %+v", err, nr)
	}
	j.waitEnded(nr.ID, 20*time.Second)
}

// Of two pickups and a sweep that each resume one died run at once, one
// resumes it, and the resume keeps the run's chain.
func TestDiedResumeRace(t *testing.T) {
	j := newJobHarness(t, "planner")
	j.setHook("planner")
	j.ackEvery()
	dir := filepath.Join(j.h.dir, "runs")
	notify := filepath.Join(j.h.dir, "notify")
	r := j.endedPlan(runs.Died, nil)
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := j.inProc("planner")
			if i == 0 {
				c.runDo(context.Background(), dir, notify, r, "maestro-ticket resume "+r.ID+" --died")
				return
			}
			c.resume(context.Background(), resumeOpts{run: r.ID, died: true})
		}()
	}
	wg.Wait()
	list, _ := runs.List(dir, time.Now(), true)
	var got []runs.Record
	for _, o := range list {
		if o.ResumeOf == r.ID {
			got = append(got, o)
		}
	}
	if len(got) != 1 || !got[0].AfterDeath || got[0].ChainOf != "R-cccccccc" || got[0].Hops != 3 {
		t.Fatalf("resumes: %+v", got)
	}
	j.waitEnded(got[0].ID, 20*time.Second)
}

// runDo runs only the fixed grammar, for this run, for a launcher with a
// webhook, and keeps the line on a refusal or a start under way.
func TestRunDoGrammar(t *testing.T) {
	h := newRouteHarness(t)
	h.hook("planner")
	dir := filepath.Join(h.dir, "runs")
	notify := filepath.Join(h.dir, "notify")
	r := h.saveRun(runs.Record{Launcher: "planner", Job: "plan", State: runs.Failed, IsError: true})
	for _, line := range []string{
		"maestro-ticket rules set limit 300",
		"maestro-ticket rules text",
		"maestro-ticket resume " + r.ID + " --opus",
		"maestro-ticket resume " + r.ID,
		"maestro-ticket job again " + r.ID + " --full",
		"maestro-ticket job again R-zzzzzzzz",
		"maestro-ticket job again " + r.ID + "; rm -rf /",
		"maestro-ticket job again " + r.ID + " --limit 4h --model sonnet",
		"maestro-ticket job build T-1",
	} {
		if h.c.runDo(context.Background(), dir, notify, r, line) {
			t.Errorf("ran %q", line)
		}
	}
	// A launcher with no webhook reads its pickup.
	o := h.saveRun(runs.Record{Launcher: "senior-engineer", Job: "plan", State: runs.Failed, IsError: true})
	if h.c.runDo(context.Background(), dir, notify, o, "maestro-ticket job again "+o.ID) {
		t.Error("ran for a launcher with no webhook")
	}
	// Another start of the same again under way: the line stays.
	os.WriteFile(filepath.Join(dir, r.ID+".again"), nil, 0o644)
	if h.c.runDo(context.Background(), dir, notify, r, "maestro-ticket job again "+r.ID) {
		t.Error("ran beside a start under way")
	}
	os.Remove(filepath.Join(dir, r.ID+".again"))
	// Under a pause, the line stays for the launcher, which runs it once
	// the pause lifts.
	setPause(h.dir, pause{Until: time.Now().Add(time.Hour), Why: "usage limit"}, time.Now())
	if h.c.runDo(context.Background(), dir, notify, r, "maestro-ticket job again "+r.ID) {
		t.Error("ran under a pause")
	}
	if n := len(againsOf(t, dir, r.ID)); n != 0 {
		t.Fatalf("%d runs started", n)
	}
}

// A build that runs out of time goes again in code, as its launcher, with
// twice the time, and its own end acks it, so no bot wakes; when the
// retry runs out of time too, its end blocks the ticket for the judge and
// leaves the line to the planner, and no third build starts.
func TestChainBuildTimeoutRetriesThenBlocks(t *testing.T) {
	j, hooks := newChainHarness(t, "senior-engineer", "planner")
	if err := os.WriteFile(filepath.Join(j.bin, "claude"), []byte("#!/bin/sh\ncat >/dev/null\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	j.h.ok(nil, "create", "--title", "a")
	j.h.ok(nil, "transition", "T-1", "--to", "in_progress")
	out, err := j.chainCmd("senior-engineer", "", "job", "build", "T-1", "--limit", "2s").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	first := strings.Fields(string(out))[0]
	hooks["planner"].wait(t, 40*time.Second)
	rs := ticketRuns(t, j.h.dir, "T-1")
	if len(rs) != 2 {
		t.Fatalf("%d builds on T-1; want 2", len(rs))
	}
	r1, r2 := rs[0], j.waitEnded(rs[1].ID, 20*time.Second)
	if r1.ID != first || r1.AckedBy != "senior-engineer" || len(r1.Chained) != 1 || !strings.HasPrefix(r1.Chained[0], "senior-engineer: maestro-ticket job again "+first+" --limit 4s") {
		t.Fatalf("first: %+v", r1)
	}
	if r2.AgainOf != first || r2.Limit != "4s" || r2.Launcher != "senior-engineer" || r2.State != runs.TimedOut || r2.Notify != "planner" {
		t.Fatalf("retry: %+v", r2)
	}
	var d struct{ Status string }
	j.h.ok(&d, "get", "T-1")
	if d.Status != "blocked" {
		t.Fatalf("T-1 is %s", d.Status)
	}
	if !strings.Contains(j.pickupAs("planner"), "send planner: block T-1: Escalation: build timed out or failed twice") {
		t.Fatal("no block line for the planner")
	}
	if n := hooks["senior-engineer"].count(); n != 0 {
		t.Errorf("the engineer was rung %d times", n)
	}
	if n := len(ticketRuns(t, j.h.dir, "T-1")); n != 2 {
		t.Fatalf("%d builds on T-1 after the block", n)
	}
	if out, err := j.chainCmd("planner", "", "ack", r2.ID).CombinedOutput(); err != nil {
		t.Fatalf("ack: %v\n%s", err, out)
	}
}
