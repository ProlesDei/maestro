//go:build unix

package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// chainClaude stands in for claude in a chain: each session does what
// its skill would to T-1, through maestro-ticket, then replies. The
// first review sends it back; the second passes.
const chainClaude = `#!/bin/sh
d=$FAKE_OUT/$MAESTRO_RUN_ID
mkdir -p "$d"
in=$(cat)
printf '%s\n' "$in" >"$d/stdin"
mt() { "$FAKE_MT" "$@" >>"$d/mt" 2>&1; }
case $in in
"/maestro-software-engineer T-1"*) mt transition T-1 --to in_review ;;
"/maestro-code-review T-1"*)
	if [ -e "$FAKE_OUT/kicked" ]; then
		printf 'Review: GO at abc1234\n' | mt comment T-1 --body-file -
	else
		: >"$FAKE_OUT/kicked"
		printf 'Kickback: review at abc1234: F1 no test\n' | mt comment T-1 --body-file -
		mt transition T-1 --to in_progress
	fi ;;
"/maestro-qa-lead T-1"*) mt transition T-1 --to done --actor-role qa-lead ;;
"/maestro-qa-lead T-3"*) mt transition T-3 --to done --actor-role qa-lead ;;
esac
sleep 1 # as long as job takes to record its run, which a real run outlasts
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

func (j *jobHarness) pickupAs(actor string) string {
	j.t.Helper()
	out, err := j.chainCmd(actor, "", "pickup").CombinedOutput()
	if err != nil {
		j.t.Fatalf("pickup as %s: %v\n%s", actor, err, out)
	}
	return string(out)
}

// ticketRuns are the runs on id, oldest first.
func ticketRuns(t *testing.T, dir, id string) []runs.Record {
	t.Helper()
	list, err := runs.List(filepath.Join(dir, "runs"), time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	out := []runs.Record{}
	for _, r := range list {
		if r.Ticket == id {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b runs.Record) int { return a.StartedAt.Compare(b.StartedAt) })
	return out
}

// A build's end starts its review, as the reviewer; the send-back starts
// the junior's build again; the GO starts QA, as the QA lead, on the
// slots the QA lead last asked for; QA merges. No worker bot is rung:
// the planner hears of each free slot and the merge, rung as itself, in
// its own pickup, and acks them.
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
	j.waitStatus("T-1", "done", 90*time.Second)
	for range 3 {
		if n := hooks["planner"].wait(t, 20*time.Second); n.Launcher != "planner" {
			t.Fatalf("the planner's ring names %q", n.Launcher)
		}
	}

	rs := ticketRuns(t, j.h.dir, "T-1")
	want := []struct{ job, launcher string }{
		{"build", "junior-engineer"}, {"review", "reviewer"}, {"build", "junior-engineer"}, {"review", "reviewer"}, {"qa", "qa-lead"},
	}
	if len(rs) != len(want) {
		t.Fatalf("%d runs on T-1; want %d: %+v", len(rs), len(want), rs)
	}
	for i, w := range want {
		r := rs[i]
		chainOf := ""
		if i > 0 {
			chainOf = rs[i-1].ID
		}
		if r.Job != w.job || r.Launcher != w.launcher || r.ChainOf != chainOf || r.Hops != i {
			t.Fatalf("run %d: %s by %s, chain_of %q, hops %d; want %s by %s after %q", i, r.Job, r.Launcher, r.ChainOf, r.Hops, w.job, w.launcher, chainOf)
		}
	}
	build, kicked, build2, qa := rs[0], rs[1], rs[2], rs[4]
	if flagValue(qa.Command, "--max") != "3" {
		t.Fatalf("qa slots: %q", qa.Command)
	}
	if len(build.Chained) != 1 || build.Chained[0] != "reviewer: review T-1" || build.Notify != "planner" {
		t.Fatalf("build: chained %q notify %q", build.Chained, build.Notify)
	}
	if len(kicked.Chained) != 1 || kicked.Chained[0] != "junior-engineer: build T-1" || kicked.Notify != "" || kicked.AckedBy != chainBy {
		t.Fatalf("send-back: chained %q notify %q acked by %q", kicked.Chained, kicked.Notify, kicked.AckedBy)
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
		build2.ID + " build done on T-1 (in_review): job: done: " + build2.ID + "\nsend planner: slot free T-1\n",
		qa.ID + " qa done on T-1 (done): job: done: " + qa.ID + "\nsend planner: merged T-1\n",
		"ack each once you have acted on it: maestro-ticket ack ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("planner pickup:\n%s\nwant it to hold:\n%s", got, want)
		}
	}
	// A start lists the runs left to the planner, as its pickup does.
	out, err = j.chainCmd("planner", "the board\n", "job", "plan").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "3 ended, unacked: run pickup") {
		t.Fatalf("planner's job: %v\n%s", err, out)
	}
	plan := strings.Fields(string(out))[0]
	// The launcher's batch, listed before its end left it to the planner,
	// goes on past it and acks nothing of the planner's.
	out, err = j.chainCmd("junior-engineer", "", "ack", build.ID).CombinedOutput()
	if err != nil || string(out) != build.ID+" is left to planner: nothing to do\n" || runs.Acked(filepath.Join(j.h.dir, "runs"), build.ID) {
		t.Fatalf("launcher's ack: %v\n%s", err, out)
	}
	j.waitEnded(plan, 20*time.Second)
	if out, err := j.chainCmd("planner", "", "ack", build.ID, build2.ID, qa.ID, plan).CombinedOutput(); err != nil {
		t.Fatalf("planner ack: %v\n%s", err, out)
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
	if hooks["planner"].count() != 0 || len(ticketRuns(t, j.h.dir, "T-1")) != 1 {
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

	// A launcher with no webhook, such as a person, reads its own pickup:
	// the run stays for it, routed.
	if !claimHandout(filepath.Join(h.dir, "handouts"), "T-1", "reviewer", "review T-1", time.Hour, time.Now()) {
		t.Fatal("no handout")
	}
	r := h.endedRun("build", "junior-engineer", "build: done")
	h.c.chain(context.Background(), dir, r)
	if got, _ := runs.Load(dir, r.ID); len(got.Routed) == 0 || len(got.Chained) != 0 || runs.Acked(dir, r.ID) {
		t.Fatalf("a launcher with no webhook: %+v acked %v", got, runs.Acked(dir, r.ID))
	}
	h.hook("junior-engineer")

	r = h.endedRun("build", "junior-engineer", "build: done")
	h.c.chain(context.Background(), dir, r)
	got, _ := runs.Load(dir, r.ID)
	if len(got.Chained) != 0 || !runs.Acked(dir, r.ID) {
		t.Fatalf("a look's handout: %+v acked %v", got, runs.Acked(dir, r.ID))
	}
	if list, _ := runs.List(dir, time.Now(), true); len(list) != 2 {
		t.Fatalf("%d runs; want the two ended", len(list))
	}
}

// A run that hit the usage limit starts nothing, but its end routes it
// and keeps the route: the pause is set once, and the planner hears of it
// from the run's ring and pickup, which show that route.
func TestChainStoresALimitsRoute(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	h.hook("reviewer")
	h.hook("planner")
	dir := filepath.Join(h.dir, "runs")
	r := h.endedRun("review", "reviewer", "Claude AI usage limit reached|"+strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	h.c.chain(context.Background(), dir, r)
	got, _ := runs.Load(dir, r.ID)
	var rt routed
	if json.Unmarshal(got.Routed, &rt) != nil || got.Notify != "" || len(got.Chained) != 0 || runs.Acked(dir, r.ID) {
		t.Fatalf("chained a limit: %+v", got)
	}
	if !fileExists(pausePath(h.dir)) || len(sends(rt)) != 1 || !strings.HasPrefix(sends(rt)[0], "send planner: usage_limit T-1") {
		t.Fatalf("stored route: %+v", rt)
	}
}

// Only sends may go unseen: a command, a reply or a failed write stays
// for a bot.
func TestChainLeftForBot(t *testing.T) {
	for _, c := range []struct {
		rt   routed
		want bool
	}{
		{routed{Send: []send{{"planner", "merged T-1"}}}, false},
		{routed{Wrote: []string{"released T-1 for the planner"}}, false},
		{routed{Wrote: []string{"follow-ups not filed: T-9 is held"}}, true},
		{routed{Wrote: []string{"not parked for the answer: locked"}}, true},
		{routed{Do: []string{"maestro-ticket resume R-aaaaaaaa --opus"}}, true},
		{routed{Reply: "the answer"}, true},
	} {
		if got := leftForBot(c.rt); got != c.want {
			t.Errorf("%+v: %v; want %v", c.rt, got, c.want)
		}
	}
}

// The chain's time limit stops a review's start even when the route
// script's child, such as a gh that hangs, holds its output open.
func TestChainLimitStopsAHungRoute(t *testing.T) {
	dir, bin := t.TempDir(), t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	prep := filepath.Join(dir, ".claude", "skills", "maestro-code-review", "scripts")
	os.MkdirAll(prep, 0o755)
	os.WriteFile(filepath.Join(prep, "review-prep.sh"), []byte("sleep 30\necho route=full\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	c := &call{env: Env{Getenv: func(string) string { return "" }, Dir: dir}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.reviewRoute(ctx, "T-1"); err == nil || time.Since(start) > 15*time.Second {
		t.Fatalf("route: %v after %s", err, time.Since(start))
	}
}

// A run whose route left a command for its bot is the bot's: the chain
// neither acks it nor leaves it to the planner.
func TestChainLeavesACommandToTheBot(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	h.hook("reviewer")
	h.hook("planner")
	dir := filepath.Join(h.dir, "runs")
	r := h.endedRun("review", "reviewer", "review: stopped: tier 2 needs the full model")
	h.c.chain(context.Background(), dir, r)
	got, _ := runs.Load(dir, r.ID)
	if got.Notify != "" || runs.Acked(dir, r.ID) {
		t.Fatalf("a do: line: %+v acked %v", got, runs.Acked(dir, r.ID))
	}
	var rt routed
	if json.Unmarshal(got.Routed, &rt) != nil || len(rt.Do) != 1 || !strings.Contains(rt.Do[0], "job again "+r.ID+" --full") {
		t.Fatalf("stored route: %+v", rt)
	}
}

// A stored route's follow-ups that failed to file are tried again, as
// routing the run again on each pickup did.
func TestChainRefilesFollowUps(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a", "--areas", "api")
	h.move("T-1", "in_progress", "in_review")
	r := h.endedRun("review", "reviewer", "Verdict: GO\n## Follow-ups\n- logs print emails\n## Outcome")
	rt := routed{Send: []send{}, Wrote: []string{"follow-ups not filed: store busy"}}
	h.c.refileFollowUps(context.Background(), h.s, r, &rt)
	if len(rt.Wrote) != 1 || !strings.HasPrefix(rt.Wrote[0], "1 follow-ups from T-1 on T-2") {
		t.Fatalf("wrote: %q", rt.Wrote)
	}
}
