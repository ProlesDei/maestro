//go:build unix

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// fakeClaude stands in for claude: it saves its arguments, stdin and
// environment under $FAKE_OUT/<run id>/ and replies as claude -p
// --output-format json does, with the session it was given.
const fakeClaude = `#!/bin/sh
d=$FAKE_OUT/$MAESTRO_RUN_ID
mkdir -p "$d"
printf '%s\n' "$@" >"$d/args"
cat >"$d/stdin"
env >"$d/env"
sid= prev=
for a in "$@"; do
	[ "$prev" = --session-id ] && sid=$a
	[ "$prev" = --resume ] && sid=$a
	prev=$a
done
printf '{"type":"result","subtype":"success","is_error":false,"result":"job: done: %s","session_id":"%s","total_cost_usd":0.01,"usage":{},"modelUsage":{}}\n' "$MAESTRO_RUN_ID" "$sid"
`

type jobHarness struct {
	*launcher
	bin, out string
}

func newJobHarness(t *testing.T, actor string) *jobHarness {
	t.Helper()
	l := newLauncher(t, actor)
	j := &jobHarness{launcher: l, bin: t.TempDir(), out: t.TempDir()}
	if err := os.WriteFile(filepath.Join(j.bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	return j
}

func (j *jobHarness) cmd(stdin string, args ...string) *exec.Cmd {
	c := j.launcher.cmd(stdin, args...)
	c.Env = append(c.Env, "PATH="+j.bin+":"+os.Getenv("PATH"), "FAKE_OUT="+j.out)
	return c
}

type started struct {
	Run   runs.Record
	Model string
	Ended []string
}

// job runs maestro-ticket job and waits for its run to end.
func (j *jobHarness) job(stdin string, args ...string) (started, runs.Record) {
	j.t.Helper()
	out, err := j.cmd(stdin, append([]string{"job", "--json"}, args...)...).Output()
	if err != nil {
		j.t.Fatalf("job %v: %v\n%s", args, err, out)
	}
	var s started
	if err := json.Unmarshal(out, &s); err != nil {
		j.t.Fatalf("job: %v: %s", err, out)
	}
	return s, j.waitEnded(s.Run.ID, 20*time.Second)
}

func (j *jobHarness) resume(stdin string, args ...string) (started, runs.Record) {
	j.t.Helper()
	out, err := j.cmd(stdin, append([]string{"resume", "--json"}, args...)...).Output()
	if err != nil {
		j.t.Fatalf("resume %v: %v\n%s", args, err, out)
	}
	var s started
	if err := json.Unmarshal(out, &s); err != nil {
		j.t.Fatalf("resume: %v: %s", err, out)
	}
	return s, j.waitEnded(s.Run.ID, 20*time.Second)
}

// fails runs a command that must fail with code, and returns its output.
func (j *jobHarness) fails(code int, stdin string, args ...string) string {
	j.t.Helper()
	out, err := j.cmd(stdin, args...).CombinedOutput()
	var ee *exec.ExitError
	if err == nil || !errorAs(err, &ee) || ee.ExitCode() != code {
		j.t.Fatalf("%v: %v; want exit %d\n%s", args, err, code, out)
	}
	return string(out)
}

func errorAs(err error, ee **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	*ee = e
	return ok
}

// seen is what the stand-in claude got for a run.
type seen struct{ args, stdin, env []string }

func (j *jobHarness) seen(id string) seen {
	j.t.Helper()
	read := func(name string) []string {
		b, err := os.ReadFile(filepath.Join(j.out, id, name))
		if err != nil {
			j.t.Fatalf("run %s: %v", id, err)
		}
		return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	}
	return seen{read("args"), read("stdin"), read("env")}
}

func (s seen) flag(name string) string { return flagValue(s.args, name) }

func TestJobBuildPicksTheModelByTier(t *testing.T) {
	j := newJobHarness(t, "junior-engineer")
	j.h.ok(nil, "create", "--title", "docs", "--tier", "junior")
	j.h.ok(nil, "create", "--title", "auth")

	s, r := j.job("", "build", "T-1")
	if s.Model != "sonnet" || r.Job != "build" || r.Ticket != "T-1" || r.Limit != "2h" || r.State != runs.Done {
		t.Fatalf("junior build: %+v, run %+v", s, r)
	}
	got := j.seen(r.ID)
	if got.stdin[0] != "/maestro-software-engineer T-1" {
		t.Fatalf("prompt %q", got.stdin)
	}
	for f, want := range map[string]string{"--model": "sonnet", "--effort": "high", "--permission-mode": "bypassPermissions", "--output-format": "json"} {
		if v := got.flag(f); v != want {
			t.Errorf("%s %q; want %q", f, v, want)
		}
	}
	if sid := got.flag("--session-id"); len(sid) != 36 || r.SessionID != sid {
		t.Errorf("session id %q, record %q", sid, r.SessionID)
	}
	if got.args[0] != "-p" {
		t.Errorf("args %q: want -p first", got.args)
	}
	if !slices.Contains(got.env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1") || slices.ContainsFunc(got.env, func(e string) bool { return strings.HasPrefix(e, "CLAUDE_CODE_PROMPT_CACHE_TTL=") }) {
		t.Errorf("a build runs checks: auto memory off, the default cache: %q", got.env)
	}

	s, r = j.job("", "build", "T-2")
	if s.Model != "opus" || j.seen(r.ID).flag("--model") != "opus" {
		t.Fatalf("a ticket with no tier builds on Opus: %+v", s)
	}
	if len(s.Ended) != 1 || s.Ended[0] != j.pickup().Ended[0].ID {
		t.Fatalf("job lists the actor's ended runs it hasn't acked: %+v", s.Ended)
	}

	// Sent back twice, a junior's ticket goes to the senior, on Opus.
	for _, st := range []string{"in_progress", "in_review", "in_progress", "in_review", "in_progress"} {
		j.h.ok(nil, "transition", "T-1", "--to", st)
	}
	if s, _ := j.job("", "build", "T-1"); s.Model != "opus" {
		t.Fatalf("a junior's ticket at TRIES 2: %+v", s)
	}

	s, r = j.job("", "build", "T-1", "--model", "opus", "--limit", "30m")
	if s.Model != "opus" || r.Limit != "30m" {
		t.Fatalf("overrides: %+v %+v", s, r)
	}
	j.fails(3, "", "job", "build", "T-9")
	j.fails(2, "", "job", "build")
	j.fails(2, "", "job", "deploy", "T-1")
	j.fails(2, "", "job", "build", "T-1", "--approved", "abc1234")
}

func TestJobTextJobs(t *testing.T) {
	j := newJobHarness(t, "planner")
	j.h.ok(nil, "create", "--title", "a")

	s, r := j.job("goal demo: ship the login page\n", "plan")
	got := j.seen(r.ID)
	if s.Model != "opus" || got.stdin[0] != "/maestro-planner goal demo: ship the login page" || r.Ticket != "" {
		t.Fatalf("plan: %+v %q", s, got.stdin)
	}
	if !slices.Contains(got.env, "CLAUDE_CODE_PROMPT_CACHE_TTL=5m") {
		t.Errorf("planner runs run no checks: 5-minute cache: %q", got.env)
	}

	s, r = j.job("split it in two\n", "rule", "T-1")
	if got := j.seen(r.ID); s.Model != "opus" || got.stdin[0] != "/maestro-planner rule T-1: split it in two" || r.Ticket != "T-1" {
		t.Fatalf("rule: %+v %q", s, got.stdin)
	}
	s, r = j.job("", "finish", "T-1")
	if got := j.seen(r.ID); s.Model != "sonnet" || got.stdin[0] != "/maestro-planner finish T-1" {
		t.Fatalf("finish: %+v %q", s, got.stdin)
	}
	_, r = j.job("Rows:\njudge: T-1 blocked: x\n", "judge", "T-1")
	if got := j.seen(r.ID); len(got.stdin) != 3 || got.stdin[0] != "/maestro-planner judge T-1" || got.stdin[1] != "Rows:" {
		t.Fatalf("judge: %q", got.stdin)
	}
	_, r = j.job("is T-1 ready?\n", "cos", "--ticket", "T-1")
	if got := j.seen(r.ID); got.stdin[0] != "/maestro-chief-of-staff is T-1 ready?" || r.Ticket != "T-1" {
		t.Fatalf("cos: %q %+v", got.stdin, r)
	}
	_, r = j.job("", "live", "T-1")
	if got := j.seen(r.ID); got.stdin[0] != "/maestro-qa-lead live T-1" {
		t.Fatalf("live: %q", got.stdin)
	}
	for _, st := range []string{"in_progress", "in_review"} {
		j.h.ok(nil, "transition", "T-1", "--to", st)
	}
	_, r = j.job("", "qa", "T-1")
	if got := j.seen(r.ID); got.flag("--model") != "opus" || got.stdin[0] != "/maestro-qa-lead T-1" {
		t.Fatalf("qa runs on Opus: %q", got.args)
	}
	if i := slices.Index(r.Command, "slot"); i < 0 || !slices.Equal(r.Command[i:i+6], []string{"slot", "--name", "qa", "--max", "6", "--"}) {
		t.Fatalf("a QA run waits for one of 6 QA slots: %q", r.Command)
	}
	_, r = j.job("why is the login slow?\n", "qa-ask", "--slots", "2")
	if i := slices.Index(r.Command, "slot"); i < 0 || r.Command[i+4] != "2" {
		t.Fatalf("--slots: %q", r.Command)
	}
	j.fails(2, "", "job", "build", "T-1", "--slots", "2")
	j.fails(4, "", "job", "live", "T-1") // in review: no live run to do
	if out := j.fails(2, "", "job", "plan"); !strings.Contains(out, "stdin") {
		t.Fatalf("plan with no ask: %s", out)
	}
	j.fails(2, "x", "job", "plan", "T-1")
}

func TestJobReview(t *testing.T) {
	j := newJobHarness(t, "reviewer")
	j.h.ok(nil, "create", "--title", "a")
	j.h.ok(nil, "create", "--title", "b")
	for _, id := range []string{"T-1", "T-2"} {
		for _, st := range []string{"in_progress", "in_review"} {
			j.h.ok(nil, "transition", id, "--to", st)
		}
	}
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = j.h.dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	prep := filepath.Join(j.h.dir, ".claude", "skills", "maestro-code-review", "scripts")
	os.MkdirAll(prep, 0o755)
	// The stand-in routes T-1 light with 900 changed lines, T-2 full.
	os.WriteFile(filepath.Join(prep, "review-prep.sh"), []byte(`[ "$1" = --route ] || exit 9
case $2 in
T-1) echo pr=4; echo lines=900; echo route=light; echo "why=2 files, all docs" ;;
T-2) echo pr=5; echo lines=30; echo route=full; echo "why=src/a.go is code" ;;
*) echo "no PR from maestro/$2" >&2; exit 3 ;;
esac
`), 0o755)

	s, r := j.job("", "review", "T-1")
	got := j.seen(r.ID)
	if s.Model != "sonnet" || got.stdin[0] != "/maestro-code-review T-1 --light" || r.Limit != "1h45m" {
		t.Fatalf("light review: %+v %q limit %s", s, got.stdin, r.Limit)
	}
	s, r = j.job("", "review", "T-1", "--full")
	if s.Model != "opus" || j.seen(r.ID).stdin[0] != "/maestro-code-review T-1" {
		t.Fatalf("--full: %+v", s)
	}
	s, r = j.job("F2 was wrong: the test covers it\n", "review", "T-2", "--approved", "abc1234", "--drop", "F2", "--drop-file", "-")
	got = j.seen(r.ID)
	if s.Model != "opus" || r.Limit != "45m" || got.stdin[0] != "/maestro-code-review T-2 --approved abc1234 --drop F2: F2 was wrong: the test covers it" {
		t.Fatalf("full review with a ruling: %+v %q %s", s, got.stdin, r.Limit)
	}
	j.h.ok(nil, "create", "--title", "c")
	for _, st := range []string{"in_progress", "in_review"} {
		j.h.ok(nil, "transition", "T-3", "--to", st)
	}
	j.fails(3, "", "job", "review", "T-3")
	j.fails(4, "", "job", "build", "T-3") // in review: no build to do
	j.fails(2, "", "job", "review", "T-2", "--drop", "F2")
	j.fails(2, "", "job", "review", "T-2", "--approved", "not a sha")
}

func TestResume(t *testing.T) {
	j := newJobHarness(t, "junior-engineer")
	j.h.ok(nil, "create", "--title", "docs", "--tier", "junior")
	_, r := j.job("", "build", "T-1")
	sid := r.SessionID

	s, nr := j.resume("use the v2 endpoint\n", r.ID)
	got := j.seen(nr.ID)
	if got.flag("--resume") != sid || got.flag("--session-id") != "" || got.flag("--model") != "sonnet" || got.stdin[0] != "use the v2 endpoint" {
		t.Fatalf("resume: %q %q", got.args, got.stdin)
	}
	if nr.ResumeOf != r.ID || nr.Job != "build" || nr.Ticket != "T-1" || nr.Limit != r.Limit || s.Model != "sonnet" {
		t.Fatalf("resumed record: %+v (%s)", nr, s.Model)
	}

	if out := j.fails(4, "x", "resume", r.ID); !strings.Contains(out, "resumed already, as "+nr.ID) {
		t.Fatalf("a run resumed twice: %s", out)
	}
	if p := j.pickup(); slices.ContainsFunc(p.Ended, func(x runs.Record) bool { return x.ID == r.ID }) {
		t.Fatalf("a resumed run is acked: pickup still shows it")
	}

	_, or := j.resume("", nr.ID, "--opus")
	got = j.seen(or.ID)
	if got.flag("--model") != "opus" || got.flag("--effort") != "high" || got.stdin[0] != opusText || strings.Count(strings.Join(got.args, " "), "--model") != 1 {
		t.Fatalf("--opus: %q %q", got.args, got.stdin)
	}

	j.fails(4, "", "resume", or.ID, "--died")
	j.fails(2, "", "resume", or.ID)
	dir := filepath.Join(j.h.dir, "runs")
	die := func(x runs.Record) {
		x.State, x.Reply, x.SessionID = runs.Died, "", ""
		if err := runs.Save(dir, x); err != nil {
			t.Fatal(err)
		}
	}
	die(or)
	_, dr := j.resume("", or.ID, "--died")
	if got := j.seen(dr.ID); got.flag("--resume") != sid || got.stdin[0] != diedText || !dr.AfterDeath {
		t.Fatalf("--died takes the session from the command: %q %+v", got.args, dr)
	}
	die(dr)
	if out := j.fails(4, "", "resume", dr.ID, "--died"); !strings.Contains(out, "once") {
		t.Fatalf("a second resume after a death: %s", out)
	}

	other := newJobHarness(t, "senior-engineer")
	other.h = j.h
	other.actor = "senior-engineer"
	other.fails(2, "x", "resume", dr.ID)
}

func TestResumeCommand(t *testing.T) {
	cmd := []string{"env", "A=1", "claude", "-p", "--session-id", "S1", "--model", "sonnet", "--effort=high", "--output-format", "json"}
	got := resumeCommand(cmd, "S1", false)
	want := []string{"env", "A=1", "claude", "-p", "--resume", "S1", "--model", "sonnet", "--effort=high", "--output-format", "json"}
	if !slices.Equal(got, want) {
		t.Fatalf("resume: %q", got)
	}
	got = resumeCommand(got, "S1", true)
	want = []string{"env", "A=1", "claude", "-p", "--resume", "S1", "--model", "opus", "--effort", "high", "--output-format", "json"}
	if !slices.Equal(got, want) {
		t.Fatalf("resume --opus: %q", got)
	}
	if got := reviewLimit(0); got != 45*time.Minute {
		t.Fatal(got)
	}
	if got := reviewLimit(399); got != time.Hour {
		t.Fatal(got)
	}
	if got := reviewLimit(5000); got != 2*time.Hour {
		t.Fatal(got)
	}
}

// The whole path: a job ends, and pickup prints what to send.
func TestPickupRoutesAJob(t *testing.T) {
	j := newJobHarness(t, "junior-engineer")
	j.h.ok(nil, "create", "--title", "docs", "--tier", "junior")
	j.h.ok(nil, "transition", "T-1", "--to", "in_progress")
	_, r := j.job("", "build", "T-1")
	j.h.ok(nil, "transition", "T-1", "--to", "in_review") // as the session would
	out, err := j.cmd("", "pickup").Output()
	if err != nil {
		t.Fatalf("pickup: %v\n%s", err, out)
	}
	want := r.ID + " build done on T-1 (in_review): job: done: " + r.ID + "\nsend reviewer: review T-1\nack each once you have acted on it: maestro-ticket ack " + r.ID + "\n"
	if string(out) != want {
		t.Fatalf("pickup:\n%s\nwant:\n%s", out, want)
	}
}

// Of starts at once, one runs: a job on one ticket, a resume of one run.
func TestJobAndResumeOneAtOnce(t *testing.T) {
	j := newJobHarness(t, "junior-engineer")
	j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
	os.WriteFile(filepath.Join(j.bin, "claude"), []byte(strings.Replace(fakeClaude, "printf '{", "sleep 2; printf '{", 1)), 0o755)
	race := func(args ...string) int {
		var wg sync.WaitGroup
		ok := make(chan bool, 8)
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok <- j.cmd("", args...).Run() == nil
			}()
		}
		wg.Wait()
		close(ok)
		n := 0
		for o := range ok {
			if o {
				n++
			}
		}
		return n
	}
	if n := race("job", "build", "T-1"); n != 1 {
		t.Fatalf("%d of 8 builds started at once; want 1", n)
	}
	var p pickupResult
	deadline := time.Now().Add(20 * time.Second)
	for len(p.Ended) == 0 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		p = j.pickup()
	}
	if n := race("resume", p.Ended[0].ID, "--opus"); n != 1 {
		t.Fatalf("%d of 8 resumes started at once; want 1", n)
	}
}

// job again starts a run's job afresh with its prompt and flags.
func TestJobAgain(t *testing.T) {
	j := newJobHarness(t, "qa-lead")
	j.h.ok(nil, "create", "--title", "a")
	for _, st := range []string{"in_progress", "in_review"} {
		j.h.ok(nil, "transition", "T-1", "--to", st)
	}
	_, r := j.job("", "qa", "T-1", "--slots", "2")
	s, nr := j.job("", "again", r.ID, "--limit", "4h")
	got := j.seen(nr.ID)
	if nr.Limit != "4h" || nr.Job != "qa" || got.stdin[0] != "/maestro-qa-lead T-1" || got.flag("--session-id") == r.SessionID || s.Model != "opus" {
		t.Fatalf("again: %+v %q", nr, got.stdin)
	}
	if i := slices.Index(nr.Command, "slot"); i < 0 || nr.Command[i+4] != "2" {
		t.Fatalf("again keeps --slots: %q", nr.Command)
	}
}

// A resumed build holds the ticket's build as a job does; a resume mark
// a crash left empty is taken over; job again on a resumed run starts
// from the session's first prompt; a second death goes up.
func TestResumeRoundThree(t *testing.T) {
	j := newJobHarness(t, "junior-engineer")
	j.h.ok(nil, "create", "--title", "a", "--tier", "junior")
	_, r := j.job("", "build", "T-1")
	os.WriteFile(filepath.Join(j.bin, "claude"), []byte(strings.Replace(fakeClaude, "printf '{", "sleep 2; printf '{", 1)), 0o755)
	var s started
	out, err := j.cmd("go on\n", "resume", "--json", r.ID).Output()
	if err != nil {
		t.Fatalf("resume: %v %s", err, out)
	}
	json.Unmarshal(out, &s)
	if o := j.fails(4, "", "job", "build", "T-1"); !strings.Contains(o, "already") {
		t.Fatalf("a build beside a resumed one: %s", o)
	}
	nr := j.waitEnded(s.Run.ID, 20*time.Second)
	os.WriteFile(filepath.Join(j.bin, "claude"), []byte(fakeClaude), 0o755)

	// job again from the resumed run: the first prompt, a new session.
	_, ar := j.job("", "again", nr.ID)
	if got := j.seen(ar.ID); got.stdin[0] != "/maestro-software-engineer T-1" || got.flag("--resume") != "" || got.flag("--session-id") == "" {
		t.Fatalf("again on a resumed run: %q %q", got.stdin, got.args)
	}

	// A crash left an empty resume mark an hour ago: it is taken over.
	dir := filepath.Join(j.h.dir, "runs")
	mark := filepath.Join(dir, ar.ID+".resumed")
	os.WriteFile(mark, nil, 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(mark, old, old)
	if _, x := j.resume("go on\n", ar.ID); x.ResumeOf != ar.ID {
		t.Fatalf("stale mark: %+v", x)
	}
}

func TestRouteSecondDeathBySession(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	dir := filepath.Join(h.dir, "runs")
	os.MkdirAll(dir, 0o755)
	// R2 resumed the session after a death; R3 resumed R2 with an answer.
	r2, _ := runs.Create(dir, runs.Record{Launcher: "senior-engineer", Limit: "2h", Command: []string{"claude", "-p", "--resume", "S9"}}, nil)
	r2.State, r2.AfterDeath = runs.Done, true
	runs.Save(dir, r2)
	rt := h.route(runs.Record{ID: "R-zzzzzzzz", Job: "build", Ticket: "T-1", State: runs.Died, Command: []string{"claude", "-p", "--resume", "S9"}})
	if !slices.Equal(sends(rt), []string{"send planner: died twice T-1"}) || len(rt.Do) != 0 {
		t.Fatalf("a second death in one session: %+v", rt)
	}
}

// A run that started before job marked runs (plain launch) holds its
// ticket; a review whose GO stands isn't run again.
func TestJobHonoursOlderRuns(t *testing.T) {
	j := newJobHarness(t, "senior-engineer")
	j.h.ok(nil, "create", "--title", "a")
	os.WriteFile(filepath.Join(j.bin, "claude"), []byte(strings.Replace(fakeClaude, "printf '{", "sleep 2; printf '{", 1)), 0o755)
	c := j.cmd("/maestro-software-engineer T-1\n", "launch", "--ticket", "T-1", "--", "claude", "-p", "--session-id", newSessionID())
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("launch: %v %s", err, out)
	}
	if o := j.fails(4, "", "job", "build", "T-1"); !strings.Contains(o, "already works on T-1") {
		t.Fatalf("a build beside a plain launch: %s", o)
	}
	time.Sleep(2500 * time.Millisecond)
	os.WriteFile(filepath.Join(j.bin, "claude"), []byte(fakeClaude), 0o755)
	for _, st := range []string{"in_progress", "in_review"} {
		j.h.ok(nil, "transition", "T-1", "--to", st)
	}
	if code, out, errs := j.h.run("Review: GO at abc1234", "comment", "T-1", "--body-file", "-"); code != 0 {
		t.Fatal(out, errs)
	}
	if o := j.fails(4, "", "job", "review", "T-1"); !strings.Contains(o, "GO stands") {
		t.Fatalf("a second review of a GO: %s", o)
	}
}

// A session isn't resumed on a ticket that moved on; a run with a do:
// line is left off pickup's ack line, its command acks it.
func TestResumeFitsAndAckLine(t *testing.T) {
	j := newJobHarness(t, "senior-engineer")
	j.h.ok(nil, "create", "--title", "a")
	_, r := j.job("", "build", "T-1")
	for _, st := range []string{"in_progress", "in_review"} {
		j.h.ok(nil, "transition", "T-1", "--to", st)
	}
	if o := j.fails(4, "go on\n", "resume", r.ID); !strings.Contains(o, "is in_review now") || !strings.Contains(o, "maestro-ticket ack "+r.ID) {
		t.Fatalf("resume on a ticket in review: %s", o)
	}
	dir := filepath.Join(j.h.dir, "runs")
	dead := r
	dead.State, dead.Reply = runs.Died, ""
	runs.Save(dir, dead)
	out, _ := j.cmd("", "pickup").Output()
	if strings.Contains(string(out), "maestro-ticket ack "+r.ID) || !strings.Contains(string(out), "do: maestro-ticket resume "+r.ID+" --died") {
		t.Fatalf("pickup:\n%s", out)
	}
}
