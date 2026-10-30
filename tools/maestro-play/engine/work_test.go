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
		bin := filepath.Join(w.dir, "bin", "maestro-ticket-real")
		build := exec.Command("go", "build", "-o", bin, "./cmd/maestro-ticket")
		build.Dir = "../../maestro-ticket"
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build maestro-ticket: %v\n%s", err, out)
		}
		w.e.cfg.TicketBin = bin
		rt := realTickets{w, bin}
		id := str(rt.call("create", "--title", "Add --json", "--body", "Type: feature\nCriteria: C1", "--role", "engineer",
			"--status", "todo", "--actor", "chief-of-staff")["id"])
		workEndToEnd(t, w, rt, id)
	})
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

// pr status reads gh's state, head and merge commit.
func TestPRStatus(t *testing.T) {
	w := newWorld(t)
	w.script(fakeConfig{Replies: map[string][]map[string]any{"gh pr view": {
		{"state": "MERGED", "headRefOid": "abc1234", "mergeCommit": map[string]any{"oid": "def5678"}, "url": "u"},
		{"exit": 1, "stderr": "no pull requests found"},
	}}})
	st := &State{ID: "r1", Branch: "maestro/T-1"}
	r, err := w.e.prStatus(st, st.Branch)
	if err != nil || r["status"] != "ok" || r["state"] != "merged" || r["sha"] != "abc1234" || r["merge_sha"] != "def5678" || r["url"] != "u" {
		t.Fatalf("pr status %v %v", r, err)
	}
	if r, _ := w.e.prStatus(st, "7"); r["status"] != "failed" || !strings.Contains(str(r["output"]), "no pull requests") {
		t.Errorf("missing PR: %v", r)
	}
	if r, _ := w.e.prStatus(&State{ID: "r1"}, ""); r["status"] != "failed" {
		t.Errorf("no branch: %v", r)
	}
	if r, _ := w.e.openPR(&State{ID: "r1"}, "", ""); r["status"] != "failed" || !strings.Contains(str(r["output"]), "worktree add first") {
		t.Errorf("open pr with no branch: %v", r)
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
