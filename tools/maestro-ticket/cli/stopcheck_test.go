package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/sqlitestore"
)

// hookCommand is the one Stop hook command in cmd's --settings, failing
// the test unless cmd has exactly one --settings that parses.
func hookCommand(t *testing.T, cmd []string) string {
	t.Helper()
	n := 0
	for _, w := range cmd {
		if w == "--settings" || strings.HasPrefix(w, "--settings=") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d --settings in %q; want 1", n, cmd)
	}
	var s struct {
		Hooks struct {
			Stop []struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
					Timeout int    `json:"timeout"`
				} `json:"hooks"`
			} `json:"Stop"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(flagValue(cmd, "--settings")), &s); err != nil {
		t.Fatalf("--settings: %v", err)
	}
	if len(s.Hooks.Stop) != 1 || len(s.Hooks.Stop[0].Hooks) != 1 {
		t.Fatalf("hooks: %+v", s)
	}
	h := s.Hooks.Stop[0].Hooks[0]
	if h.Type != "command" || h.Timeout != stopHookTimeout {
		t.Fatalf("hook: %+v", h)
	}
	return h.Command
}

// stopScene is a store with T-1 and a run of job on it that started a
// minute ago in session S1, as its supervisor saved it.
type stopScene struct {
	h   *harness
	dir string
	r   runs.Record
}

func newStopScene(t *testing.T, job, model string) *stopScene {
	t.Helper()
	h := newHarness(t)
	h.env["MAESTRO_TICKET_ACTOR"] = "planner"
	h.ok(nil, "create", "--title", "a")
	s := &stopScene{h: h, dir: filepath.Join(h.dir, "runs")}
	s.r = s.put(runs.Record{Job: job, Command: claudeCommand("S1", model, effortHigh, false), StartedAt: time.Now().Add(-time.Minute)})
	return s
}

func (s *stopScene) put(r runs.Record) runs.Record {
	s.h.t.Helper()
	if r.Launcher == "" {
		r.Launcher = "senior-engineer"
	}
	if r.Ticket == "" {
		r.Ticket = "T-1"
	}
	if r.Limit == "" {
		r.Limit = "2h"
	}
	r.State = runs.Running
	return putRun(s.h.t, s.dir, r)
}

// stop runs stop-check as the hook does, for run id, and reports whether
// it blocked.
func (s *stopScene) stop(id, input string) bool {
	s.h.t.Helper()
	s.h.env[runs.RunIDEnv] = id
	code, out, errs := s.h.run(input, "stop-check", "--store", s.h.env["MAESTRO_TICKET_STORE"])
	if code != 0 || errs != "" {
		s.h.t.Fatalf("stop-check: exit %d: %s %s", code, out, errs)
	}
	if out == "" {
		return false
	}
	var b struct{ Decision, Reason string }
	if err := json.Unmarshal([]byte(out), &b); err != nil || b.Decision != "block" || !strings.Contains(b.Reason, "post your note") {
		s.h.t.Fatalf("stop-check printed %q", out)
	}
	return true
}

func stopIn(session, msg string, active bool) string {
	b, _ := json.Marshal(map[string]any{"session_id": session, "stop_hook_active": active, "last_assistant_message": msg, "hook_event_name": "Stop"})
	return string(b)
}

// Each end its job's route reads lets the session end; an end with
// nothing to show is blocked.
func TestStopCheckEnds(t *testing.T) {
	for _, c := range []struct {
		name, job, model string
		setup            func(s *stopScene)
		msg              string
		block            bool
	}{
		{"build, no note", "build", modelOpus, inProgress, "done", true},
		{"build Handoff:", "build", modelOpus, note("Handoff: built at abc1234"), "done", false},
		{"build Escalation:", "build", modelOpus, note("Escalation: build: which API?"), "done", false},
		{"build in review", "build", modelOpus, stopMove("in_progress", "in_review"), "done", false},
		{"build refused", "build", modelOpus, inProgress, "build: refused: T-1 is claimed", false},
		{"Sonnet build asked", "build", modelSonnet, inProgress, "I stopped.\nWhich endpoint?", false},
		{"Opus build asked", "build", modelOpus, inProgress, "I stopped.\nWhich endpoint?", true},
		{"build note before its session", "build", modelOpus, nil, "done", true},
		{"review, no verdict", "review", modelOpus, stopMove("in_progress", "in_review"), "done", true},
		{"review GO", "review", modelOpus, inReviewWith("Review: GO at abc1234"), "done", false},
		{"review Kickback:", "review", modelOpus, inReviewWith("Kickback: review at abc1234"), "done", false},
		{"review held by another", "review", modelOpus, inReviewClaimed("qa-lead/abc"), "done", false},
		{"review own claim", "review", modelOpus, inReviewClaimed("senior-engineer/abc"), "done", true},
		{"review needs full", "review", modelSonnet, stopMove("in_progress", "in_review"), "review: stopped: tier 2 needs the full model at abc1234", false},
		{"review report only", "review", modelOpus, stopMove("in_progress", "in_review"), "REPORT-ONLY: NO-GO at abc1234", false},
		{"review out of review", "review", modelOpus, inProgress, "done", false},
		{"qa, no verdict", "qa", modelOpus, stopMove("in_progress", "in_review"), "done", true},
		{"qa pass", "qa", modelOpus, inReviewWith("QA: pass at abc1234"), "done", false},
		{"qa fail", "qa", modelOpus, inReviewWith("Kickback: QA fail at abc1234"), "done", false},
		{"qa Rereview:", "qa", modelOpus, inReviewWith("Rereview: QA at abc1234: review passed none"), "done", false},
		{"qa refused", "qa", modelOpus, stopMove("in_progress", "in_review"), "test: refused: T-1 at abc1234", false},
		{"qa blocked", "qa", modelOpus, stopMove("in_progress", "in_review"), "test: blocked: T-1 at abc1234", false},
		{"live, no verdict", "live", modelOpus, nil, "done", true},
		{"live verdict", "live", modelOpus, note("Live run: fail at abc1234"), "done", false},
		{"live blocked", "live", modelOpus, nil, "live: blocked: T-1 at none", false},
		{"design, no note", "design", modelOpus, inProgress, "done", true},
		{"design ready", "design", modelOpus, note("Design spec: x"), "done", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newStopScene(t, c.job, c.model)
			if c.name == "build note before its session" {
				s.h.ok(nil, "comment", "T-1", "--body", "Handoff: an older build's")
				s.r.StartedAt = time.Now().Add(time.Second)
				runs.Save(s.dir, s.r)
			}
			if c.setup != nil {
				c.setup(s)
			}
			if got := s.stop(s.r.ID, stopIn("S1", c.msg, false)); got != c.block {
				t.Fatalf("blocked %v; want %v", got, c.block)
			}
		})
	}
}

func inProgress(s *stopScene) { s.h.ok(nil, "transition", "T-1", "--to", "in_progress") }

func note(body string) func(*stopScene) {
	return func(s *stopScene) { s.h.ok(nil, "comment", "T-1", "--body", body) }
}

func stopMove(to ...string) func(*stopScene) {
	return func(s *stopScene) {
		for _, st := range to {
			s.h.ok(nil, "transition", "T-1", "--to", st)
		}
	}
}

func inReviewWith(body string) func(*stopScene) {
	return func(s *stopScene) {
		stopMove("in_progress", "in_review")(s)
		note(body)(s)
	}
}

func inReviewClaimed(holder string) func(*stopScene) {
	return func(s *stopScene) {
		stopMove("in_progress", "in_review")(s)
		s.h.ok(nil, "claim", "T-1", "--holder", holder, "--status", "in_review")
	}
}

// A resumed session's note from its first run counts: the session began
// there.
func TestStopCheckWalksResumes(t *testing.T) {
	s := newStopScene(t, "build", modelOpus)
	inProgress(s)
	note("Escalation: build: which API?")(s)
	res := s.put(runs.Record{Job: "build", ResumeOf: s.r.ID, StartedAt: time.Now().Add(time.Second),
		Command: resumeCommand(s.r.Command, "S1", false)})
	if s.stop(res.ID, stopIn("S1", "done", false)) {
		t.Fatal("a resume whose session posted its note was blocked")
	}
	alone := s.put(runs.Record{Job: "build", StartedAt: time.Now().Add(time.Second), Command: claudeCommand("S2", modelOpus, effortHigh, false)})
	if !s.stop(alone.ID, stopIn("S2", "done", false)) {
		t.Fatal("a new session went by an older session's note")
	}
}

// The hook fails open: every error, and every end that is no fault of
// the session's, lets it end and prints nothing.
func TestStopCheckFailsOpen(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(s *stopScene) (id, input string)
	}{
		{"no run id", func(s *stopScene) (string, string) { return "", stopIn("S1", "done", false) }},
		{"bad run id", func(s *stopScene) (string, string) { return "nonsense", stopIn("S1", "done", false) }},
		{"no such run", func(s *stopScene) (string, string) { return "R-aaaaaaaa", stopIn("S1", "done", false) }},
		{"bad input", func(s *stopScene) (string, string) { return s.r.ID, "{not json" }},
		{"no input", func(s *stopScene) (string, string) { return s.r.ID, "" }},
		{"another session", func(s *stopScene) (string, string) { return s.r.ID, stopIn("S9", "done", false) }},
		{"no session", func(s *stopScene) (string, string) { return s.r.ID, stopIn("", "done", false) }},
		{"stop hook active", func(s *stopScene) (string, string) { return s.r.ID, stopIn("S1", "done", true) }},
		{"asked to stop", func(s *stopScene) (string, string) {
			runs.AskStop(s.dir, s.r.ID, "planner", time.Now())
			return s.r.ID, stopIn("S1", "done", false)
		}},
		{"STOP", func(s *stopScene) (string, string) {
			os.WriteFile(filepath.Join(s.h.dir, sqlitestore.StopFile), nil, 0o644)
			return s.r.ID, stopIn("S1", "done", false)
		}},
		{"paused", func(s *stopScene) (string, string) {
			setPause(s.h.dir, pause{Until: time.Now().Add(time.Hour), Why: "usage limit"}, time.Now())
			return s.r.ID, stopIn("S1", "done", false)
		}},
		{"under 3 minutes left", func(s *stopScene) (string, string) {
			s.r.StartedAt = time.Now().Add(-2*time.Hour + 2*time.Minute)
			runs.Save(s.dir, s.r)
			return s.r.ID, stopIn("S1", "done", false)
		}},
		{"usage limit", func(s *stopScene) (string, string) {
			return s.r.ID, stopIn("S1", "Claude AI usage limit reached|1900000000", false)
		}},
		{"a text job", func(s *stopScene) (string, string) {
			p := s.put(runs.Record{Job: "plan", Command: claudeCommand("S3", modelOpus, effortHigh, true)})
			return p.ID, stopIn("S3", "done", false)
		}},
		{"a plain launch", func(s *stopScene) (string, string) {
			p := s.put(runs.Record{Command: claudeCommand("S3", modelOpus, effortHigh, false)})
			return p.ID, stopIn("S3", "done", false)
		}},
		{"its ticket is gone", func(s *stopScene) (string, string) {
			p := s.put(runs.Record{Job: "build", Ticket: "T-99", Command: claudeCommand("S3", modelOpus, effortHigh, false)})
			return p.ID, stopIn("S3", "done", false)
		}},
		{"its resume chain is cut", func(s *stopScene) (string, string) {
			p := s.put(runs.Record{Job: "build", ResumeOf: "R-bbbbbbbb", Command: resumeCommand(s.r.Command, "S1", false)})
			return p.ID, stopIn("S1", "done", false)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newStopScene(t, "build", modelOpus)
			inProgress(s)
			id, input := c.setup(s)
			if s.stop(id, input) {
				t.Fatal("blocked; want it to fail open")
			}
			if c.name == "stop hook active" && stopWasCapped(s.dir, s.r.ID) {
				t.Fatal("marked capped with no block of its own")
			}
		})
	}
	// A transcript stands in for a Claude Code that sends no reply.
	s := newStopScene(t, "build", modelOpus)
	inProgress(s)
	tr := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(tr, []byte(`{"type":"user","message":{"content":"go"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"build: refused: T-1 is claimed"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}
`), 0o644)
	b, _ := json.Marshal(map[string]any{"session_id": "S1", "transcript_path": tr})
	if s.stop(s.r.ID, string(b)) {
		t.Fatal("a refusal in the transcript was blocked")
	}
}

// The hook blocks one run's end at most twice; the third end goes
// through, marked capped, and route counts and names it.
func TestStopCheckCap(t *testing.T) {
	s := newStopScene(t, "review", modelOpus)
	stopMove("in_progress", "in_review")(s)
	for i := range stopBlocks {
		if !s.stop(s.r.ID, stopIn("S1", "done", false)) {
			t.Fatalf("stop %d went through", i+1)
		}
	}
	if stopWasCapped(s.dir, s.r.ID) {
		t.Fatal("capped before the cap")
	}
	if s.stop(s.r.ID, stopIn("S1", "done", false)) {
		t.Fatal("a third block")
	}
	if !stopWasCapped(s.dir, s.r.ID) || stopMarks(s.dir, s.r.ID) != stopBlocks {
		t.Fatalf("marks %d, capped %v", stopMarks(s.dir, s.r.ID), stopWasCapped(s.dir, s.r.ID))
	}
	// A session blocked once that then posts its verdict is no cap.
	s2 := newStopScene(t, "review", modelOpus)
	stopMove("in_progress", "in_review")(s2)
	s2.stop(s2.r.ID, stopIn("S1", "done", false))
	note("Review: GO at abc1234")(s2)
	if s2.stop(s2.r.ID, stopIn("S1", "done", true)) || stopWasCapped(s2.dir, s2.r.ID) {
		t.Fatal("a session that posted its verdict after a block")
	}
}

// A run that ended past the hook's cap counts as no verdict even when it
// then timed out, and the block that follows says how many such runs
// there were.
func TestNoVerdictCountsCappedRuns(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	h.comment("T-1", "Handoff: PR x at abc1234")
	dir := filepath.Join(h.dir, "runs")
	os.MkdirAll(dir, 0o755)
	earlier := func(state string, capped bool) {
		r, _ := runs.Create(dir, runs.Record{Launcher: "reviewer", Limit: "45m", Command: []string{"true"}}, nil)
		now := time.Now()
		r.State, r.Ticket, r.Job, r.StartedAt, r.EndedAt, r.PID = state, "T-1", "review", now, &now, 42
		runs.Save(dir, r)
		if capped {
			addStopMark(dir, r.ID, stopBlocked)
			addStopMark(dir, r.ID, stopCapped)
		}
	}
	earlier(runs.TimedOut, false) // a plain timeout counts as none
	earlier(runs.Done, false)
	last := runs.Record{ID: "R-v6v6v6v6", Job: "review", Ticket: "T-1", PID: 42, StartedAt: time.Now(), Command: opusCmd(), Reply: "review done"}
	addStopMark(dir, last.ID, stopBlocked)
	addStopMark(dir, last.ID, stopCapped)
	if rt := h.route(last); rt.Status == "blocked" || rt.Mismatch == "" {
		t.Fatalf("one run with no verdict before it: %+v", rt)
	}
	earlier(runs.TimedOut, true) // the hook kept it going, then it timed out
	rt := h.route(last)
	if rt.Status != "blocked" || !strings.HasPrefix(rt.Send[0].Line, "block T-1: Escalation: review ended 3 times with no verdict, 2 of them past the Stop hook's blocks; the last said: review done") {
		t.Fatalf("capped thrice: %+v", rt)
	}
	if !slices.Contains(rt.Wrote, "T-1 blocked: its review ended 3 times with no verdict") {
		t.Fatalf("wrote %q: the ring body's fixed shape", rt.Wrote)
	}
}

// shWords splits a hook command as sh -c would, for the quoting shq
// writes: words part at spaces, '...' keeps what it holds, and \x outside
// quotes is x. A character sh would read as more than itself, outside
// quotes, fails the test.
func shWords(t *testing.T, s string) []string {
	t.Helper()
	var words []string
	var w strings.Builder
	in, word := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case in && ch == '\'':
			in = false
		case in:
			w.WriteByte(ch)
		case ch == '\'':
			in, word = true, true
		case ch == '\\' && i+1 < len(s):
			i++
			w.WriteByte(s[i])
			word = true
		case ch == ' ':
			if word {
				words = append(words, w.String())
				w.Reset()
				word = false
			}
		case strings.IndexByte("\"$`;&|<>(){}*?[]#~=%!\t\n\\", ch) >= 0:
			t.Fatalf("%q outside quotes in %q", ch, s)
		default:
			w.WriteByte(ch)
			word = true
		}
	}
	if in {
		t.Fatalf("unclosed quote in %q", s)
	}
	if word {
		words = append(words, w.String())
	}
	return words
}

// stopSupervisor stands in for a start's supervisor until the test ends:
// it keeps each command and marks its run running, as a supervisor does
// once claude starts, so startRun returns at once. No process starts.
func stopSupervisor(t *testing.T, dir string) *[]*exec.Cmd {
	t.Helper()
	var got []*exec.Cmd
	was := startSupervisor
	t.Cleanup(func() { startSupervisor = was })
	startSupervisor = func(sup *exec.Cmd) error {
		got = append(got, sup)
		if len(sup.Args) < 3 || sup.Args[1] != "supervise" {
			return fmt.Errorf("supervisor %q", sup.Args)
		}
		r, err := runs.Load(dir, sup.Args[2])
		if err != nil {
			return err
		}
		r.State, r.PID = runs.Running, 4242
		return runs.Save(dir, r)
	}
	return &got
}

// endRunByHand ends a run done, as its supervisor would once its session
// ended.
func endRunByHand(t *testing.T, dir string, r runs.Record) {
	t.Helper()
	r, err := runs.Load(dir, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r.State, r.EndedAt, r.Reply = runs.Done, &now, "done"
	if err := runs.Save(dir, r); err != nil {
		t.Fatal(err)
	}
}

// startJob runs a job, resume or again command in process and returns
// the run it started, as saved.
func startJob(h *harness, stdin, dir string, args ...string) runs.Record {
	h.t.Helper()
	code, out, errs := h.run(stdin, append(args, "--json")...)
	if code != 0 {
		h.t.Fatalf("%v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errs)
	}
	var s struct{ Run runs.Record }
	if err := json.Unmarshal([]byte(out), &s); err != nil || s.Run.ID == "" {
		h.t.Fatalf("%v: %v %s", args, err, out)
	}
	r, err := runs.Load(dir, s.Run.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

// The five jobs whose end is a note get the hook; text jobs don't. The
// hook's command names this binary and the store as absolute paths, quoted
// so sh -c gives them back byte for byte, whatever they hold.
func TestStopHookPinsStore(t *testing.T) {
	var hooked []string
	for _, k := range jobKinds {
		if k.stopHook {
			hooked = append(hooked, k.name)
		}
	}
	if want := []string{"build", "review", "qa", "live", "design"}; !slices.Equal(hooked, want) {
		t.Fatalf("hooked jobs %q; want %q", hooked, want)
	}
	dir := t.TempDir()
	odd := filepath.Join(dir, `it's a "store" \ here $HOME; *`)
	exe := filepath.Join(odd, "maestro ticket's")
	store := filepath.Join(odd, "tickets.db")
	for _, k := range jobKinds {
		cmd := withStopHook(claudeCommand("S1", modelOpus, effortHigh, k.lean), k.stopHook, exe, store)
		if !k.stopHook {
			if slices.Contains(cmd, "--settings") {
				t.Errorf("%s: %q has a hook", k.name, cmd)
			}
			continue
		}
		if got := shWords(t, hookCommand(t, cmd)); !slices.Equal(got, []string{exe, "stop-check", "--store", store}) {
			t.Errorf("%s: the hook runs %q", k.name, got)
		}
	}
	// A call's hook names its store as it resolved it, made absolute.
	c := &call{env: Env{Dir: dir, Getenv: func(string) string { return "" }}, storeFlag: "rel/tickets.db"}
	cmd, err := c.stopHook(claudeCommand("S1", modelOpus, effortHigh, false), "review")
	if err != nil {
		t.Fatal(err)
	}
	if hc := hookCommand(t, cmd); !strings.HasSuffix(hc, " stop-check --store "+shq(filepath.Join(dir, "rel", "tickets.db"))) {
		t.Fatalf("hook %q", hc)
	}
	// A plain launch's command is the launcher's: left as it is.
	own := []string{"claude", "-p", "--settings", "{}"}
	if got, _ := c.stopHook(own, ""); !slices.Equal(got, own) {
		t.Fatalf("plain launch: %q", got)
	}
}

// Resume and job again give a run exactly one hook, on this binary and
// the same store: from a command whose hook names a binary an upgrade
// moved, and from one started before jobs had a hook. The supervisor's
// stream swap keeps it.
func TestResumeRewritesHook(t *testing.T) {
	if !runs.Supported {
		t.Skip("resume needs a unix system")
	}
	h := newHarness(t)
	h.env["MAESTRO_TICKET_ACTOR"] = "senior-engineer"
	h.ok(nil, "create", "--title", "a")
	dir := filepath.Join(h.dir, "runs")
	sups := stopSupervisor(t, dir)
	store := h.env["MAESTRO_TICKET_STORE"]
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := shq(exe) + " stop-check --store " + shq(store)
	first := startJob(h, "", dir, "job", "build", "T-1")
	if got := hookCommand(t, first.Command); got != want {
		t.Fatalf("job: %q; want %q", got, want)
	}
	if a, ok := runs.StreamArgv(first.Command); !ok || hookCommand(t, a) != want {
		t.Fatalf("stream argv %q", a)
	}
	// An upgrade moved the binary the hook names.
	endRunByHand(t, dir, first)
	first, _ = runs.Load(dir, first.ID)
	first.Command = withStopHook(first.Command, true, "/old/bin/maestro-ticket", store)
	if err := runs.Save(dir, first); err != nil {
		t.Fatal(err)
	}
	resumed := startJob(h, "go on\n", dir, "resume", first.ID)
	if got := hookCommand(t, resumed.Command); got != want {
		t.Fatalf("resume: %q; want %q", got, want)
	}
	if flagValue(resumed.Command, "--resume") == "" {
		t.Fatalf("resume lost its session: %q", resumed.Command)
	}
	endRunByHand(t, dir, resumed)
	again := startJob(h, "", dir, "job", "again", resumed.ID)
	if got := hookCommand(t, again.Command); got != want {
		t.Fatalf("again: %q; want %q", got, want)
	}
	// A session begun before the hook: job again starts from its first
	// run's command, which has none.
	endRunByHand(t, dir, again)
	old := startJob(h, "", dir, "job", "again", again.ID)
	endRunByHand(t, dir, old)
	old, _ = runs.Load(dir, old.ID)
	old.Command = withStopHook(old.Command, false, "", "")
	if err := runs.Save(dir, old); err != nil {
		t.Fatal(err)
	}
	res := startJob(h, "go on\n", dir, "resume", old.ID)
	if got := hookCommand(t, res.Command); got != want {
		t.Fatalf("resume of a run with no hook: %q", got)
	}
	endRunByHand(t, dir, res)
	again2 := startJob(h, "", dir, "job", "again", res.ID)
	if got := hookCommand(t, again2.Command); got != want {
		t.Fatalf("again from a session with no hook: %q", got)
	}
	// What the supervisor runs, its stream swap, keeps the hook, and the
	// supervisor finds the run on the same store.
	if a, ok := runs.StreamArgv(again2.Command); !ok || hookCommand(t, a) != want {
		t.Fatalf("stream argv %q", a)
	}
	if n := len(*sups); n != 6 {
		t.Fatalf("%d supervisors; want 6", n)
	}
	for _, sup := range *sups {
		if got := flagValue(sup.Args, "--store"); got != store {
			t.Fatalf("supervisor %q: store %q; want %q", sup.Args, got, store)
		}
	}
}

// The store reaches the session only in the hook's command: a session's
// environment gets no MAESTRO_TICKET_STORE the launcher didn't have, so a
// maestro-ticket it runs in a scratch repo finds that repo's own store.
// The session's environment is its supervisor's, with the run id and
// actor added.
func TestSessionEnvAddsNoStore(t *testing.T) {
	t.Setenv("MAESTRO_TICKET_STORE", "")
	os.Unsetenv("MAESTRO_TICKET_STORE") // t.Setenv puts it back after
	h := newHarness(t)
	h.env["MAESTRO_TICKET_ACTOR"] = "senior-engineer"
	h.ok(nil, "create", "--title", "a")
	store := h.env["MAESTRO_TICKET_STORE"]
	delete(h.env, "MAESTRO_TICKET_STORE")
	dir := filepath.Join(h.dir, "runs")
	sups := stopSupervisor(t, dir)
	r := startJob(h, "", dir, "job", "build", "T-1", "--store", store)
	if len(*sups) != 1 {
		t.Fatalf("%d supervisors; want 1", len(*sups))
	}
	sup := (*sups)[0]
	if sup.Env == nil {
		t.Fatal("the supervisor inherits the test's whole environment: want its own list")
	}
	for _, kv := range sup.Env {
		if strings.HasPrefix(kv, "MAESTRO_TICKET_STORE=") {
			t.Fatalf("the supervisor's env has the store: %q", kv)
		}
	}
	for _, w := range r.Command[:slices.Index(r.Command, "claude")] {
		if strings.HasPrefix(w, "MAESTRO_TICKET_STORE=") {
			t.Fatalf("the session's command sets the store: %q", r.Command)
		}
	}
	if !strings.Contains(hookCommand(t, r.Command), " --store "+shq(store)) {
		t.Fatalf("hook %q", hookCommand(t, r.Command))
	}
}

// stop-check run where the session might run it, in another git repo
// with a store of its own and no MAESTRO_TICKET_STORE, reads the store
// its hook names: the live run, and its ticket's notes.
func TestStopCheckUsesPinnedStore(t *testing.T) {
	h := newHarness(t)
	h.env["MAESTRO_TICKET_ACTOR"] = "senior-engineer"
	h.ok(nil, "create", "--title", "a")
	dir := filepath.Join(h.dir, "runs")
	stopSupervisor(t, dir)
	r := startJob(h, "", dir, "job", "build", "T-1")
	words := shWords(t, hookCommand(t, r.Command))
	if len(words) < 2 || words[1] != "stop-check" {
		t.Fatalf("hook runs %q", words)
	}
	other := t.TempDir()
	if out, err := exec.Command("git", "-C", other, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	// The session's view: its own repo, no store in its env, its run id.
	s := &harness{t: t, dir: other, env: map[string]string{runs.RunIDEnv: r.ID}}
	in := `{"session_id":"` + sessionOf(r) + `","stop_hook_active":false,"last_assistant_message":"done"}`
	hook := func() string {
		t.Helper()
		code, out, errs := s.run(in, words[1:]...)
		if code != 0 || errs != "" {
			t.Fatalf("hook: exit %d: %s %s", code, out, errs)
		}
		return out
	}
	if out := hook(); !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "T-1") {
		t.Fatalf("no note on the pinned store: %q; want a block", out)
	}
	h.ok(nil, "comment", "T-1", "--body", "Handoff: built at abc1234")
	if out := hook(); out != "" {
		t.Fatalf("with its Handoff: %q; want nothing", out)
	}
}
