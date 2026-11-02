package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
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
	now func() time.Time // store clock; nil means the database's
	// checks replaces the store-directory checks when set.
	checks func(dir string, allow bool) []ticket.Check
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
		Getenv: func(k string) string { return h.env[k] }, Dir: h.dir, Now: h.now, StoreChecks: h.checks,
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
		Token   int64
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
	h.env["MAESTRO_TICKET_ACTOR"] = "cos"
	var a tk
	h.ok(&a, "create", "--title", "Build login", "--role", "engineer", "--priority", "1")
	if a.ID != "T-1" || a.Status != "todo" || a.Version != 1 {
		t.Fatalf("create %+v", a)
	}

	var c tk
	h.ok(&c, "claim", "--next", "--holder", "jse/s1", "--role", "engineer", "--ttl", "10m")
	if c.ID != "T-1" || c.Claim == nil || c.Claim.Holder != "jse/s1" || c.Claim.Token != 1 || c.Version != 2 {
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
	h.ok(&tr, "transition", "T-1", "--to", "in_progress", "--holder", "jse/s1", "--token", "1", "--expect-version", "2")
	if tr.Status != "in_progress" || tr.Version != 3 {
		t.Fatalf("transition %+v", tr)
	}
	if code := h.fails(4, "transition", "--to", "in_review", "--holder", "jse/s1", "--token", "1", "--expect-version", "2", "1"); code != "version_conflict" {
		t.Fatalf("version conflict code %q", code)
	}
	if code := h.fails(4, "transition", "1", "--from", "todo", "--to", "in_review", "--holder", "jse/s1", "--token", "1"); code != "status_conflict" {
		t.Fatalf("status conflict code %q", code)
	}
	if code := h.fails(2, "transition", "1", "--to", "done", "--holder", "jse/s1", "--token", "1"); code != "invalid_transition" {
		t.Fatalf("invalid transition code %q", code)
	}
	if code := h.fails(2, "transition", "1", "--holder", "jse/s1"); code != "usage" {
		t.Fatalf("missing --to code %q", code)
	}
	h.ok(nil, "heartbeat", "1", "--holder", "jse/s1", "--token", "1", "--ttl", "20m")
	if code := h.fails(4, "heartbeat", "1", "--holder", "jse/s2", "--token", "1"); code != "lease_lost" {
		t.Fatalf("lease lost code %q", code)
	}
	if code := h.fails(4, "heartbeat", "1", "--holder", "jse/s1", "--token", "7"); code != "lease_lost" {
		t.Fatalf("stale heartbeat code %q", code)
	}
	if code := h.fails(2, "heartbeat", "1", "--holder", "jse/s1"); code != "invalid" {
		t.Fatalf("heartbeat without token code %q", code)
	}
	h.fails(2, "heartbeat", "1", "--holder", "jse/s1", "--token", "1", "--idempotency-key", "k")

	// Fencing: a claimed ticket takes writes only from its holder and token.
	if code := h.fails(4, "comment", "T-1", "--body", "drive-by"); code != "claimed" {
		t.Fatalf("unfenced comment code %q", code)
	}
	if code := h.fails(2, "comment", "T-1", "--body", "x", "--holder", "jse/s1"); code != "invalid" {
		t.Fatalf("comment without token code %q", code)
	}
	if code := h.fails(4, "transition", "1", "--to", "in_review", "--holder", "jse/s1", "--token", "7"); code != "lease_lost" {
		t.Fatalf("stale token code %q", code)
	}
	if code, _, errs := h.run("handoff via stdin\n", "comment", "T-1", "--body-file", "-", "--holder", "jse/s1", "--token", "1"); code != 0 {
		t.Fatalf("comment: %d %s", code, errs)
	}
	if code := h.fails(4, "release", "T-1", "--holder", "jse/s1", "--token", "7"); code != "lease_lost" {
		t.Fatalf("stale release code %q", code)
	}
	h.ok(nil, "transition", "T-1", "--to", "in_review", "--holder", "jse/s1", "--token", "1", "--release", "--note", "checks pass")
	h.ok(nil, "release", "T-1", "--holder", "jse/s1", "--token", "1") // second release is a no-op
	if code := h.fails(2, "release", "T-1", "--holder", "jse/s1"); code != "invalid" {
		t.Fatalf("release without token code %q", code)
	}

	var got tk
	h.ok(&got, "get", "T-1")
	if got.Status != "in_review" || got.Claim != nil || len(got.Comments) != 1 ||
		got.Comments[0].Body != "handoff via stdin\n" || got.Comments[0].Author != "cos" {
		t.Fatalf("get %+v", got)
	}
	if code := h.fails(3, "get", "T-99"); code != "not_found" {
		t.Fatalf("not found code %q", code)
	}
	h.fails(2, "get", "nonsense")

	var list struct{ Tickets []tk }
	h.ok(&list, "list", "--status", "in_review,blocked", "--unclaimed")
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
		t.Fatalf("an unchanged cursor should see nothing: %+v", page)
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
	code, out, _ := h.run("", "create", "--title", "Readable", "--role", "reviewer")
	if code != 0 || !strings.HasPrefix(out, "created T-1 [todo] P2 v1 Readable") {
		t.Fatalf("create: %q", out)
	}
	_, out, _ = h.run("", "list")
	if !strings.Contains(out, "ID") || !strings.Contains(out, "reviewer") {
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

	// Render from an agent branch's worktree writes next to the store,
	// in the git common dir, and leaves every work tree clean.
	var r struct{ Path string }
	linked.ok(&r, "render")
	if r.Path != filepath.Join(repo, ".git", "maestro", "Backlog.md") {
		t.Fatalf("render path %q", r.Path)
	}
	main.ok(nil, "render")
	for _, dir := range []string{repo, wt} {
		if st := git(t, dir, "status", "--porcelain", "--untracked-files=all"); st != "" {
			t.Fatalf("render left changes in %s:\n%s", dir, st)
		}
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

func TestCreateTypeOwnerBacklog(t *testing.T) {
	h := newHarness(t)
	var sc struct {
		ID, Type, Status, Owner string
		CreatedBy               string `json:"created_by"`
	}
	h.ok(&sc, "create", "--title", "investigate", "--type", "scout", "--status", "backlog", "--owner", "chief-of-staff", "--actor", "cos")
	if sc.Type != "scout" || sc.Status != "backlog" || sc.Owner != "chief-of-staff" || sc.CreatedBy != "cos" {
		t.Fatalf("create scout %+v", sc)
	}
	if code := h.fails(2, "create", "--title", "x", "--type", "epic"); code != "invalid" {
		t.Fatalf("bad type code %q", code)
	}
	if code := h.fails(2, "create", "--title", "x", "--type", "scout"); code != "invalid" {
		t.Fatalf("scout without owner code %q", code)
	}
	h.fails(2, "claim", "--next", "--status", "backlog", "--holder", "cos/s1")
	h.fails(5, "claim", "--next", "--holder", "cos/s1")
	var blk struct {
		Status      string
		BlockedFrom string `json:"blocked_from"`
	}
	h.ok(&blk, "create", "--title", "waits", "--status", "blocked")
	if blk.BlockedFrom != "todo" {
		t.Fatalf("created blocked %+v", blk)
	}
	if code, out, _ := h.run("", "get", "T-1"); code != 0 || !strings.Contains(out, "type:     scout") || !strings.Contains(out, "owner:    chief-of-staff") {
		t.Fatalf("human get: %q", out)
	}
	if code, out, _ := h.run("", "get", "T-2"); code != 0 || !strings.Contains(out, "status:   blocked (from todo)") {
		t.Fatalf("human get blocked: %q", out)
	}
}

func TestDoneGateFlags(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "ship it")
	for _, to := range []string{"in_progress", "in_review"} {
		h.ok(nil, "transition", "T-1", "--to", to)
	}
	if code := h.fails(2, "transition", "T-1", "--to", "done", "--actor-role", "engineer"); code != "forbidden" {
		t.Fatalf("engineer done code %q", code)
	}
	if code := h.fails(2, "transition", "T-1", "--to", "done", "--actor-role", "release-manager"); code != "invalid" {
		t.Fatalf("unknown role code %q", code)
	}
	h.env["MAESTRO_TICKET_ROLE"] = "reviewer"
	var d tk
	h.ok(&d, "transition", "T-1", "--to", "done", "--from", "in_review", "--note", "merged abc123")
	if d.Status != "done" {
		t.Fatalf("reviewer done %+v", d)
	}

	h.ok(nil, "create", "--title", "scout", "--type", "scout", "--owner", "chief-of-staff")
	h.ok(nil, "transition", "T-2", "--to", "in_progress")
	h.ok(nil, "transition", "T-2", "--to", "in_review")
	if code := h.fails(2, "transition", "T-2", "--to", "done", "--actor", "rev"); code != "forbidden" {
		t.Fatalf("reviewer closing a scout code %q", code)
	}
	h.ok(&d, "transition", "T-2", "--to", "done", "--actor", "chief-of-staff")
	if d.Status != "done" {
		t.Fatalf("owner done %+v", d)
	}
}

func TestReapCommand(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "x")
	h.ok(nil, "claim", "T-1", "--holder", "jse/s1", "--ttl", "5s")
	now := time.Now().Add(time.Minute)
	h.now = func() time.Time { return now }
	var r struct{ Reaped []string }
	h.ok(&r, "reap")
	if len(r.Reaped) != 1 || r.Reaped[0] != "T-1" {
		t.Fatalf("reap %+v", r)
	}
	var page struct {
		Events []struct{ Type, Actor string }
	}
	h.ok(&page, "events", "--type", "lease_expired")
	if len(page.Events) != 1 || page.Events[0].Actor != "maestro-ticket" {
		t.Fatalf("lease_expired events %+v", page)
	}
	if code, out, _ := h.run("", "reap"); code != 0 || !strings.Contains(out, "reaped 0") {
		t.Fatalf("human reap: %d %q", code, out)
	}
}

// The git default refused nothing. A bare repo got a store and doctor
// said ok; a submodule silently got its own store.
func TestGitDefaultRefusesBareAndSubmodule(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	bare := filepath.Join(t.TempDir(), "bare.git")
	git(t, filepath.Dir(bare), "init", "-q", "--bare", bare)
	b := &harness{t: t, dir: bare, env: map[string]string{}}
	if code := b.fails(2, "doctor"); code != "invalid" {
		t.Fatalf("doctor in bare repo: %q", code)
	}
	b.fails(2, "list")
	if _, err := os.Stat(filepath.Join(bare, "maestro")); !os.IsNotExist(err) {
		t.Fatalf("bare repo got a store dir: %v", err)
	}
	// --store still works from anywhere.
	b.env["MAESTRO_TICKET_STORE"] = filepath.Join(t.TempDir(), "tickets.db")
	b.ok(nil, "list")

	lib := t.TempDir()
	git(t, lib, "init", "-q")
	git(t, lib, "commit", "-q", "--allow-empty", "-m", "lib")
	super := t.TempDir()
	git(t, super, "init", "-q")
	git(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "lib")
	sub := &harness{t: t, dir: filepath.Join(super, "lib"), env: map[string]string{}}
	code, out, _ := sub.run("", "list", "--json")
	if code != 2 || !strings.Contains(out, "submodule") {
		t.Fatalf("list in submodule: exit %d %s", code, out)
	}
	top := &harness{t: t, dir: super, env: map[string]string{}}
	top.ok(nil, "list")
}

// A relative --store resolved against the process directory, not the
// directory the CLI was asked to run in.
func TestRelativeStoreFromDir(t *testing.T) {
	h := &harness{t: t, dir: t.TempDir(), env: map[string]string{}}
	h.ok(nil, "create", "--title", "x", "--store", "rel/tickets.db")
	if _, err := os.Stat(filepath.Join(h.dir, "rel", "tickets.db")); err != nil {
		t.Fatalf("store not under Env.Dir: %v", err)
	}
	var doc struct {
		OK     bool
		Checks []struct {
			Name string
			OK   bool
		}
	}
	h.ok(&doc, "doctor", "--store", "rel/tickets.db")
	names := map[string]bool{}
	for _, c := range doc.Checks {
		names[c.Name] = c.OK
	}
	if !doc.OK || !names["filesystem"] || !names["locks"] {
		t.Fatalf("doctor checks %+v", doc.Checks)
	}
}

func TestBackupAndExport(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "keep me")
	h.ok(nil, "comment", "T-1", "--body", "note")
	clock := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return clock }
	var paths []string
	for i := 0; i < 3; i++ {
		var r struct {
			Path   string
			Pruned []string
		}
		h.ok(&r, "backup", "--keep", "2")
		paths = append(paths, r.Path)
		clock = clock.Add(time.Hour)
	}
	if filepath.Dir(paths[0]) != filepath.Join(h.dir, "backups") {
		t.Fatalf("backup dir %s", filepath.Dir(paths[0]))
	}
	left, _ := filepath.Glob(filepath.Join(h.dir, "backups", "*"))
	if len(left) != 2 || left[0] != paths[1] || left[1] != paths[2] {
		t.Fatalf("after pruning: %v; want the two newest of %v", left, paths)
	}
	// A snapshot is a working store.
	b := &harness{t: t, dir: h.dir, env: map[string]string{"MAESTRO_TICKET_STORE": paths[2]}}
	var got tk
	b.ok(&got, "get", "T-1")
	if len(got.Comments) != 1 {
		t.Fatalf("snapshot %+v", got)
	}
	// Same name twice is refused, not overwritten.
	clock = clock.Add(-time.Hour)
	h.fails(2, "backup")

	// Pruning touches only snapshot names: not a store or another file that
	// happens to match tickets-*.db in the same folder.
	data := filepath.Join(h.dir, "data")
	os.MkdirAll(data, 0o755)
	os.WriteFile(filepath.Join(data, "tickets-archive-2019.db"), []byte("x"), 0o644)
	live := &harness{t: t, dir: h.dir, env: map[string]string{"MAESTRO_TICKET_STORE": filepath.Join(data, "tickets-1.db")}}
	live.ok(nil, "create", "--title", "precious")
	live.ok(nil, "backup", "--dir", "data", "--keep", "1")
	live.ok(nil, "backup", "--dir", "data", "--keep", "1")
	for _, f := range []string{"tickets-1.db", "tickets-archive-2019.db"} {
		if _, err := os.Stat(filepath.Join(data, f)); err != nil {
			t.Fatalf("pruning removed %s: %v", f, err)
		}
	}
	var one tk
	live.ok(&one, "get", "T-1")
	// A store named like a snapshot, reached through a symlinked folder,
	// is still the store.
	snapName := "tickets-20200101T000000.000Z.db"
	os.Rename(filepath.Join(data, "tickets-1.db"), filepath.Join(data, snapName))
	os.Remove(filepath.Join(data, "tickets-1.db-wal"))
	os.Remove(filepath.Join(data, "tickets-1.db-shm"))
	if err := os.Symlink(data, filepath.Join(h.dir, "link")); err != nil {
		t.Fatal(err)
	}
	linked := &harness{t: t, dir: h.dir, env: map[string]string{"MAESTRO_TICKET_STORE": filepath.Join(h.dir, "link", snapName)}}
	linked.ok(nil, "backup", "--dir", "data", "--keep", "1")
	if _, err := os.Stat(filepath.Join(data, snapName)); err != nil {
		t.Fatalf("pruning removed the store through a symlink: %v", err)
	}

	var ex struct {
		SchemaVersion int   `json:"schema_version"`
		LastSeq       int64 `json:"last_seq"`
		Tickets       []tk
		Events        []struct{ Type string }
	}
	code, out, _ := h.run("", "export")
	if code != 0 || json.Unmarshal([]byte(out), &ex) != nil {
		t.Fatalf("export: %d %s", code, out)
	}
	if ex.SchemaVersion < 3 || ex.LastSeq != 2 || len(ex.Tickets) != 1 || len(ex.Tickets[0].Comments) != 1 || len(ex.Events) != 2 {
		t.Fatalf("export %+v", ex)
	}
	var r struct{ Path string }
	h.ok(&r, "export", "--out", "dump.json")
	if r.Path != filepath.Join(h.dir, "dump.json") {
		t.Fatalf("export path %q", r.Path)
	}
}

func TestDepsAndAreasFlags(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "base", "--areas", "tools/maestro-ticket")
	var tk2 struct {
		DependsOn []string `json:"depends_on"`
		Areas     []string
	}
	h.ok(&tk2, "create", "--title", "next", "--priority", "0", "--deps", "T-1", "--areas", "skills/,docs")
	if fmt.Sprint(tk2.DependsOn) != "[T-1]" || fmt.Sprint(tk2.Areas) != "[docs skills]" {
		t.Fatalf("create %+v", tk2)
	}
	if code := h.fails(4, "claim", "T-2", "--holder", "jse/a"); code != "waiting_on_deps" {
		t.Fatalf("claim waiting ticket: %q", code)
	}
	h.ok(nil, "claim", "--next", "--holder", "jse/a") // T-1
	h.ok(nil, "create", "--title", "same area", "--areas", "tools/maestro-ticket/cli")
	if code := h.fails(4, "claim", "T-3", "--holder", "jse/b"); code != "area_busy" {
		t.Fatalf("claim busy area: %q", code)
	}
	h.fails(5, "claim", "--next", "--holder", "jse/b")
	h.ok(nil, "update", "T-2", "--remove-deps", "T-1")
	var c tk
	h.ok(&c, "claim", "--next", "--holder", "jse/b")
	if c.ID != "T-2" {
		t.Fatalf("claimed %s; want T-2", c.ID)
	}
	tok2 := fmt.Sprint(c.Claim.Token)
	h.ok(nil, "update", "T-3", "--areas", "")
	h.ok(&c, "claim", "--next", "--holder", "jse/c")
	if c.ID != "T-3" {
		t.Fatalf("claimed %s; want T-3 after clearing its areas", c.ID)
	}
	// T-2 -> T-1 again, then T-1 -> T-2 would be a cycle.
	h.ok(nil, "update", "T-2", "--add-deps", "T-1", "--holder", "jse/b", "--token", tok2)
	if code := h.fails(2, "update", "T-1", "--add-deps", "T-2", "--holder", "jse/a", "--token", "1"); code != "invalid" {
		t.Fatalf("cycle: %q", code)
	}
	if code, out, _ := h.run("", "get", "T-2"); code != 0 || !strings.Contains(out, "deps:     T-1") || !strings.Contains(out, "areas:    docs skills") {
		t.Fatalf("human get: %q", out)
	}
}

func leaseExpiry(t *testing.T, h *harness, id string) time.Time {
	t.Helper()
	var got struct {
		Claim *struct {
			ExpiresAt time.Time `json:"expires_at"`
		}
	}
	h.ok(&got, "get", id)
	if got.Claim == nil {
		return time.Time{}
	}
	return got.Claim.ExpiresAt
}

func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	sl := exec.Command("sleep", "60")
	// It stands in for the harness, which runs in its own session.
	detach(sl)
	if err := sl.Start(); err != nil {
		t.Skip("no sleep binary")
	}
	t.Cleanup(func() { sl.Process.Kill(); sl.Wait() })
	return sl
}

// Heartbeats come from a background process watching the session's pid,
// never from LLM turns.
func TestKeepalive(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "long step")
	h.ok(nil, "claim", "T-1", "--holder", "jse/s1", "--ttl", "6s")
	first := leaseExpiry(t, h, "T-1")
	sl := startSleeper(t)

	done := make(chan int, 1)
	go func() {
		code, _, _ := h.run("", "keepalive", "T-1", "--holder", "jse/s1", "--token", "1", "--pid", fmt.Sprint(sl.Process.Pid), "--ttl", "6s", "--interval", "200ms")
		done <- code
	}()
	time.Sleep(1200 * time.Millisecond)
	if got := leaseExpiry(t, h, "T-1"); !got.After(first) {
		t.Fatalf("keepalive didn't extend the lease: %s, first %s", got, first)
	}
	// The session dies: the keepalive stops, and the lease is left to expire.
	sl.Process.Kill()
	sl.Wait()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("keepalive exit %d after session died", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("keepalive outlived its session")
	}

	// A released lease stops the keepalive with lease_lost.
	h.ok(nil, "claim", "T-1", "--holder", "jse/s1", "--ttl", "6s")
	go func() {
		code, _, _ := h.run("", "keepalive", "T-1", "--holder", "jse/s1", "--token", "2", "--pid", fmt.Sprint(os.Getpid()), "--ttl", "6s", "--interval", "200ms")
		done <- code
	}()
	time.Sleep(300 * time.Millisecond)
	h.ok(nil, "release", "T-1", "--holder", "jse/s1", "--token", "2")
	select {
	case code := <-done:
		if code != 4 {
			t.Fatalf("keepalive exit %d after release; want 4", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("keepalive kept running after release")
	}
	h.fails(2, "keepalive", "T-1", "--holder", "jse/s1", "--token", "2")
	h.fails(2, "claim", "T-1", "--holder", "jse/s1", "--keepalive", "999999999")
	// PID 1 never exits, and a process we may not signal isn't the
	// session's: either would keep the lease forever.
	h.fails(2, "claim", "T-1", "--holder", "jse/s1", "--keepalive", "1")
	h.fails(2, "keepalive", "T-1", "--holder", "jse/s1", "--token", "2", "--pid", "1")
}

// A session that ends but is never reaped stays a zombie, which
// kill(pid, 0) still finds. The keepalive must stop on it all the same.
func TestKeepaliveStopsOnZombie(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "long step")
	h.ok(nil, "claim", "T-1", "--holder", "jse/s1", "--ttl", "6s")
	sl := startSleeper(t)
	done := make(chan int, 1)
	go func() {
		code, _, _ := h.run("", "keepalive", "T-1", "--holder", "jse/s1", "--token", "1", "--pid", fmt.Sprint(sl.Process.Pid), "--ttl", "6s", "--interval", "200ms")
		done <- code
	}()
	time.Sleep(300 * time.Millisecond)
	sl.Process.Kill() // no Wait: it stays a zombie until cleanup reaps it
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("keepalive exit %d after session became a zombie", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("keepalive kept a zombie's lease alive")
	}
}

// Claude Code and Codex run each command in a new shell, in a session of
// its own with no terminal, that exits with the command. A keepalive on
// anything in that session ($$, a timeout wrapper) would stop at once and
// let the lease lapse while the session works on, so claim refuses it.
// The harness, the shell's parent ($PPID), is in another session.
func TestClaimKeepaliveRefusesCommandSession(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.ok(nil, "create", "--title", "long step")
	store := h.env["MAESTRO_TICKET_STORE"]
	claim := func(pid string) (int, string) {
		cmd := exec.Command("sh", "-c", `"$0" claim T-1 --holder jse/s1 --ttl 6s --keepalive `+pid+`; exit $?`, exe)
		cmd.Env = append(os.Environ(), childEnv+"=1", "MAESTRO_TICKET_STORE="+store)
		detach(cmd)
		out, _ := cmd.CombinedOutput()
		return cmd.ProcessState.ExitCode(), string(out)
	}
	if code, out := claim("$$"); code != 2 {
		t.Fatalf("claim --keepalive $$: exit %d; want 2\n%s", code, out)
	}
	var got tk
	if h.ok(&got, "get", "T-1"); got.Claim != nil {
		t.Fatalf("refused claim still claimed: %+v", got.Claim)
	}
	if code, out := claim("$PPID"); code != 0 {
		t.Fatalf("claim --keepalive $PPID: exit %d\n%s", code, out)
	}
	// Its keepalive watches this test: release so it stops, and let it
	// close the store before the temp dir goes.
	h.ok(nil, "release", "T-1", "--holder", "jse/s1", "--token", "1")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if out, _ := exec.Command("pgrep", "-f", "keepalive T-1 .*"+regexp.QuoteMeta(store)).Output(); len(out) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("keepalive kept running after release")
		}
	}
}

// claim --keepalive starts the keepalive itself, as a detached process.
func TestClaimStartsKeepalive(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.ok(nil, "create", "--title", "long step")
	sl := startSleeper(t)
	cmd := exec.Command(exe, "claim", "T-1", "--holder", "jse/s1", "--ttl", "6s", "--keepalive", fmt.Sprint(sl.Process.Pid), "--json")
	cmd.Env = append(os.Environ(), childEnv+"=1", "MAESTRO_TICKET_STORE="+h.env["MAESTRO_TICKET_STORE"])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("claim: %v\n%s", err, out)
	}
	first := leaseExpiry(t, h, "T-1")
	deadline := time.Now().Add(10 * time.Second)
	for !leaseExpiry(t, h, "T-1").After(first) {
		if time.Now().After(deadline) {
			t.Fatal("no heartbeat from the keepalive claim started")
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Stop it the normal way: the session ends. It checks every second;
	// give it time to close the store before the temp dir goes.
	sl.Process.Kill()
	sl.Wait()
	time.Sleep(1500 * time.Millisecond)
}

// claim --ttl 0 means the default TTL for the keepalive too. Passing the
// raw 0 on made the detached keepalive exit at once with a usage error
// nobody saw, while the claim reported success.
func TestClaimDefaultTTLKeepaliveRuns(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.ok(nil, "create", "--title", "long step")
	sl := startSleeper(t)
	defer func() {
		sl.Process.Kill()
		sl.Wait()
		time.Sleep(1500 * time.Millisecond) // the keepalive closes the store
	}()
	store := h.env["MAESTRO_TICKET_STORE"]
	cmd := exec.Command(exe, "claim", "T-1", "--holder", "jse/s1", "--ttl", "0", "--keepalive", fmt.Sprint(sl.Process.Pid), "--json")
	cmd.Env = append(os.Environ(), childEnv+"=1", "MAESTRO_TICKET_STORE="+store)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("claim: %v\n%s", err, out)
	}
	time.Sleep(1500 * time.Millisecond)
	out, _ := exec.Command("pgrep", "-f", "keepalive T-1 .*"+regexp.QuoteMeta(store)).Output()
	if len(strings.Fields(string(out))) != 1 {
		t.Fatalf("want one keepalive running for the claim, got pids %q", out)
	}
}

// A store on an unsafe filesystem is refused before it's opened, by
// every command; doctor reports why; the override lets it through.
func TestUnsafeFilesystemRefused(t *testing.T) {
	h := newHarness(t)
	h.checks = func(dir string, allow bool) []ticket.Check {
		return []ticket.Check{{Name: "filesystem", OK: allow, Detail: dir + " is on nfs"}}
	}
	if code := h.fails(2, "create", "--title", "x"); code != "invalid" {
		t.Fatalf("create on nfs: %q", code)
	}
	if _, err := os.Stat(h.env["MAESTRO_TICKET_STORE"]); !os.IsNotExist(err) {
		t.Fatalf("store opened on an unsafe filesystem: %v", err)
	}
	code, out, _ := h.run("", "doctor", "--json")
	if code != 1 || !strings.Contains(out, "is on nfs") {
		t.Fatalf("doctor: %d %s", code, out)
	}
	if _, err := os.Stat(h.env["MAESTRO_TICKET_STORE"]); !os.IsNotExist(err) {
		t.Fatalf("doctor opened a store on an unsafe filesystem: %v", err)
	}
	h.env["MAESTRO_TICKET_ALLOW_UNSAFE_FS"] = "1"
	h.ok(nil, "create", "--title", "x")
}

// list --latest-prefix and --not-latest-prefix match the latest comment, so
// the playbook engine can pick intake without reading every ticket.
func TestListLatestPrefix(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"2", "2", "1", "2"} {
		h.ok(nil, "create", "--title", "t", "--priority", p)
	}
	h.ok(nil, "comment", "T-1", "--body", "Escalation: which API?")
	h.ok(nil, "comment", "T-2", "--body", "Escalation: old")
	h.ok(nil, "comment", "T-2", "--body", "Handoff: answered")
	h.ok(nil, "comment", "T-3", "--body", "Escalation (chief-of-staff): budget")
	ids := func(args ...string) string {
		t.Helper()
		var l struct{ Tickets []tk }
		h.ok(&l, append([]string{"list"}, args...)...)
		var out []string
		for _, x := range l.Tickets {
			out = append(out, x.ID)
		}
		return fmt.Sprint(out)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--latest-prefix", "Escalation:"}, "[T-1]"},             // T-2's latest is the handoff
		{[]string{"--latest-prefix", "Escalation"}, "[T-3 T-1]"},          // priority, then id
		{[]string{"--not-latest-prefix", "Escalation:"}, "[T-3 T-2 T-4]"}, // no comment passes
		{[]string{"--latest-prefix", "Escalation", "--not-latest-prefix", "Escalation (chief-of-staff):"}, "[T-1]"},
		{[]string{"--not-latest-prefix", "Escalation", "--limit", "1"}, "[T-2]"}, // limit after the filter
		{[]string{"--latest-prefix", "escalation:"}, "[]"},                       // case-sensitive
		{[]string{"--latest-prefix", "Escalation:", "--status", "done"}, "[]"},
	}
	for _, c := range cases {
		if got := ids(c.args...); got != c.want {
			t.Errorf("list %v = %s; want %s", c.args, got, c.want)
		}
	}
	if code, out, _ := h.run("", "list", "--latest-prefix", "Handoff:"); code != 0 || !strings.Contains(out, "T-2") || strings.Contains(out, "T-1 ") {
		t.Fatalf("human list: exit %d %q", code, out)
	}
}
