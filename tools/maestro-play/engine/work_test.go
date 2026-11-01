package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

var playbookStub = playbook.Playbook{Name: "stub", Role: "engineer"}

// workReplies are plan, build (which commits in the worktree) and verify.
func workReplies(commit string) []map[string]any {
	build := reply("done", map[string]any{"files": []any{"change.txt"}, "tests": []any{"go test ./... -> exit 0"}, "items": []any{}})
	build["commit"] = commit
	return []map[string]any{
		reply("done", map[string]any{"plan": "scratch", "risks": []any{"none"}}),
		build,
		reply("done", map[string]any{"criteria": []any{"C1 pass"}, "failures": []any{}}),
	}
}

// ticketStore is the store an end-to-end run uses: the fake, or the real
// maestro-ticket built from ../../maestro-ticket.
type ticketStore interface {
	get(id string) map[string]any
	sendBack(id, note string)
}

type fakeTickets struct{ w *world }

func (f fakeTickets) get(id string) map[string]any {
	b, _ := json.Marshal(f.w.ticket(id))
	m := map[string]any{}
	json.Unmarshal(b, &m)
	return m
}

func (f fakeTickets) sendBack(id, note string) {
	s := f.w.store()
	t := s.Tickets[id]
	t.Status, t.Tries = "in_progress", t.Tries+1
	t.Comments = append(t.Comments, fakeComment{ID: len(t.Comments) + 1, Author: "reviewer", Body: note})
	f.w.saveFake("tickets.json", &s)
}

type realTickets struct {
	w   *world
	bin string
}

func (r realTickets) call(args ...string) map[string]any {
	r.w.t.Helper()
	cmd := exec.Command(r.bin, append(args, "--json")...)
	cmd.Dir, cmd.Env = r.w.repo, r.w.e.cfg.Environ
	out, err := cmd.Output()
	if err != nil {
		r.w.t.Fatalf("maestro-ticket %v: %v\n%s", args, err, out)
	}
	m := map[string]any{}
	json.Unmarshal(out, &m)
	return m
}

func (r realTickets) get(id string) map[string]any { return r.call("get", id) }

func (r realTickets) sendBack(id, note string) {
	r.call("transition", id, "--from", "in_review", "--to", "in_progress", "--actor", "reviewer", "--actor-role", "reviewer")
	r.call("comment", id, "--actor", "reviewer", "--body", note)
}

// TestWorkEndToEnd runs a work-shaped playbook on a real git repo with an
// origin, fake gh and claude, and the fake or the real ticket store: claim,
// worktree, plan, build (a real commit), check, verify, head, open pr, then
// in_review with the lease released. A Kickback then sends it back, and a
// second run reuses the branch and the PR.
func TestWorkEndToEnd(t *testing.T) {
	t.Run("fake store", func(t *testing.T) {
		w := newWorldAt(t, "testdata/work", &fakeTicketT{ID: "T-1", Title: "Add --json", Body: "Type: feature\nCriteria: C1",
			Type: "ship", Status: "todo", Role: "engineer", Owner: "chief-of-staff", Version: 1})
		workEndToEnd(t, w, fakeTickets{w}, "T-1")
	})
	// The repo's own playbooks/work/ and shared templates, on the same
	// path.
	t.Run("repo playbook", func(t *testing.T) {
		w := newWorldAt(t, "../../..", &fakeTicketT{ID: "T-1", Title: "Add --json", Body: "Type: feature\nCriteria: C1",
			Type: "ship", Status: "todo", Role: "engineer", Owner: "chief-of-staff", Version: 1})
		workEndToEnd(t, w, fakeTickets{w}, "T-1")
	})
	t.Run("real store", func(t *testing.T) {
		if testing.Short() {
			t.Skip("builds maestro-ticket")
		}
		w := newWorldAt(t, "testdata/work")
		rt := useRealTickets(t, w)
		id := str(rt.call("create", "--title", "Add --json", "--body", "Type: feature\nCriteria: C1", "--role", "engineer",
			"--status", "todo", "--actor", "chief-of-staff")["id"])
		workEndToEnd(t, w, rt, id)
	})
}

// useRealTickets builds maestro-ticket from ../../maestro-ticket and points
// the world's engine at it.
func useRealTickets(t *testing.T, w *world) realTickets {
	t.Helper()
	bin := filepath.Join(w.dir, "bin", "maestro-ticket-real")
	build := exec.Command("go", "build", "-o", bin, "./cmd/maestro-ticket")
	build.Dir = "../../maestro-ticket"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build maestro-ticket: %v\n%s", err, out)
	}
	w.e.cfg.TicketBin = bin
	return realTickets{w, bin}
}

func workEndToEnd(t *testing.T, w *world, store ticketStore, id string) {
	w.script(fakeConfig{Harness: workReplies("Add --json")})
	r := w.run(RunOpts{Ref: "work", Ticket: id})
	if r.Status != StatusDone {
		t.Fatalf("result %+v", r)
	}
	tk := store.get(id)
	if tk["status"] != "in_review" || tk["claim"] != nil {
		t.Fatalf("ticket %v claim %v, want in_review and released", tk["status"], tk["claim"])
	}
	origin := filepath.Join(w.dir, "origin.git")
	branch := "maestro/" + id
	sha := w.git(w.dir, "--git-dir", origin, "rev-parse", branch)
	if msg := w.git(w.dir, "--git-dir", origin, "log", "-1", "--format=%s", branch); msg != "Add --json" {
		t.Errorf("pushed commit %q", msg)
	}
	url := "https://github.com/o/r/pull/7"
	note := lastComment(tk)
	for _, want := range []string{"Handoff: PR " + url + " from " + branch + " at " + sha + ".", `Checks: "2 passed"`,
		"sha: " + sha, "pr: " + url, "author-family: claude"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	create := w.find("gh", "pr", "create")
	if len(create) != 1 || flagValue(create[0].Args, "--base") != "main" || flagValue(create[0].Args, "--head") != branch ||
		flagValue(create[0].Args, "--title") != id+": Add --json" {
		t.Fatalf("pr create %+v", create)
	}
	s := w.sessions()
	if len(s) != 3 || resumed(s[0]) || !resumed(s[1]) || !resumed(s[2]) {
		t.Fatalf("sessions %+v", s)
	}
	// The worktree is clean, so it's gone; the branch stays for a fix.
	wt := filepath.Join(w.repo, ".maestro", "worktrees", id)
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree kept: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(w.repo, ".git", "info", "exclude")); !strings.Contains(string(b), "/.maestro/worktrees/") {
		t.Errorf("exclude:\n%s", b)
	}

	// Review sends it back. The next run skips start and plan, puts the
	// worktree back on the same branch, and pushes to the same PR.
	store.sendBack(id, "Kickback: C1 prints a trailing comma.")
	w.script(fakeConfig{Harness: workReplies("Drop the trailing comma")[1:], PRs: w.fake().PRs})
	before := len(w.calls())
	r = w.run(RunOpts{Ref: "work", Ticket: id})
	if r.Status != StatusDone {
		t.Fatalf("second run %+v", r)
	}
	tk = store.get(id)
	if tk["status"] != "in_review" || tk["claim"] != nil {
		t.Fatalf("second run: ticket %v claim %v", tk["status"], tk["claim"])
	}
	sha2 := w.git(w.dir, "--git-dir", origin, "rev-parse", branch)
	if sha2 == sha || w.git(w.dir, "--git-dir", origin, "rev-parse", branch+"~1") != sha {
		t.Errorf("second commit %s on top of %s not pushed", sha2, sha)
	}
	note = lastComment(tk)
	for _, want := range []string{"Handoff: PR " + url + " from " + branch + " at " + sha2 + ".", "Skipped: start — already in_progress",
		"Skipped: plan — sent back by review"} {
		if !strings.Contains(note, want) {
			t.Errorf("second note lacks %q:\n%s", want, note)
		}
	}
	if n := len(w.find("gh", "pr", "create")); n != 1 {
		t.Errorf("%d PRs created, want the first only", n)
	}
	var adds []string
	for _, c := range w.calls()[before:] {
		if c.Bin == "git" && len(c.Args) > 1 && c.Args[0] == "worktree" && c.Args[1] == "add" {
			adds = append(adds, strings.Join(c.Args, " "))
		}
	}
	if len(adds) != 1 || adds[0] != "worktree add "+wt+" "+branch {
		t.Errorf("worktree adds %q, want the branch reused", adds)
	}
	if s := w.sessions(); len(s) != 5 || resumed(s[3]) || !strings.Contains(s[3].Prompt, "# Build") {
		t.Errorf("second run's sessions %+v", s[3:])
	}
}

func lastComment(t map[string]any) string {
	cs, _ := t["comments"].([]any)
	if len(cs) == 0 {
		return ""
	}
	c, _ := cs[len(cs)-1].(map[string]any)
	return str(c["body"])
}

// A worktree that still exists on its branch is reused as it is; one on
// another branch fails.
func TestWorktreeAddReuses(t *testing.T) {
	w := newWorld(t)
	st := &State{ID: "r1"}
	first, err := w.e.worktreeAdd(st, "T-1", "main")
	if err != nil || first["status"] != "ok" {
		t.Fatalf("first %v %v", first, err)
	}
	again, err := w.e.worktreeAdd(st, "T-1", "main")
	if err != nil || again["status"] != "ok" || again["path"] != first["path"] || !strings.HasPrefix(str(again["output"]), "reused") {
		t.Fatalf("again %v %v", again, err)
	}
	w.git(str(first["path"]), "checkout", "-q", "-b", "other")
	if r, _ := w.e.worktreeAdd(st, "T-1", "main"); r["status"] != "failed" {
		t.Errorf("worktree on another branch: %v", r)
	}
	if r, _ := w.e.worktreeAdd(st, "T-2", "no-such-branch"); r["status"] != "failed" || toInt(r["exit_code"]) == 0 {
		t.Errorf("bad --from: %v", r)
	}
}

// A new worktree links the skills an install hid in .agents/skills, so
// Codex finds them; a tracked skill comes with the checkout and an
// untracked one that git doesn't ignore stays out. The links leave git
// status clean, and the worktree still removes.
func TestWorktreeAddLinksHiddenSkills(t *testing.T) {
	w := newWorld(t)
	for _, n := range []string{"maestro-tdd", "ours", "stray"} {
		os.MkdirAll(filepath.Join(w.repo, ".agents", "skills", n), 0o755)
		os.WriteFile(filepath.Join(w.repo, ".agents", "skills", n, "SKILL.md"), []byte(n+"\n"), 0o644)
	}
	w.git(w.repo, "add", ".agents/skills/ours")
	w.git(w.repo, "commit", "-q", "-m", "our skill")
	exclude := filepath.Join(w.repo, ".git", "info", "exclude")
	os.WriteFile(exclude, []byte("/.agents/skills/maestro-tdd\n"), 0o644)
	r, err := w.e.worktreeAdd(&State{ID: "r1"}, "T-1", "main")
	if err != nil || r["status"] != "ok" {
		t.Fatalf("worktree add: %v %v", r, err)
	}
	wt := str(r["path"])
	if b, err := os.ReadFile(filepath.Join(wt, ".agents", "skills", "maestro-tdd", "SKILL.md")); err != nil || string(b) != "maestro-tdd\n" {
		t.Errorf("hidden skill not linked: %q %v", b, err)
	}
	if fi, err := os.Lstat(filepath.Join(wt, ".agents", "skills", "ours")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("tracked skill should be the checkout's own folder: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wt, ".agents", "skills", "stray")); err == nil {
		t.Error("an untracked skill git doesn't ignore was linked")
	}
	if out := w.git(wt, "status", "--porcelain", "--untracked-files=all"); out != "" {
		t.Errorf("worktree not clean: %s", out)
	}
	w.git(w.repo, "worktree", "remove", wt)
}

// A check that isn't listed fails the step; check all with no checks
// listed, or no setup, passes with none run.
func TestCheckNeedsChecks(t *testing.T) {
	w := newWorld(t)
	st := &State{ID: "r1"}
	if r, _ := w.e.check(st, "nope"); r["status"] != "failed" || !strings.Contains(str(r["output"]), "no check nope under ## Checks") {
		t.Errorf("unknown check: %v", r)
	}
	if r, _ := w.e.check(st, "lint"); r["status"] != "ok" || r["summary"] != "1 passed" {
		t.Errorf("one check: %v", r)
	}
	os.Remove(filepath.Join(w.repo, SetupPath))
	if r, _ := w.e.check(st, "all"); r["status"] != "ok" || r["summary"] != "no checks listed" || len(r["checks"].([]any)) != 0 {
		t.Errorf("no setup: %v", r)
	}
	if e := w.e.environment(); e["project"].(map[string]any)["main_branch"] != "main" {
		t.Errorf("default main branch: %v", e)
	}
}

// The shipped template parses to its main branch and checks.
func TestParseSetupTemplate(t *testing.T) {
	b, err := os.ReadFile("../../../templates/project-setup.md")
	if err != nil {
		t.Skip(err)
	}
	s := parseSetup(string(b))
	var names []string
	for _, c := range s.Checks {
		names = append(names, c.Name+"="+c.Command)
	}
	if s.MainBranch != "main" || strings.Join(names, ",") != "lint=make lint,test=make test" || s.Name != "<name>" {
		t.Errorf("setup %+v", s)
	}
	s = parseSetup("## Project\n- **Name:** tiny\n## Main branch\n`trunk`\n## Checks\n- **unit**: `go test ./...`\n- not a check\n* vet: `go vet ./...`\n")
	if s.Name != "tiny" || s.MainBranch != "trunk" || len(s.Checks) != 2 || s.Checks[0].Name != "unit" || s.Checks[1].Command != "go vet ./..." ||
		s.BadCheck != "line 7: - not a check" {
		t.Errorf("setup %+v", s)
	}
	if s = parseSetup("## Checks\n\nNone yet.\n\n## Run\n\n- make run\n"); len(s.Checks) != 0 || s.BadCheck != "" {
		t.Errorf("prose under ## Checks: %+v", s)
	}
}

// A bullet under ## Checks that isn't a check fails check all and names
// the line, so a typo can't pass as no checks; prose there is no checks.
func TestCheckBadBullet(t *testing.T) {
	w := newWorld(t)
	st := &State{ID: "r1"}
	path := filepath.Join(w.repo, SetupPath)
	os.WriteFile(path, []byte("# Project setup\n\n## Checks\n\n- lint: `fake-check lint`\n- test: make test\n"), 0o644)
	for _, name := range []string{"all", "lint"} {
		r, err := w.e.check(st, name)
		if err != nil || r["status"] != "failed" || r["summary"] != "bad ## Checks line" ||
			!strings.Contains(str(r["output"]), path+": line 6: - test: make test: under ## Checks, want - <name>: `<command>`") {
			t.Errorf("check %s: %v %v", name, r, err)
		}
	}
	if n := len(w.find("check")); n != 0 {
		t.Errorf("%d checks ran", n)
	}
	os.WriteFile(path, []byte("# Project setup\n\n## Checks\n\nNone yet.\n"), 0o644)
	if r, _ := w.e.check(st, "all"); r["status"] != "ok" || r["summary"] != "no checks listed" {
		t.Errorf("prose: %v", r)
	}
}

// A check that exits, leaving a background child holding its output,
// passes at once, and the child dies with it.
func TestCheckLeavesNoChild(t *testing.T) {
	w := newWorld(t)
	os.WriteFile(filepath.Join(w.repo, SetupPath),
		[]byte("## Checks\n\n- bg: `sleep 30 & echo $! > \"$FAKE_DIR/child.pid\"; echo ok`\n"), 0o644)
	start := time.Now()
	r, err := w.e.check(&State{ID: "r1"}, "all")
	if err != nil || r["status"] != "ok" || r["summary"] != "1 passed" || !strings.Contains(str(r["output"]), "ok") {
		t.Fatalf("check %v %v", r, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("check took %v", d)
	}
	if !w.childGone() {
		t.Errorf("the check's background child outlived it")
	}
}

// git and gh calls can't stop on a prompt: they get GIT_TERMINAL_PROMPT=0,
// GH_PROMPT_DISABLED=1 and a batch-mode ssh, unless the user set an ssh
// command of their own.
func TestGitAndGhNeverPrompt(t *testing.T) {
	for _, c := range []struct {
		env, config, want string
	}{
		{"GIT_SSH_COMMAND=", "", "0|1|ssh -o BatchMode=yes"},
		{"GIT_SSH_COMMAND=ssh -i key", "", "0|1|ssh -i key"},
		{"GIT_SSH=my-ssh", "", "0|1|"},
		{"GIT_SSH_COMMAND=", "ssh -i key", "0|1|"},
	} {
		w := newWorld(t)
		if c.config != "" {
			w.git(w.repo, "config", "core.sshCommand", c.config)
		}
		w.e.cfg.Environ = append(w.e.cfg.Environ, "GIT_SSH_COMMAND=", "GIT_SSH=", c.env)
		st := &State{ID: "r1"}
		if r, err := w.e.head(st); err != nil || r["status"] != "ok" {
			t.Fatalf("head %v %v", r, err)
		}
		w.e.gh(st, w.repo, "pr", "list")
		calls := append(w.find("git", "rev-parse"), w.find("gh", "pr")...)
		if len(calls) != 2 {
			t.Fatalf("calls %+v", calls)
		}
		for _, call := range calls {
			if call.NoPrompt != c.want {
				t.Errorf("%s %v with %s, core.sshCommand %q: %q, want %q", call.Bin, call.Args, c.env, c.config, call.NoPrompt, c.want)
			}
		}
	}
}

// An interrupt to the engine reaches its command's process group, which
// no longer shares the engine's, and still stops the engine.
func TestForwardSignals(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := exec.Command(self, "-test.run=^$")
	cmd.Env = append(os.Environ(), "FAKE_SIGNAL_DIR="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	read := func(name string) string {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil && len(b) > 0 {
				return strings.TrimSpace(string(b))
			}
		}
		return ""
	}
	pid, _ := strconv.Atoi(read("ready"))
	if pid <= 0 {
		cmd.Process.Kill()
		t.Fatal("the command never started")
	}
	t.Cleanup(func() { syscall.Kill(-pid, syscall.SIGKILL) })
	time.Sleep(100 * time.Millisecond) // the engine listens just after the command starts
	cmd.Process.Signal(os.Interrupt)
	select {
	case <-done:
		if ws := cmd.ProcessState.Sys().(syscall.WaitStatus); !ws.Signaled() || ws.Signal() != syscall.SIGINT {
			t.Errorf("engine exited %v, want killed by SIGINT", cmd.ProcessState)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the interrupt didn't stop the engine")
	}
	if got := read("got"); got != "INT" {
		t.Errorf("the command's group got %q, want INT", got)
	}
}

// A claude reply with no structured_output still counts when its text
// holds the JSON; a codex session resumes by its own thread id.
func TestHarnessReplies(t *testing.T) {
	if m := replyFromText("Done.\n```json\n{\"status\": \"done\", \"n\": 2}\n```\n"); m["status"] != "done" || m["n"] != 2 {
		t.Errorf("fenced: %v", m)
	}
	if m := replyFromText("no json here"); m != nil {
		t.Errorf("plain text: %v", m)
	}
	w := newWorld(t, todoTicket())
	text := workReplies("x")[0]
	text["text_reply"] = true
	w.script(fakeConfig{Harness: []map[string]any{text}})
	st := w.e.newState(&playbookStub, "eng-1", "", nil)
	r, err := w.e.session(st, "plan", "prompt", []string{"plan"}, "mid", "", false)
	if err != nil || r["status"] != "ok" || harnessReply(r)["status"] != "done" || !st.SessionStarted {
		t.Fatalf("text reply %v %v", r, err)
	}
	w.e.cfg.Harness = "codex"
	w.script(fakeConfig{Harness: []map[string]any{workReplies("x")[0], workReplies("x")[0]}})
	st = w.e.newState(&playbookStub, "eng-1", "", nil)
	for i := 0; i < 2; i++ {
		if r, err := w.e.session(st, "plan", "prompt", []string{"plan"}, "mid", "", false); err != nil || harnessReply(r)["status"] != "done" {
			t.Fatalf("codex %d: %v %v", i, r, err)
		}
	}
	s := w.sessions()[1:] // after the claude call
	if len(s) != 2 || resumed(s[0]) || !resumed(s[1]) || s[1].Args[2] != "codex-thread-1" || hasFlag(s[0].Args, "-m") {
		t.Errorf("codex calls %+v", s)
	}
}

// check, head and open pr fail on a worktree with uncommitted changes, so
// no check passes and no sha or PR goes out for files the branch lacks.
// open pr also needs a commit beyond the main branch, and skips a fork's
// PR from a branch of the same name.
func TestDirtyWorktreeFailsCheckHeadAndOpenPR(t *testing.T) {
	w := newWorld(t)
	if r, _ := w.e.openPR(&State{ID: "r1"}, "", ""); r["status"] != "failed" || !strings.Contains(str(r["output"]), "worktree add first") {
		t.Errorf("open pr with no branch: %v", r)
	}
	st := &State{ID: "r1", Ticket: "T-1"}
	wt, err := w.e.worktreeAdd(st, "T-1", "main")
	if err != nil || wt["status"] != "ok" {
		t.Fatalf("worktree add %v %v", wt, err)
	}
	st.Worktree, st.Branch = str(wt["path"]), str(wt["branch"])
	if r, _ := w.e.openPR(st, "", ""); r["status"] != "failed" || !strings.Contains(str(r["output"]), "maestro/T-1 has no commits beyond main") {
		t.Errorf("open pr with nothing to push: %v", r)
	}
	os.WriteFile(filepath.Join(st.Worktree, "stray.txt"), []byte("x\n"), 0o644)
	check, _ := w.e.check(st, "all")
	head, _ := w.e.head(st)
	pr, _ := w.e.openPR(st, "", "")
	for name, r := range map[string]map[string]any{"check": check, "head": head, "open pr": pr} {
		if r["status"] != "failed" || !strings.Contains(str(r["output"]), name+": "+st.Worktree+" has uncommitted changes") ||
			!strings.Contains(str(r["output"]), "?? stray.txt") {
			t.Errorf("%s on a dirty worktree: %v", name, r)
		}
	}
	if b, _ := os.ReadFile(str(check["output_path"])); !strings.Contains(string(b), "?? stray.txt") {
		t.Errorf("check output %q", b)
	}
	if n, m := len(w.find("check")), len(w.find("git", "push")); n != 0 || m != 0 {
		t.Errorf("%d checks and %d pushes ran on a dirty worktree", n, m)
	}
	w.git(st.Worktree, "add", "stray.txt")
	w.git(st.Worktree, "commit", "-q", "-m", "stray")
	w.script(fakeConfig{PRs: []fakePR{{Number: 3, URL: "https://github.com/fork/r/pull/3", Head: "maestro/T-1", State: "OPEN", Fork: true}}})
	if r, _ := w.e.check(st, "all"); r["status"] != "ok" {
		t.Errorf("check on a clean worktree: %v", r)
	}
	if r, _ := w.e.head(st); r["status"] != "ok" || r["sha"] != w.git(st.Worktree, "rev-parse", "HEAD") {
		t.Errorf("head on a clean worktree: %v", r)
	}
	if r, _ := w.e.openPR(st, "", ""); r["status"] != "ok" || r["url"] != "https://github.com/o/r/pull/8" || len(w.find("gh", "pr", "create")) != 1 {
		t.Errorf("open pr beside a fork's PR: %v", r)
	}
}

// blockedReplies are a plan step that blocks and the escalation's reply.
var blockedReplies = []map[string]any{
	{"reply": map[string]any{"status": "blocked", "summary": "which flag name?"}},
	reply("done", map[string]any{"happened": "no flag name"}),
}

// The store takes only the three roles, so an escalation to an owner who
// is a person moves the ticket to blocked under its own role. A move the
// store refuses fails the run, which still drops the lease.
func TestEscalationToAPersonKeepsTheRole(t *testing.T) {
	newTicket := func() *fakeTicketT {
		return &fakeTicketT{ID: "T-1", Title: "Add --json", Body: "Type: feature", Type: "ship", Status: "todo", Role: "engineer",
			Owner: "paul", Version: 1}
	}
	t.Run("fake store", func(t *testing.T) {
		w := newWorldAt(t, "testdata/work", newTicket())
		w.script(fakeConfig{Harness: blockedReplies})
		r := w.run(RunOpts{Ref: "work", Ticket: "T-1"})
		if r.Status != StatusBlocked || strings.Contains(r.Reason, "write failed") {
			t.Fatalf("result %+v", r)
		}
		tk := w.ticket("T-1")
		if tk.Status != "blocked" || tk.Role != "engineer" || tk.Claim != nil {
			t.Errorf("ticket %s role %s claim %+v", tk.Status, tk.Role, tk.Claim)
		}
		if move := w.find("ticket", "transition", "T-1", "--to", "blocked"); len(move) != 1 || hasFlag(move[0].Args, "--role") {
			t.Errorf("move %+v", move)
		}
	})
	t.Run("refused move", func(t *testing.T) {
		w := newWorldAt(t, "testdata/work", newTicket())
		w.script(fakeConfig{Harness: blockedReplies, Replies: map[string][]map[string]any{
			"ticket transition": {{}, {"exit": 2, "code": "invalid"}}, // start passes, the escalation's move fails
		}})
		r := w.run(RunOpts{Ref: "work", Ticket: "T-1"})
		if r.Status != StatusFailed || r.ExitCode() != 1 || !strings.Contains(r.Reason, "escalation write failed: escalate-move: exit 2") {
			t.Fatalf("result %+v", r)
		}
		if tk := w.ticket("T-1"); tk.Status != "in_progress" || tk.Claim != nil {
			t.Errorf("ticket %s claim %+v, want in_progress and released", tk.Status, tk.Claim)
		}
	})
	t.Run("real store", func(t *testing.T) {
		if testing.Short() {
			t.Skip("builds maestro-ticket")
		}
		w := newWorldAt(t, "testdata/work")
		rt := useRealTickets(t, w)
		id := str(rt.call("create", "--title", "Add --json", "--body", "Type: feature", "--role", "engineer",
			"--status", "todo", "--owner", "paul", "--actor", "chief-of-staff")["id"])
		w.script(fakeConfig{Harness: blockedReplies})
		r := w.run(RunOpts{Ref: "work", Ticket: id})
		if r.Status != StatusBlocked || strings.Contains(r.Reason, "write failed") {
			t.Fatalf("result %+v", r)
		}
		tk := rt.get(id)
		if tk["status"] != "blocked" || tk["role"] != "engineer" || tk["claim"] != nil {
			t.Errorf("ticket %v role %v claim %v", tk["status"], tk["role"], tk["claim"])
		}
	})
}

// A start that fails may still have made the session, so the retry starts
// a new one with a new id instead of resuming.
func TestFailedStartRetriesWithANewSession(t *testing.T) {
	w := newWorld(t, todoTicket())
	w.script(fakeConfig{Harness: []map[string]any{{"is_error": true, "result": "overloaded"}, workReplies("x")[0]}})
	st := w.e.newState(&playbookStub, "eng-1", "", nil)
	if r, err := w.e.session(st, "plan", "prompt", []string{"plan"}, "mid", "", false); err != nil || r["status"] != "failed" || st.SessionStarted {
		t.Fatalf("failed start %v %v, started %v", r, err, st.SessionStarted)
	}
	if r, err := w.e.session(st, "plan.try1", "prompt", []string{"plan"}, "mid", "", false); err != nil || r["status"] != "ok" {
		t.Fatalf("retry %v %v", r, err)
	}
	s := w.sessions()
	if len(s) != 2 || resumed(s[0]) || resumed(s[1]) {
		t.Fatalf("sessions %+v", s)
	}
	first, second := flagValue(s[0].Args, "--session-id"), flagValue(s[1].Args, "--session-id")
	if first == "" || second == "" || first == second {
		t.Errorf("session ids %q then %q, want two new ones", first, second)
	}
}

// A project with no checks still gets its PR: check all passes with none
// run, and the Handoff note says so.
func TestNoChecksStillOpensThePR(t *testing.T) {
	w := newWorldAt(t, "testdata/work", &fakeTicketT{ID: "T-1", Title: "Add --json", Body: "Type: feature\nCriteria: C1",
		Type: "ship", Status: "todo", Role: "engineer", Owner: "chief-of-staff", Version: 1})
	os.WriteFile(filepath.Join(w.repo, SetupPath), []byte("# Project setup\n\n## Main branch\n\nmain\n\n## Checks\n\nNone yet.\n"), 0o644)
	w.script(fakeConfig{Harness: workReplies("Add --json")})
	r := w.run(RunOpts{Ref: "work", Ticket: "T-1"})
	if r.Status != StatusDone {
		t.Fatalf("result %+v", r)
	}
	tk := w.ticket("T-1")
	if note := tk.Comments[len(tk.Comments)-1].Body; tk.Status != "in_review" || !strings.Contains(note, `Checks: "no checks listed"`) {
		t.Errorf("ticket %s, note:\n%s", tk.Status, note)
	}
	if n := len(w.find("check")); n != 0 {
		t.Errorf("%d checks ran", n)
	}
}

// Sessions may do anything unless the user's own args set permissions;
// then the engine adds none. Codex gets the flag on resume too.
func TestSessionPermissions(t *testing.T) {
	session := func(w *world, st *State) fakeCall {
		t.Helper()
		if _, err := w.e.session(st, "plan", "prompt", []string{"plan"}, "mid", "", false); err != nil {
			t.Fatal(err)
		}
		s := w.sessions()
		return s[len(s)-1]
	}
	w := newWorld(t)
	w.script(fakeConfig{Harness: []map[string]any{workReplies("x")[0]}})
	if c := session(w, w.e.newState(&playbookStub, "eng-1", "", nil)); flagValue(c.Args, "--permission-mode") != "bypassPermissions" {
		t.Errorf("claude default %q", c.Args)
	}
	w.e.cfg.ClaudeArgs = []string{"--permission-mode", "acceptEdits"}
	if c := session(w, w.e.newState(&playbookStub, "eng-1", "", nil)); strings.Contains(strings.Join(c.Args, " "), "bypassPermissions") ||
		flagValue(c.Args, "--permission-mode") != "acceptEdits" {
		t.Errorf("claude with the user's mode %q", c.Args)
	}
	w.e.cfg.Harness = "codex"
	st := w.e.newState(&playbookStub, "eng-1", "", nil)
	for _, c := range []fakeCall{session(w, st), session(w, st)} {
		if !hasFlag(c.Args, "--dangerously-bypass-approvals-and-sandbox") {
			t.Errorf("codex default %q", c.Args)
		}
	}
	w.e.cfg.CodexArgs = []string{"-c", "sandbox_mode=workspace-write"}
	if c := session(w, w.e.newState(&playbookStub, "eng-1", "", nil)); hasFlag(c.Args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("codex with the user's sandbox %q", c.Args)
	}
	for _, c := range []struct {
		h    string
		user string
		ours bool
	}{
		{"claude", "", true}, {"claude", "--model x", true}, {"claude", "--permission-mode=plan", false},
		{"claude", "--dangerously-skip-permissions", false}, {"claude", "--allowedTools Edit", false},
		{"claude", "--settings perms.json", false}, {"claude", "--settings=perms.json", false},
		{"codex", "", true}, {"codex", "-c model=x", true}, {"codex", "-s read-only", false},
		{"codex", "--sandbox=workspace-write", false}, {"codex", "--config=approval_policy=never", false},
		{"codex", "-p work", false}, {"codex", "--profile=work", false},
	} {
		got := permissionArgs(c.h, strings.Fields(c.user))
		if ours := len(got) > len(strings.Fields(c.user)); ours != c.ours {
			t.Errorf("permissionArgs(%s, %q) = %q", c.h, c.user, got)
		}
	}
}

// childGone reports, within 5s, that the child a hung fake started has
// exited.
func (w *world) childGone() bool {
	b, err := os.ReadFile(filepath.Join(w.dir, "child.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		w.t.Fatalf("no child.pid: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
			if f := strings.Fields(string(b)); len(f) > 2 && f[2] == "Z" {
				return true // dead, not yet reaped
			}
		}
	}
	return false
}

// A check that hangs is killed with what it started; it fails with
// "timed out after", and the other checks still run.
func TestCheckTimesOut(t *testing.T) {
	w := newWorld(t)
	w.e.cfg.CheckTimeout = 300 * time.Millisecond
	w.script(fakeConfig{Replies: map[string][]map[string]any{"check unit": {{"sleep": "30s"}}}})
	start := time.Now()
	r, err := w.e.check(&State{ID: "r1"}, "all")
	if err != nil || r["status"] != "failed" || r["summary"] != "1 of 2 failed: unit" || toInt(r["exit_code"]) != 124 ||
		!strings.Contains(str(r["output"]), "timed out after 300ms") {
		t.Fatalf("check %v %v", r, err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("check took %v", d)
	}
	if n := len(w.find("check", "lint")); n != 1 {
		t.Errorf("lint ran %d times", n)
	}
	if !w.childGone() {
		t.Errorf("the check's child outlived the timeout")
	}
}

// A session that hangs times out, and the run escalates as for any other
// failed step.
func TestSessionTimesOutAndEscalates(t *testing.T) {
	w := newWorld(t, todoTicket())
	w.e.cfg.SessionTimeout = 300 * time.Millisecond
	hung := reply("done", map[string]any{"plan": "p", "risks": "r"})
	hung["sleep"] = "30s"
	w.script(fakeConfig{Harness: []map[string]any{hung, reply("done", map[string]any{"happened": "plan hung"})}})
	r := w.run(RunOpts{Ref: "build-ticket", Ticket: "T-1"})
	if r.Status != StatusBlocked || r.Step != "plan" || !strings.Contains(r.Reason, "session failed: timed out after 300ms") {
		t.Fatalf("result %+v", r)
	}
	tk := w.ticket("T-1")
	if tk.Status != "blocked" || tk.Claim != nil || !strings.HasPrefix(tk.Comments[len(tk.Comments)-1].Body, "Escalation: plan hung") {
		t.Errorf("ticket %s claim %+v comments %+v", tk.Status, tk.Claim, tk.Comments)
	}
	if !w.childGone() {
		t.Errorf("the session's child outlived the timeout")
	}
	// A blocked run's next try starts a new session, so its temp folder goes.
	if s := w.sessions(); len(s) == 0 || s[0].Tmp == "missing" {
		t.Errorf("sessions %+v", s)
	} else if _, err := os.Stat(s[0].Tmp); !os.IsNotExist(err) {
		t.Errorf("tmp kept: %v", err)
	}
}

// A push that hangs fails open pr with "timed out after", and no PR opens.
func TestPushTimesOut(t *testing.T) {
	w := newWorld(t)
	st := &State{ID: "r1", Ticket: "T-1"}
	wt, err := w.e.worktreeAdd(st, "T-1", "main")
	if err != nil || wt["status"] != "ok" {
		t.Fatalf("worktree add %v %v", wt, err)
	}
	st.Worktree, st.Branch = str(wt["path"]), str(wt["branch"])
	os.WriteFile(filepath.Join(st.Worktree, "change.txt"), []byte("x\n"), 0o644)
	w.git(st.Worktree, "add", "change.txt")
	w.git(st.Worktree, "commit", "-q", "-m", "change")
	w.e.cfg.GitTimeout = 300 * time.Millisecond
	w.script(fakeConfig{Replies: map[string][]map[string]any{"git push": {{"sleep": "30s"}}}})
	r, err := w.e.openPR(st, "", "")
	if err != nil || r["status"] != "failed" || toInt(r["exit_code"]) != 124 ||
		!strings.HasPrefix(str(r["output"]), "git push: ") || !strings.Contains(str(r["output"]), "timed out after 300ms") {
		t.Fatalf("open pr %v %v", r, err)
	}
	if n := len(w.find("gh", "pr", "create")); n != 0 {
		t.Errorf("%d PRs created", n)
	}
	if !w.childGone() {
		t.Errorf("the push's child outlived the timeout")
	}
}

// TestRunNext: run with no --ticket takes the first unclaimed ticket intake
// matches, entry by entry: a sent-back ticket before new work, and past a
// ticket whose claim fails.
func TestRunNext(t *testing.T) {
	ticket := func(id, status string, priority int, latest string) *fakeTicketT {
		tk := &fakeTicketT{ID: id, Title: "t " + id, Body: "Type: feature", Type: "ship", Status: status, Role: "engineer",
			Owner: "chief-of-staff", Priority: priority, Version: 1}
		if latest != "" {
			tk.Comments = []fakeComment{{ID: 1, Author: "reviewer", Body: latest}}
		}
		return tk
	}
	t.Run("sent back first", func(t *testing.T) {
		held := ticket("T-4", "in_progress", 0, "Kickback: 1 blocker")
		held.Claim = &fakeClaim{Holder: "eng-2/s", Token: 9}
		w := newWorldAt(t, "testdata/work",
			ticket("T-1", "todo", 0, ""),
			ticket("T-2", "in_progress", 2, "Kickback: 1 blocker"),
			ticket("T-3", "in_progress", 1, "Handoff: PR 7"), // not sent back
			held)
		w.script(fakeConfig{Harness: blockedReplies})
		r := w.run(RunOpts{Ref: "work"})
		if r.Ticket != "T-2" {
			t.Fatalf("took %q, want T-2: %+v", r.Ticket, r)
		}
		if c := w.find("ticket", "claim"); len(c) != 1 || c[0].Args[1] != "T-2" {
			t.Errorf("claims %+v", c)
		}
	})
	t.Run("past a lost race", func(t *testing.T) {
		w := newWorldAt(t, "testdata/work", ticket("T-1", "todo", 0, ""), ticket("T-2", "todo", 1, ""))
		w.script(fakeConfig{Harness: blockedReplies, Replies: map[string][]map[string]any{
			"ticket claim": {{"exit": 4, "code": "claimed"}, {}}, // the last reply repeats; {} lets it through
		}})
		r := w.run(RunOpts{Ref: "work"})
		if r.Ticket != "T-2" {
			t.Fatalf("took %q, want T-2: %+v", r.Ticket, r)
		}
	})
	t.Run("nothing to take", func(t *testing.T) {
		w := newWorldAt(t, "testdata/work", ticket("T-1", "in_review", 0, ""))
		r := w.run(RunOpts{Ref: "work"})
		if r.Status != StatusFailed || !strings.Contains(r.Reason, "no unclaimed ticket matches") {
			t.Errorf("result %+v", r)
		}
		if n := len(w.find("ticket", "claim")); n != 0 {
			t.Errorf("%d claims", n)
		}
	})
}
