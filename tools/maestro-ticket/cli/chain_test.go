//go:build unix

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// chainClaude stands in for claude in a chain: each session does what
// its skill would to T-1, through maestro-ticket, then replies.
const chainClaude = `#!/bin/sh
d=$FAKE_OUT/$MAESTRO_RUN_ID
mkdir -p "$d"
in=$(cat)
printf '%s\n' "$in" >"$d/stdin"
mt() { "$FAKE_MT" "$@" >>"$d/mt" 2>&1; }
case $in in
"/maestro-software-engineer T-1"*) mt transition T-1 --to in_review ;;
"/maestro-code-review T-1"*) printf 'Review: GO at abc1234\n' | mt comment T-1 --body-file - ;;
"/maestro-qa-lead T-1"*) mt transition T-1 --to done --actor-role qa-lead ;;
esac
printf '{"type":"result","subtype":"success","is_error":false,"result":"job: done: %s","session_id":"s","total_cost_usd":0.01,"usage":{},"modelUsage":{}}\n' "$MAESTRO_RUN_ID"
`

// newChainHarness is a job harness whose claude moves T-1 as each
// session would, with a webhook for each bot named.
func newChainHarness(t *testing.T, bots ...string) (*jobHarness, map[string]*hook) {
	t.Helper()
	j := newJobHarness(t, "junior-engineer")
	if err := os.WriteFile(filepath.Join(j.bin, "claude"), []byte(chainClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	git := exec.Command("git", "init", "-q")
	git.Dir = j.h.dir
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	prep := filepath.Join(j.h.dir, ".claude", "skills", "maestro-code-review", "scripts")
	os.MkdirAll(prep, 0o755)
	os.WriteFile(filepath.Join(prep, "review-prep.sh"), []byte("echo lines=30; echo route=full; echo why=code\n"), 0o755)
	hooks := map[string]*hook{}
	for _, b := range bots {
		hooks[b] = j.setHook(b)
	}
	return j, hooks
}

// setHook gives bot a webhook that counts its rings.
func (j *jobHarness) setHook(bot string) *hook {
	j.t.Helper()
	k, url := newHook(j.t)
	if code, _, errs := j.h.run(url+"\n", "notify", bot, "--url-file", "-"); code != 0 {
		j.t.Fatal(errs)
	}
	return k
}

func (j *jobHarness) chainCmd(actor, stdin string, args ...string) *exec.Cmd {
	c := j.cmd(stdin, args...)
	c.Env = append(c.Env, "FAKE_MT="+j.exe, "MAESTRO_TICKET_ACTOR="+actor)
	return c
}

// status waits until T-1 is in want.
func (j *jobHarness) waitStatus(id, want string, within time.Duration) {
	j.t.Helper()
	deadline := time.Now().Add(within)
	for {
		var d struct{ Status string }
		j.h.ok(&d, "get", id)
		if d.Status == want {
			return
		}
		if time.Now().After(deadline) {
			j.t.Fatalf("%s still %s after %s; want %s", id, d.Status, within, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func allRuns(t *testing.T, dir string) map[string]runs.Record {
	t.Helper()
	list, err := runs.List(filepath.Join(dir, "runs"), time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]runs.Record{}
	for _, r := range list {
		out[r.Job] = r
	}
	return out
}

func (j *jobHarness) pickupAs(actor string) string {
	j.t.Helper()
	out, err := j.chainCmd(actor, "", "pickup").CombinedOutput()
	if err != nil {
		j.t.Fatalf("pickup as %s: %v\n%s", actor, err, out)
	}
	return string(out)
}

// A build's end starts its review, as the reviewer; the GO starts QA, as
// the QA lead, on the slots the QA lead last asked for; QA merges. No
// worker bot is rung: the planner hears of the free slot and the merge,
// in its own pickup, and acks them.
func TestChainRunsTheHandoffs(t *testing.T) {
	j, hooks := newChainHarness(t, "junior-engineer", "reviewer", "qa-lead")
	j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
	j.h.ok(nil, "create", "--title", "b") // ready work, so the planner hears of slots
	j.h.ok(nil, "transition", "T-1", "--to", "in_progress")
	// The QA lead's memories say 3 slots, as its last run passed.
	j.h.ok(nil, "create", "--title", "old")
	for _, st := range []string{"in_progress", "in_review"} {
		j.h.ok(nil, "transition", "T-3", "--to", st)
	}
	out, err := j.chainCmd("qa-lead", "", "job", "qa", "T-3", "--slots", "3").CombinedOutput()
	if err != nil {
		t.Fatalf("qa T-3: %v\n%s", err, out)
	}
	old := strings.Fields(string(out))[0]
	j.waitEnded(old, 20*time.Second)
	hooks["qa-lead"].wait(t, 20*time.Second)
	if out, err := j.chainCmd("qa-lead", "", "ack", old).CombinedOutput(); err != nil {
		t.Fatalf("ack: %v\n%s", err, out)
	}
	hooks["planner"] = j.setHook("planner")
	qaRings := hooks["qa-lead"].count()

	if out, err := j.chainCmd("junior-engineer", "", "job", "build", "T-1").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	j.waitStatus("T-1", "done", 60*time.Second)
	hooks["planner"].wait(t, 20*time.Second)
	hooks["planner"].wait(t, 20*time.Second)

	rs := allRuns(t, j.h.dir)
	build, review, qa := rs["build"], rs["review"], rs["qa"]
	if review.Launcher != "reviewer" || review.ChainOf != build.ID || review.Hops != 1 {
		t.Fatalf("review: %+v", review)
	}
	if qa.Ticket != "T-1" || qa.Launcher != "qa-lead" || qa.ChainOf != review.ID || qa.Hops != 2 || flagValue(qa.Command, "--max") != "3" {
		t.Fatalf("qa: %+v", qa)
	}
	if len(build.Chained) != 1 || build.Chained[0] != "reviewer: review T-1" || build.Notify != "planner" {
		t.Fatalf("build: chained %q notify %q", build.Chained, build.Notify)
	}
	for _, b := range []string{"junior-engineer", "reviewer"} {
		if n := hooks[b].count(); n != 0 {
			t.Errorf("%s rung %d times", b, n)
		}
	}
	if n := hooks["qa-lead"].count(); n != qaRings {
		t.Errorf("qa-lead rung %d more times", n-qaRings)
	}
	for _, b := range []string{"junior-engineer", "reviewer", "qa-lead"} {
		if got := j.pickupAs(b); got != "no run of yours ended or running\n" {
			t.Errorf("%s pickup: %s", b, got)
		}
	}
	got := j.pickupAs("planner")
	for _, want := range []string{
		build.ID + " build done on T-1 (in_review): job: done: " + build.ID + "\nsend planner: slot free T-1\n",
		qa.ID + " qa done on T-1 (done): job: done: " + qa.ID + "\nsend planner: merged T-1\n",
		"ack each once you have acted on it: maestro-ticket ack ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("planner pickup:\n%s\nwant it to hold:\n%s", got, want)
		}
	}
	if out, err := j.chainCmd("planner", "", "ack", build.ID, qa.ID).CombinedOutput(); err != nil {
		t.Fatalf("planner ack: %v\n%s", err, out)
	}
	if out, err := j.chainCmd("junior-engineer", "", "ack", build.ID).CombinedOutput(); err == nil {
		t.Fatalf("the launcher acked a run left to the planner: %s", out)
	}
}

// A bot with no webhook would never hear how its run ended, so no chain
// starts its job: the launcher is rung and sends it on, as before.
func TestChainNeedsTheNextBotsWebhook(t *testing.T) {
	j, hooks := newChainHarness(t, "junior-engineer", "planner")
	j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
	j.h.ok(nil, "transition", "T-1", "--to", "in_progress")
	if out, err := j.chainCmd("junior-engineer", "", "job", "build", "T-1").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	hooks["junior-engineer"].wait(t, 20*time.Second)
	got := j.pickupAs("junior-engineer")
	if !strings.Contains(got, "\nsend reviewer: review T-1\n") {
		t.Fatalf("pickup:\n%s", got)
	}
	if hooks["planner"].count() != 0 || allRuns(t, j.h.dir)["review"].ID != "" {
		t.Fatal("a review started, or the planner was rung")
	}
	j.ackAll()
}

// endedRun saves a run of job on T-1 that ended done, as its supervisor
// would before it chains, with reply as its reply.
func (h *routeHarness) endedRun(job, launcher, reply string) runs.Record {
	h.t.Helper()
	dir := filepath.Join(h.dir, "runs")
	os.MkdirAll(dir, 0o755)
	r, err := runs.Create(dir, runs.Record{Launcher: launcher, Limit: "1h", Command: sonnetCmd()}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	now := time.Now()
	r.State, r.Ticket, r.Job, r.StartedAt, r.EndedAt, r.Reply = runs.Done, "T-1", job, now.Add(-time.Minute), &now, reply
	r.IsError = strings.Contains(reply, "limit")
	if err := runs.Save(dir, r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *routeHarness) hook(bot string) {
	h.t.Helper()
	if code, _, errs := h.run("http://127.0.0.1:1/x\n", "notify", bot, "--url-file", "-"); code != 0 {
		h.t.Fatal(errs)
	}
}

// Only a fixed job for a worker bot chains, and none past maxHops.
func TestChainNextStep(t *testing.T) {
	for _, c := range []struct {
		bot, line string
		hops      int
		want      string
	}{
		{"reviewer", "review T-1", 0, "review T-1"},
		{"qa-lead", "qa T-12", maxHops - 1, "qa T-12"},
		{"senior-engineer", "build T-3", 2, "build T-3"},
		{"reviewer", "review T-1", maxHops, ""},
		{"planner", "slot free T-1", 0, ""},
		{"chief-of-staff", "design T-1: card", 0, ""},
		{"reviewer", "review T-1 --approved abc1234", 0, ""},
		{"qa-lead", "live T-1", 0, ""},
		{"designer", "build T-1", 0, ""},
	} {
		got := ""
		if k, id, ok := nextStep(runs.Record{Hops: c.hops}, send{c.bot, c.line}); ok {
			got = k.name + " " + id
		}
		if got != c.want {
			t.Errorf("%s: %s at %d hops: %q; want %q", c.bot, c.line, c.hops, got, c.want)
		}
	}
}

// A review a look handed out lately is on its way: the end starts none,
// and with nothing left to do, acks the run.
func TestChainLeavesALooksHandout(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a", "--tier", "junior")
	h.move("T-1", "in_progress", "in_review")
	h.hook("reviewer")
	dir := filepath.Join(h.dir, "runs")
	h.c.actor = "junior-engineer"

	if !claimHandout(filepath.Join(h.dir, "handouts"), "T-1", "reviewer", "review T-1", time.Hour, time.Now()) {
		t.Fatal("no handout")
	}
	r := h.endedRun("build", "junior-engineer", "build: done")
	h.c.chain(context.Background(), dir, r)
	got, _ := runs.Load(dir, r.ID)
	if len(got.Chained) != 0 || !runs.Acked(dir, r.ID) {
		t.Fatalf("a look's handout: %+v acked %v", got, runs.Acked(dir, r.ID))
	}
	if list, _ := runs.List(dir, time.Now(), true); len(list) != 1 {
		t.Fatalf("%d runs; want the one ended", len(list))
	}
}

// A run that hit the usage limit is pickup's to route: the chain leaves
// it whole, so the planner still hears of the pause.
func TestChainLeavesALimitToPickup(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	h.hook("reviewer")
	h.hook("planner")
	dir := filepath.Join(h.dir, "runs")
	r := h.endedRun("review", "reviewer", "Claude AI usage limit reached|"+strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	h.c.chain(context.Background(), dir, r)
	if got, _ := runs.Load(dir, r.ID); len(got.Routed) != 0 || got.Notify != "" || runs.Acked(dir, r.ID) {
		t.Fatalf("chained a limit: %+v", got)
	}
	if fileExists(pausePath(h.dir)) {
		t.Fatal("the chain set the pause, which pickup shows the planner once")
	}
	if rt := h.route(r); len(sends(rt)) != 1 || !strings.HasPrefix(sends(rt)[0], "send planner: usage_limit T-1") {
		t.Fatalf("pickup after: %+v", rt)
	}
}
