package engine

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	// The repo's own playbooks/engineer/work.yaml and prompts, on the same
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
	r := w.run(RunOpts{Ref: "engineer/work", Ticket: id})
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
	r = w.run(RunOpts{Ref: "engineer/work", Ticket: id})
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

// A check that isn't listed, or a setup with no checks, fails the step.
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
	if r, _ := w.e.check(st, "all"); r["status"] != "failed" || !strings.Contains(str(r["output"]), "no checks under ## Checks") {
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
	if s.Name != "tiny" || s.MainBranch != "trunk" || len(s.Checks) != 2 || s.Checks[0].Name != "unit" || s.Checks[1].Command != "go vet ./..." {
		t.Errorf("setup %+v", s)
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
		r := w.run(RunOpts{Ref: "engineer/work", Ticket: "T-1"})
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
		r := w.run(RunOpts{Ref: "engineer/work", Ticket: "T-1"})
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
		r := w.run(RunOpts{Ref: "engineer/work", Ticket: id})
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
