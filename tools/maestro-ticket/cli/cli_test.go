package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// When re-executed with this variable set, the test binary acts as the
// maestro-ticket CLI. The multi-process race test uses this.
const childEnv = "MAESTRO_TICKET_CLI_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		os.Exit(Main(os.Args[1:]))
	}
	os.Exit(m.Run())
}

type harness struct {
	t   *testing.T
	dir string
	env map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	return &harness{t: t, dir: dir, env: map[string]string{"MAESTRO_TICKET_STORE": filepath.Join(dir, "tickets.db")}}
}

func (h *harness) run(stdin string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(context.Background(), args, Env{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string { return h.env[k] }, Dir: h.dir,
	})
	return code, out.String(), errb.String()
}

// ok runs a command that must succeed and decodes its JSON output.
func (h *harness) ok(v any, args ...string) {
	h.t.Helper()
	code, out, errs := h.run("", append(args, "--json")...)
	if code != 0 {
		h.t.Fatalf("%v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errs)
	}
	if v != nil {
		if err := json.Unmarshal([]byte(out), v); err != nil {
			h.t.Fatalf("%v: bad json %v: %s", args, err, out)
		}
	}
}

// fails runs a command that must exit with code and returns the JSON error code.
func (h *harness) fails(code int, args ...string) string {
	h.t.Helper()
	got, out, errs := h.run("", append(args, "--json")...)
	if got != code {
		h.t.Fatalf("%v: exit %d; want %d\nstdout: %s\nstderr: %s", args, got, code, out, errs)
	}
	var e struct {
		Error struct{ Code, Message string }
	}
	json.Unmarshal([]byte(out), &e)
	return e.Error.Code
}

type tk struct {
	ID      string
	Status  string
	Role    string
	Version int64
	Claim   *struct {
		Holder  string
		Expired bool
	}
	Comments []struct{ Body, Author string }
}

func TestHelpAndUsage(t *testing.T) {
	h := newHarness(t)
	if code, out, _ := h.run("", "help"); code != 0 || !strings.Contains(out, "Exit codes") {
		t.Fatalf("help: %d %q", code, out)
	}
	if code, _, _ := h.run(""); code != 2 {
		t.Fatalf("no args: %d", code)
	}
	if code, _, errs := h.run("", "frobnicate"); code != 2 || !strings.Contains(errs, "unknown command") {
		t.Fatalf("unknown: %d %q", code, errs)
	}
	if code, _, _ := h.run("", "list", "--nope"); code != 2 {
		t.Fatalf("bad flag: %d", code)
	}
	if code, _, _ := h.run("", "list", "-h"); code != 0 {
		t.Fatalf("-h: %d", code)
	}
}

func TestLifecycleJSON(t *testing.T) {
	h := newHarness(t)
	h.env["MAESTRO_TICKET_ACTOR"] = "planner"
	var a tk
	h.ok(&a, "create", "--title", "Build login", "--role", "junior-software-engineer", "--priority", "1")
	if a.ID != "T-1" || a.Status != "todo" || a.Version != 1 {
		t.Fatalf("create %+v", a)
	}

	var c tk
	h.ok(&c, "claim", "--next", "--holder", "jse/s1", "--role", "junior-software-engineer", "--ttl", "10m")
	if c.ID != "T-1" || c.Claim == nil || c.Claim.Holder != "jse/s1" || c.Version != 2 {
		t.Fatalf("claim %+v", c)
	}
	if code := h.fails(5, "claim", "--next", "--holder", "jse/s2"); code != "no_work" {
		t.Fatalf("no work code %q", code)
	}
	if code := h.fails(4, "claim", "T-1", "--holder", "sse/s1"); code != "claimed" {
		t.Fatalf("claimed code %q", code)
	}

	// Positional id before or after flags.
	var tr tk
	h.ok(&tr, "transition", "T-1", "--to", "in_progress", "--holder", "jse/s1", "--expect-version", "2")
	if tr.Status != "in_progress" || tr.Version != 3 {
		t.Fatalf("transition %+v", tr)
	}
	if code := h.fails(4, "transition", "--to", "in_review", "--holder", "jse/s1", "--expect-version", "2", "1"); code != "version_conflict" {
		t.Fatalf("version conflict code %q", code)
	}
	if code := h.fails(4, "transition", "1", "--from", "todo", "--to", "in_review", "--holder", "jse/s1"); code != "status_conflict" {
		t.Fatalf("status conflict code %q", code)
	}
	if code := h.fails(2, "transition", "1", "--to", "in_qa", "--holder", "jse/s1"); code != "invalid_transition" {
		t.Fatalf("invalid transition code %q", code)
	}
	if code := h.fails(2, "transition", "1", "--holder", "jse/s1"); code != "usage" {
		t.Fatalf("missing --to code %q", code)
	}
	h.ok(nil, "heartbeat", "1", "--holder", "jse/s1", "--ttl", "20m")
	if code := h.fails(4, "heartbeat", "1", "--holder", "jse/s2"); code != "lease_lost" {
		t.Fatalf("lease lost code %q", code)
	}
	h.fails(2, "heartbeat", "1", "--holder", "jse/s1", "--idempotency-key", "k")

	if code, _, errs := h.run("handoff via stdin\n", "comment", "T-1", "--body-file", "-"); code != 0 {
		t.Fatalf("comment: %d %s", code, errs)
	}
	h.ok(nil, "transition", "T-1", "--to", "in_review", "--holder", "jse/s1", "--release", "--note", "checks pass")
	h.ok(nil, "release", "T-1", "--holder", "jse/s1") // second release is a no-op

	var got tk
	h.ok(&got, "get", "T-1")
	if got.Status != "in_review" || got.Claim != nil || len(got.Comments) != 1 ||
		got.Comments[0].Body != "handoff via stdin\n" || got.Comments[0].Author != "planner" {
		t.Fatalf("get %+v", got)
	}
	if code := h.fails(3, "get", "T-99"); code != "not_found" {
		t.Fatalf("not found code %q", code)
	}
	h.fails(2, "get", "nonsense")

	var list struct{ Tickets []tk }
	h.ok(&list, "list", "--status", "in_review,in_qa", "--unclaimed")
	if len(list.Tickets) != 1 {
		t.Fatalf("list %+v", list)
	}
	h.fails(2, "list", "--status", "bogus")
	h.fails(2, "list", "--claimed", "--unclaimed")
}

func TestIdempotencyKeyFlag(t *testing.T) {
	h := newHarness(t)
	var a, b tk
	h.ok(&a, "create", "--title", "x", "--idempotency-key", "cos/s1:1")
	h.ok(&b, "create", "--title", "x", "--idempotency-key", "cos/s1:1")
	if a.ID != b.ID {
		t.Fatalf("replay made a new ticket: %s %s", a.ID, b.ID)
	}
	if code := h.fails(4, "create", "--title", "y", "--idempotency-key", "cos/s1:1"); code != "idempotency_conflict" {
		t.Fatalf("code %q", code)
	}
}

func TestEventsCursor(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.ok(nil, "create", "--title", "b")
	var page struct {
		Events []struct {
			Seq  int64
			Type string
		}
		NextSeq int64 `json:"next_seq"`
	}
	h.ok(&page, "events")
	if len(page.Events) != 2 || page.NextSeq != 2 {
		t.Fatalf("events %+v", page)
	}
	cursor := page.NextSeq
	h.ok(&page, "events", "--since", fmt.Sprint(cursor))
	if len(page.Events) != 0 || page.NextSeq != cursor {
		t.Fatalf("idle wake gate should see nothing: %+v", page)
	}
	h.ok(nil, "comment", "T-1", "--body", "hi")
	h.ok(&page, "events", "--since", fmt.Sprint(cursor))
	if len(page.Events) != 1 || page.Events[0].Type != "commented" || page.NextSeq != 3 {
		t.Fatalf("after write %+v", page)
	}
	if code, out, _ := h.run("", "events"); code != 0 || !strings.Contains(out, "next_seq: 3") {
		t.Fatalf("human events: %q", out)
	}
}

func TestHumanOutput(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("", "create", "--title", "Readable", "--role", "qa-lead")
	if code != 0 || !strings.HasPrefix(out, "created T-1 [todo] P2 v1 Readable") {
		t.Fatalf("create: %q", out)
	}
	_, out, _ = h.run("", "list")
	if !strings.Contains(out, "ID") || !strings.Contains(out, "qa-lead") {
		t.Fatalf("list: %q", out)
	}
	code, _, errs := h.run("", "get", "T-9")
	if code != 3 || !strings.HasPrefix(errs, "maestro-ticket: not found") {
		t.Fatalf("human error: %d %q", code, errs)
	}
}

func TestStorePrecedence(t *testing.T) {
	h := newHarness(t)
	other := filepath.Join(h.dir, "other.db")
	h.ok(nil, "create", "--title", "in env store")
	h.ok(nil, "create", "--title", "in flag store", "--store", other)
	var l struct{ Tickets []struct{ Title string } }
	h.ok(&l, "list", "--store", other)
	if len(l.Tickets) != 1 || l.Tickets[0].Title != "in flag store" {
		t.Fatalf("flag store %+v", l)
	}
	h.ok(&l, "list")
	if len(l.Tickets) != 1 || l.Tickets[0].Title != "in env store" {
		t.Fatalf("env store %+v", l)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestGitDefaultsSharedAcrossWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", "-b", "maestro/T-1-x", wt)

	main := &harness{t: t, dir: repo, env: map[string]string{}}
	linked := &harness{t: t, dir: wt, env: map[string]string{}}
	main.ok(nil, "create", "--title", "shared")

	var l struct{ Tickets []tk }
	linked.ok(&l, "list")
	if len(l.Tickets) != 1 {
		t.Fatalf("linked worktree should see the main repo's store: %+v", l)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "maestro", "tickets.db")); err != nil {
		t.Fatalf("store not in git common dir: %v", err)
	}

	var doc struct {
		OK    bool
		Store string
	}
	linked.ok(&doc, "doctor")
	if !doc.OK || !strings.HasSuffix(doc.Store, filepath.Join(".git", "maestro", "tickets.db")) {
		t.Fatalf("doctor %+v", doc)
	}

	var r struct{ Path string }
	linked.ok(&r, "render")
	if r.Path != filepath.Join(wt, ".maestro", "Backlog.md") {
		t.Fatalf("render path %q", r.Path)
	}
	b, err := os.ReadFile(r.Path)
	if err != nil || !strings.HasPrefix(string(b), "<!-- generated by maestro-ticket render, do not edit -->") ||
		!strings.Contains(string(b), "shared") {
		t.Fatalf("backlog: %v\n%s", err, b)
	}

	outside := &harness{t: t, dir: t.TempDir(), env: map[string]string{}}
	if code := outside.fails(2, "list"); code != "invalid" {
		t.Fatalf("outside git: %q", code)
	}
}

func TestRenderOut(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "x")
	var r struct {
		Path    string
		Tickets int
		LastSeq int64 `json:"last_seq"`
	}
	h.ok(&r, "render", "--out", "view/Backlog.md")
	if r.Path != filepath.Join(h.dir, "view", "Backlog.md") || r.Tickets != 1 || r.LastSeq != 1 {
		t.Fatalf("render %+v", r)
	}
}

// TestRaceClaimProcesses runs 200 real maestro-ticket processes claiming from 50
// tickets at once.
func TestRaceClaimProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns 200 processes")
	}
	const tickets, procs = 50, 200
	h := newHarness(t)
	for i := 0; i < tickets; i++ {
		h.ok(nil, "create", "--title", fmt.Sprint("t", i), "--priority", fmt.Sprint(i%4))
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		code   int
		id     string
		holder string
		stderr string
	}
	results := make([]result, procs)
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(procs)
	for i := 0; i < procs; i++ {
		go func() {
			defer done.Done()
			holder := fmt.Sprintf("bot-%d/session-%d", i%5, i)
			cmd := exec.Command(exe, "claim", "--next", "--holder", holder, "--json")
			cmd.Env = append(os.Environ(), childEnv+"=1", "MAESTRO_TICKET_STORE="+h.env["MAESTRO_TICKET_STORE"])
			var out, errb bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errb
			start.Wait()
			err := cmd.Run()
			r := result{holder: holder, stderr: errb.String()}
			if ee, ok := err.(*exec.ExitError); ok {
				r.code = ee.ExitCode()
			} else if err != nil {
				r.code = -1
				r.stderr = err.Error()
			}
			var tk struct{ ID string }
			if r.code == 0 && json.Unmarshal(out.Bytes(), &tk) == nil {
				r.id = tk.ID
			}
			results[i] = r
		}()
	}
	start.Done()
	done.Wait()

	won := map[string]string{}
	noWork := 0
	for _, r := range results {
		switch r.code {
		case 0:
			if prev, dup := won[r.id]; dup {
				t.Errorf("double claim of %s by %s and %s", r.id, prev, r.holder)
			}
			won[r.id] = r.holder
		case 5:
			noWork++
		default:
			t.Errorf("%s: exit %d: %s", r.holder, r.code, r.stderr)
		}
	}
	if len(won) != tickets || noWork != procs-tickets {
		t.Fatalf("claimed %d, no-work %d; want %d, %d", len(won), noWork, tickets, procs-tickets)
	}
	var l struct{ Tickets []tk }
	h.ok(&l, "list", "--claimed")
	for _, x := range l.Tickets {
		if x.Claim.Holder != won[x.ID] || x.Version != 2 {
			t.Errorf("%s: store says %s v%d, process saw %s", x.ID, x.Claim.Holder, x.Version, won[x.ID])
		}
	}
	if len(l.Tickets) != tickets {
		t.Fatalf("store has %d claimed tickets; want %d", len(l.Tickets), tickets)
	}
	t.Logf("%d processes, %d tickets: %d claimed, %d no-work, 0 double claims", procs, tickets, len(won), noWork)
}
