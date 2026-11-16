//go:build unix

package cli

// Pins: the bytes bots and sessions read today, held as masked goldens in
// testdata/pins, so a change that moves one shows as a golden diff. Each
// scene builds its own store, so a later test can run the same scene
// under other settings and compare it with the same golden.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// fixedChecks stands in for the store's filesystem checks, whose details
// name the machine's disk.
func fixedChecks(string, bool) []ticket.Check {
	return []ticket.Check{{Name: "filesystem", OK: true, Detail: "local"}}
}

// pinHarness is an in-process harness whose output masks as a golden.
func pinHarness(t *testing.T, actor string) (*harness, *masker) {
	t.Helper()
	h := newHarness(t)
	h.checks = fixedChecks
	if actor != "" {
		h.env["MAESTRO_TICKET_ACTOR"] = actor
	}
	h.ok(nil, "doctor") // makes the store
	return h, newMasker(h.dir, "<DIR>")
}

// pin runs one command in-process and returns it as a golden holds it.
func (h *harness) pin(m *masker, stdin string, args ...string) string {
	h.t.Helper()
	code, out, errs := h.run(stdin, args...)
	return m.mask(result(args, stdin, code, out, errs))
}

// pinBase is the start of a scene's run times: whole seconds, hours ago,
// so each run's "after" reads the same on every test run.
func pinBase() time.Time { return time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second) }

// putRun writes a run as its supervisor would have left it, its fields
// from r; it returns the record with its new id.
func putRun(t *testing.T, dir string, r runs.Record) runs.Record {
	t.Helper()
	c, err := runs.Create(dir, r, []byte("prompt\n"))
	if err != nil {
		t.Fatal(err)
	}
	r.ID, r.Host = c.ID, c.Host
	if err := runs.Save(dir, r); err != nil {
		t.Fatal(err)
	}
	return r
}

// holdRun holds a run's lock as a live supervisor does, until the test
// ends.
func holdRun(t *testing.T, dir, id string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, id+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
}

func ended(start time.Time, d time.Duration) *time.Time {
	e := start.Add(d)
	return &e
}

func exitCode(n int) *int { return &n }

// endedScene is a store with one run of each way a run ends, for the
// actor senior-engineer: a build done and one failed, stopped, timed out
// and died with no supervisor to say so; a plain launch done; a build
// still running; and a reviewer's run, which isn't the actor's.
func endedScene(t *testing.T, h *harness) (dir string, ids map[string]string) {
	t.Helper()
	const actor = "senior-engineer"
	for _, s := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		h.ok(nil, "create", "--title", s)
	}
	for _, id := range []string{"T-1", "T-2", "T-3", "T-4", "T-5", "T-7"} {
		h.ok(nil, "transition", id, "--to", "in_progress")
	}
	h.ok(nil, "transition", "T-1", "--to", "in_review")
	h.ok(nil, "transition", "T-7", "--to", "in_review")
	dir = filepath.Join(h.dir, "runs")
	base := pinBase()
	cmd := func(session, model string) []string { return withEnv(claudeCommand(session, model, effortHigh, false)) }
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Minute) }
	ids = map[string]string{}
	put := func(name string, r runs.Record) {
		if r.Launcher == "" {
			r.Launcher = actor
		}
		r.Dir, r.Limit = h.dir, "2h"
		ids[name] = putRun(t, dir, r).ID
		writeSide(dir, ids[name], &sideFacts{}) // as a start with stats on writes it
	}
	put("done", runs.Record{Ticket: "T-1", Job: "build", Command: cmd("11111111-1111-4111-8111-111111111111", modelOpus),
		StartedAt: at(0), EndedAt: ended(at(0), 90*time.Second), State: runs.Done, PID: 4001, Exit: exitCode(0),
		SessionID: "11111111-1111-4111-8111-111111111111", Reply: "build: done: T-1 at abc1234\nPR #4 is open.", Rang: true, Rings: 1})
	put("failed", runs.Record{Ticket: "T-2", Job: "build", Command: cmd("22222222-2222-4222-8222-222222222222", modelOpus),
		StartedAt: at(1), EndedAt: ended(at(1), 2*time.Second), State: runs.Failed, PID: 4002, Exit: exitCode(1),
		Error: "Error: something went wrong", Rang: true, Rings: 1})
	put("stopped", runs.Record{Ticket: "T-3", Job: "build", Command: cmd("33333333-3333-4333-8333-333333333333", modelOpus),
		StartedAt: at(2), EndedAt: ended(at(2), 5*time.Minute), State: runs.Failed, PID: 4003, Exit: exitCode(143),
		SessionID: "33333333-3333-4333-8333-333333333333",
		Error:     "the launcher was stopped, so it stopped the run", Rang: true, Rings: 1})
	put("timed_out", runs.Record{Ticket: "T-4", Job: "build", Command: cmd("44444444-4444-4444-8444-444444444444", modelOpus),
		StartedAt: at(3), EndedAt: ended(at(3), 2*time.Hour), State: runs.TimedOut, PID: 4004, Exit: exitCode(143),
		Error: "the 2h time limit ended it", Rang: true, Rings: 1})
	put("died", runs.Record{Ticket: "T-5", Job: "build", Command: cmd("55555555-5555-4555-8555-555555555555", modelOpus),
		StartedAt: at(4), State: runs.Running, PID: 4005})
	put("launch", runs.Record{Command: []string{"claude", "-p", "--output-format", "json"},
		StartedAt: at(5), EndedAt: ended(at(5), 30*time.Second), State: runs.Done, PID: 4006, Exit: exitCode(0),
		SessionID: "66666666-6666-4666-8666-666666666666", Reply: "hello", Rang: true, Rings: 1})
	put("running", runs.Record{Ticket: "T-6", Job: "build", Command: cmd("77777777-7777-4777-8777-777777777777", modelOpus),
		StartedAt: at(6), State: runs.Running, PID: 4007})
	holdRun(t, dir, ids["running"])
	put("other", runs.Record{Launcher: "reviewer", Ticket: "T-7", Job: "review", Command: cmd("88888888-8888-4888-8888-888888888888", modelOpus),
		StartedAt: at(7), EndedAt: ended(at(7), 10*time.Minute), State: runs.Done, PID: 4008, Exit: exitCode(0),
		SessionID: "88888888-8888-4888-8888-888888888888", Reply: "Review: GO at abc1234", Rang: true, Rings: 1})
	return dir, ids
}

// pinPickup is pickup's scene: each way a run ends, as the bot reads it.
func pinPickup(t *testing.T, setup func(*harness)) map[string]string {
	out := map[string]string{}
	for name, args := range map[string][]string{
		"pickup":      {"pickup"},
		"pickup_json": {"pickup", "--json"},
		"pickup_full": {"pickup", "--full"},
	} {
		h, m := pinHarness(t, "senior-engineer")
		if setup != nil {
			setup(h)
		}
		endedScene(t, h)
		got := h.pin(m, "", args...)
		// A second pickup routes as the first, and the run that died is
		// rung for only once.
		got += h.pin(m, "", args...)
		out[name] = got
	}
	return out
}

func TestPinPickup(t *testing.T) {
	for name, got := range pinPickup(t, nil) {
		golden(t, name, got)
	}
}

// pinDoctor is doctor's scene: the runs check frees the run that died and
// flags the ones waiting for an ack.
func pinDoctor(t *testing.T, setup func(*harness)) string {
	var b strings.Builder
	for _, args := range [][]string{{"doctor"}, {"doctor", "--json"}} {
		h, m := pinHarness(t, "senior-engineer")
		if setup != nil {
			setup(h)
		}
		endedScene(t, h)
		b.WriteString(h.pin(m, "", args...))
	}
	return b.String()
}

func TestPinDoctor(t *testing.T) { golden(t, "doctor", pinDoctor(t, nil)) }

// tableGap is a run of spaces a table pads a column with: its width
// follows the elapsed times, which move.
var tableGap = regexp.MustCompile(`  +`)

// pinRuns is runs' scene: the list, one run, and the refusals.
func pinRuns(t *testing.T, setup func(*harness)) string {
	h, m := pinHarness(t, "senior-engineer")
	if setup != nil {
		setup(h)
	}
	dir, ids := endedScene(t, h)
	runs.Ack(dir, ids["launch"], "senior-engineer", time.Now())
	var b strings.Builder
	for _, args := range [][]string{
		{"runs"}, {"runs", "--all"}, {"runs", "--launcher", "reviewer"},
	} {
		code, out, errs := h.run("", args...)
		b.WriteString(m.mask(result(args, "", code, tableGap.ReplaceAllString(maskTable(out), "  "), errs)))
	}
	for _, args := range [][]string{
		{"runs", "--json"}, {"runs", "--all", "--json"},
		{"runs", ids["done"]}, {"runs", ids["done"], "--json"},
		{"runs", ids["died"]}, {"runs", ids["running"], "--json"},
		{"runs", "R-zzzzzzzz"}, {"runs", "R-zzzzzzzz", "--json"},
		{"runs", "nope"}, {"runs", ids["done"], ids["failed"]},
	} {
		b.WriteString(h.pin(m, "", args...))
	}
	return b.String()
}

// maskTable masks the FOR column of the runs table, the time each run
// took or has run so far.
func maskTable(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) >= 6 && strings.HasPrefix(f[0], "R-") {
			f[4] = "D"
			lines[i] = strings.Join(f, "  ")
		}
	}
	return strings.Join(lines, "\n")
}

func TestPinRuns(t *testing.T) { golden(t, "runs", pinRuns(t, nil)) }

// pinAck is ack's scene: an ack, a second one, and each refusal.
func pinAck(t *testing.T, setup func(*harness)) string {
	var b strings.Builder
	for _, json := range []bool{false, true} {
		h, m := pinHarness(t, "senior-engineer")
		if setup != nil {
			setup(h)
		}
		_, ids := endedScene(t, h)
		for _, args := range [][]string{
			{"ack", ids["done"], ids["failed"]},
			{"ack", ids["done"]},
			{"ack", ids["running"]},
			{"ack", ids["other"]},
			{"ack", "R-zzzzzzzz"},
			{"ack", "nope"},
			{"ack"},
		} {
			if json {
				args = append(args, "--json")
			}
			b.WriteString(h.pin(m, "", args...))
		}
	}
	return b.String()
}

func TestPinAck(t *testing.T) { golden(t, "ack", pinAck(t, nil)) }

// pinCosts is costs' scene: logged runs on tickets of two goals and none,
// by three bots, grouped each way.
func pinCosts(t *testing.T, setup func(*harness)) string {
	h, m := pinHarness(t, "")
	if setup != nil {
		setup(h)
	}
	h.ok(nil, "create", "--title", "a", "--goal", "login")
	h.ok(nil, "create", "--title", "b", "--goal", "login")
	h.ok(nil, "create", "--title", "c", "--goal", "billing")
	for _, st := range []string{"in_progress", "in_review", "in_progress"} {
		h.ok(nil, "transition", "T-2", "--to", st)
	}
	var b strings.Builder
	for _, l := range []struct {
		actor, ticket, session string
		usd                    float64
	}{
		{"junior-engineer", "T-1", "s1", 1.25},
		{"senior-engineer", "T-2", "s2", 3.5},
		{"senior-engineer", "T-2", "s3", 0.75},
		{"reviewer", "T-3", "s4", 0.4},
		{"planner", "", "s5", 0.2},
		{"junior-engineer", "T-1", "s1", 2}, // a resume replaces its total
	} {
		args := []string{"log-run", "--actor", l.actor}
		if l.ticket != "" {
			args = append(args, "--ticket", l.ticket)
		}
		b.WriteString(h.pin(m, reply(l.session, l.usd, "claude-sonnet-5-5"), args...))
	}
	for _, args := range [][]string{
		{"costs"}, {"costs", "--json"},
		{"costs", "--by", "actor"}, {"costs", "--by", "ticket"}, {"costs", "--by", "goal"},
		{"costs", "--by", "origin"}, {"costs", "--by", "tries"}, {"costs", "--by", "tries", "--json"},
		{"costs", "--ticket", "T-2"}, {"costs", "--since", "72h"},
		{"costs", "--by", "model"}, {"costs", "--by", "model", "--json"},
		{"costs", "--since", "yesterday"},
	} {
		b.WriteString(h.pin(m, "", args...))
	}
	return b.String()
}

func TestPinCosts(t *testing.T) { golden(t, "costs", pinCosts(t, nil)) }

// TestPinUsage pins the usage text and the help of the commands a bot
// or a session reads it for.
func TestPinUsage(t *testing.T) {
	h, m := pinHarness(t, "")
	var b strings.Builder
	for _, args := range [][]string{
		{}, {"help"}, {"--help"}, {"-h"}, {"frobnicate"}, {"--json", "list"},
		{"costs", "--help"}, {"pickup", "--help"}, {"job", "--help"}, {"resume", "--help"},
		{"ack", "--help"}, {"runs", "--help"}, {"look", "--help"}, {"check", "--help"},
	} {
		b.WriteString(h.pin(m, "", args...))
	}
	golden(t, "usage", b.String())
}

// checkMoves are what a carried line holds that moves between test runs:
// the head's sha, with the commit's time in it, and how long a pass took.
var (
	checkHead = regexp.MustCompile(`\(head [0-9a-f]{12}\)`)
	checkTook = regexp.MustCompile(`, in [0-9.]+[0-9.hmsµ]*;`)
)

// pinCheck is check's scene: a pass, a pass carried, and a failure.
func pinCheck(t *testing.T, setup func(map[string]string)) string {
	root, env := checkRepo(t)
	if setup != nil {
		setup(env)
	}
	m := newMasker(root, "<REPO>", filepath.Dir(env["MAESTRO_TICKET_STORE"]), "<STORE>")
	good, bad := filepath.Join(root, "good"), filepath.Join(root, "bad")
	var b strings.Builder
	for _, c := range []struct {
		dir  string
		args []string
	}{
		{good, append([]string{"test"}, okCmd...)},
		{good, append([]string{"test"}, okCmd...)},
		{good, append([]string{"test", "--json"}, okCmd...)},
		{bad, append([]string{"test"}, okCmd...)},
		{good, append([]string{"test", "--fresh"}, okCmd...)},
		{good, []string{"test"}},
		{good, append([]string{"Bad Name"}, okCmd...)},
	} {
		var out, errb strings.Builder
		code := Run(t.Context(), append([]string{"check"}, c.args...), Env{
			Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
			Getenv: func(k string) string { return env[k] }, Dir: c.dir, DiskFree: halfFree,
		})
		got := m.mask(result(append([]string{"check"}, c.args...), "", code, out.String(), errb.String()))
		got = checkHead.ReplaceAllString(got, "(head H)")
		b.WriteString(checkTook.ReplaceAllString(got, ", in D;"))
	}
	return b.String()
}

func TestPinCheck(t *testing.T) { golden(t, "check", pinCheck(t, nil)) }

// gatedClaude is fakeClaude held until the test makes $FAKE_OUT/<run>.go,
// so a start prints its run as running every time.
var gatedClaude = strings.Replace(fakeClaude, "printf '{",
	`i=0; while [ ! -e "$FAKE_OUT/$MAESTRO_RUN_ID.go" ] && [ $i -lt 600 ]; do sleep 0.05; i=$((i+1)); done
printf '{`, 1)

func newPinJobs(t *testing.T, actor string) (*jobHarness, *masker) {
	t.Helper()
	j := newJobHarness(t, actor)
	j.h.checks = fixedChecks
	if err := os.WriteFile(filepath.Join(j.bin, "claude"), []byte(gatedClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	m := newMasker(j.h.dir, "<DIR>", j.exe, "<exe>", j.bin, "<bin>", j.out, "<out>")
	return j, m
}

// start runs a command that starts a run, lets the run end, and returns
// the command as a golden holds it, with the run's id.
func (j *jobHarness) start(m *masker, stdin string, args ...string) (string, string) {
	j.t.Helper()
	got, out := j.pinCmd(m, stdin, args...)
	id := maskRunID.FindString(out) // text: the started run comes first
	if m := startedID.FindStringSubmatch(out); m != nil {
		id = m[1] // JSON: the run's own id, not an ended one's
	}
	if id == "" {
		j.t.Fatalf("%v started no run:\n%s", args, got)
	}
	os.WriteFile(filepath.Join(j.out, id+".go"), nil, 0o644)
	j.waitEnded(id, 20*time.Second)
	return got, id
}

var startedID = regexp.MustCompile(`"id": "(R-[a-z2-7]{8})"`)

// pinCmd runs maestro-ticket as its own process, as a bot does.
func (j *jobHarness) pinCmd(m *masker, stdin string, args ...string) (got, stdout string) {
	j.t.Helper()
	c := j.cmd(stdin, args...)
	var out, errb strings.Builder
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	code := 0
	if err != nil {
		code = c.ProcessState.ExitCode()
		if code < 0 {
			j.t.Fatalf("%v: %v", args, err)
		}
	}
	return m.mask(result(args, stdin, code, out.String(), errb.String())), out.String()
}

// TestPinJobStart pins what a job prints as it starts its run, as text
// and as JSON, with the line for an earlier run not yet acked.
func TestPinJobStart(t *testing.T) {
	t.Parallel() // its runs go one at a time; it overlaps the other run pins
	golden(t, "job_start", pinJobStart(t, nil))
}

func pinJobStart(t *testing.T, setup func(*jobHarness)) string {
	j, m := newPinJobs(t, "junior-engineer")
	if setup != nil {
		setup(j)
	}
	j.h.ok(nil, "create", "--title", "docs", "--tier", "junior")
	j.h.ok(nil, "create", "--title", "auth")
	var b strings.Builder
	got, _ := j.start(m, "", "job", "build", "T-1")
	b.WriteString(got)
	got, _ = j.start(m, "", "job", "build", "T-2", "--json")
	b.WriteString(got)
	got, _ = j.start(m, "", "job", "build", "T-1", "--model", "opus", "--limit", "30m")
	b.WriteString(got)
	return b.String()
}

// TestPinJobAgain pins job again, as text and as JSON.
func TestPinJobAgain(t *testing.T) {
	t.Parallel() // its runs go one at a time; it overlaps the other run pins
	golden(t, "job_again", pinJobAgain(t, nil))
}

func pinJobAgain(t *testing.T, setup func(*jobHarness)) string {
	j, m := newPinJobs(t, "qa-lead")
	if setup != nil {
		setup(j)
	}
	j.h.ok(nil, "create", "--title", "a")
	for _, st := range []string{"in_progress", "in_review"} {
		j.h.ok(nil, "transition", "T-1", "--to", st)
	}
	var b strings.Builder
	got, first := j.start(m, "", "job", "qa", "T-1", "--slots", "2")
	b.WriteString(got)
	got, second := j.start(m, "", "job", "again", first, "--limit", "4h")
	b.WriteString(got)
	got, _ = j.start(m, "", "job", "again", second, "--json")
	b.WriteString(got)
	return b.String()
}

// TestPinResume pins resume with an answer and with each of its flags.
func TestPinResume(t *testing.T) {
	t.Parallel() // its runs go one at a time; it overlaps the other run pins
	golden(t, "resume", pinResume(t, nil))
}

func pinResume(t *testing.T, setup func(*jobHarness)) string {
	j, m := newPinJobs(t, "junior-engineer")
	if setup != nil {
		setup(j)
	}
	j.h.ok(nil, "create", "--title", "docs", "--tier", "junior")
	dir := filepath.Join(j.h.dir, "runs")
	var b strings.Builder
	got, id := j.start(m, "", "job", "build", "T-1")
	b.WriteString(got)
	for _, c := range []struct {
		stdin string
		flags []string
	}{
		{"use the v2 endpoint\n", nil},
		{"", []string{"--opus", "--json"}},
		{"", []string{"--escalate"}},
		{"", []string{"--decide"}},
		{"", []string{"--died"}},
		{"", []string{"--go-on"}},
	} {
		if slices.Contains(c.flags, "--died") {
			r, err := runs.Load(dir, id)
			if err != nil {
				t.Fatal(err)
			}
			r.State, r.Reply, r.SessionID = runs.Died, "", ""
			runs.Save(dir, r)
		}
		got, id = j.start(m, c.stdin, append([]string{"resume", id}, c.flags...)...)
		b.WriteString(got)
	}
	// Each nudge comes once a session.
	got, _ = j.pinCmd(m, "", "resume", id, "--opus")
	b.WriteString(got)
	return b.String()
}

// TestPinJobRefusals pins each refusal of job, job again and resume, as
// text and as JSON.
func TestPinJobRefusals(t *testing.T) {
	var b strings.Builder
	for _, json := range []bool{false, true} {
		h, m := pinHarness(t, "senior-engineer")
		dir, ids := endedScene(t, h)
		h.ok(nil, "create", "--title", "h") // T-8: a plain launch works on it
		h.ok(nil, "create", "--title", "i") // T-9: free
		h.run("Review: GO at abc1234", "comment", "T-7", "--body-file", "-")
		live := putRun(t, dir, runs.Record{Launcher: "qa-lead", Ticket: "T-8", Command: []string{"claude", "-p"},
			Dir: h.dir, Limit: "2h", StartedAt: pinBase().Add(time.Hour), State: runs.Running, PID: 4010})
		holdRun(t, dir, live.ID)
		cases := []struct {
			stdin string
			args  []string
		}{
			{"", []string{"job"}},
			{"", []string{"job", "deploy", "T-1"}},
			{"", []string{"job", "build"}},
			{"", []string{"job", "build", "T-1", "T-2"}},
			{"", []string{"job", "build", "T-99"}},
			{"", []string{"job", "build", "nope"}},
			{"", []string{"job", "build", "T-1", "--slots", "2"}},
			{"", []string{"job", "qa", "T-7", "--slots", "-1"}},
			{"", []string{"job", "build", "T-1", "--approved", "abc1234"}},
			{"", []string{"job", "review", "T-1", "--drop", "F2"}},
			{"", []string{"job", "review", "T-1", "--approved", "not-a-sha"}},
			{"", []string{"job", "review", "T-1", "--drop", "F 2", "--drop-file", "-"}},
			{"", []string{"job", "plan"}},
			{"x", []string{"job", "plan", "T-1"}},
			{"x", []string{"job", "judge", "T-1", "--ticket", "T-2"}},
			{"", []string{"job", "judge"}},
			{"", []string{"job", "live", "T-1"}},
			{"", []string{"job", "build", "T-1"}},
			{"", []string{"job", "review", "T-7"}},
			{"", []string{"job", "build", "T-8"}},
			{"", []string{"job", "build", "T-9", "--limit", "25h"}},
			{"", []string{"job", "build", "T-6"}},
			{"", []string{"job", "again"}},
			{"", []string{"job", "again", "R-zzzzzzzz"}},
			{"", []string{"job", "again", ids["other"]}},
			{"", []string{"job", "again", ids["launch"]}},
			{"", []string{"job", "again", ids["running"]}},
			{"", []string{"resume"}},
			{"", []string{"resume", "nope"}},
			{"", []string{"resume", "R-zzzzzzzz"}},
			{"", []string{"resume", ids["other"]}},
			{"", []string{"resume", ids["running"]}},
			{"", []string{"resume", ids["done"], "--died"}},
			{"", []string{"resume", ids["failed"]}},
			{"go on", []string{"resume", ids["done"]}},
		}
		for _, c := range cases {
			args := c.args
			if json {
				args = append(slices.Clone(args), "--json")
			}
			b.WriteString(h.pin(m, c.stdin, args...))
		}
		// No actor; STOP; a usage limit's pause.
		delete(h.env, "MAESTRO_TICKET_ACTOR")
		b.WriteString(h.pin(m, "", appendJSON(json, "job", "build", "T-9")...))
		h.env["MAESTRO_TICKET_ACTOR"] = "senior-engineer"
		os.WriteFile(filepath.Join(h.dir, "STOP"), nil, 0o644)
		b.WriteString(h.pin(m, "", appendJSON(json, "job", "build", "T-9")...))
		os.Remove(filepath.Join(h.dir, "STOP"))
		setPause(h.dir, pause{Until: time.Now().Add(time.Hour), Why: "usage limit"}, time.Now())
		b.WriteString(h.pin(m, "", appendJSON(json, "job", "build", "T-9")...))
		b.WriteString(h.pin(m, "x", appendJSON(json, "resume", ids["failed"])...))
	}
	golden(t, "job_refusals", b.String())
}

func appendJSON(json bool, args ...string) []string {
	if json {
		return append(args, "--json")
	}
	return args
}

// lookBoard is TestLookRows' board: one ticket for each row look prints.
func lookBoard(l *lookHarness) {
	mk := func(args ...string) { l.h.ok(nil, append([]string{"create", "--title", "x"}, args...)...) }
	for range 13 {
		mk()
	}
	l.h.ok(nil, "update", "T-7", "--tier", "junior")
	l.move("T-1", "in_progress", "in_review")
	l.move("T-2", "in_progress", "in_review")
	l.comment("T-2", "Review: GO at "+sha)
	l.claimed("T-3", "junior-engineer")
	l.move("T-3", "in_progress", "in_review", "in_progress")
	l.comment("T-3", "Kickback: at abc1234")
	l.move("T-4", "in_progress", "in_review", "in_progress", "in_review", "in_progress")
	l.move("T-4", "in_review", "in_progress")
	l.comment("T-4", "Kickback: at abc1231\nF1")
	l.comment("T-4", "Kickback: at abc1232\nF2")
	l.move("T-5", "blocked")
	l.comment("T-5", "Escalation: build at abc: which cache key?\nReturn-role: engineer")
	l.move("T-6", "blocked")
	l.comment("T-6", "Sent up: chief of staff: pay for Postmark?")
	l.h.ok(nil, "transition", "T-10", "--to", "backlog", "--parked", "hold")
	l.move("T-11", "blocked")
	l.comment("T-11", "Answer: split it")
	l.move("T-12", "in_progress", "in_review")
	l.comment("T-12", "Split into T-1")
	l.claim("T-13", "senior-engineer/s9")
}

// TestPinLook pins the planner's look: its rows, the runs it starts, a
// second look, the judge, an empty board and STOP.
func TestPinLook(t *testing.T) {
	t.Parallel() // its runs go one at a time; it overlaps the other run pins
	golden(t, "look", pinLook(t, nil))
}

func pinLook(t *testing.T, setup func(*jobHarness)) string {
	var b strings.Builder
	for _, args := range [][]string{
		{"look", "--limit", "3", "--report"},
		{"look", "--limit", "3", "--json"},
	} {
		l := &lookHarness{newJobHarness(t, "planner")}
		l.h.checks = fixedChecks
		if setup != nil {
			setup(l.jobHarness)
		}
		m := newMasker(l.h.dir, "<DIR>", l.exe, "<exe>")
		lookBoard(l)
		got, _ := l.pinCmd(m, "", args...)
		b.WriteString(got)
		got, _ = l.pinCmd(m, "", "look", "--limit", "3")
		b.WriteString(got)
		got, _ = l.pinCmd(m, "rule: senior only\n", "look", "--limit", "3", "--judge")
		b.WriteString(got)
		os.WriteFile(filepath.Join(l.h.dir, "STOP"), nil, 0o644)
		got, _ = l.pinCmd(m, "", "look", "--limit", "3")
		b.WriteString(got)
	}
	l := &lookHarness{newJobHarness(t, "planner")}
	if setup != nil {
		setup(l.jobHarness)
	}
	m := newMasker(l.h.dir, "<DIR>", l.exe, "<exe>")
	got, _ := l.pinCmd(m, "", "look", "--limit", "4")
	b.WriteString(got)
	got, _ = l.pinCmd(m, "", "look")
	b.WriteString(got)
	return b.String()
}
