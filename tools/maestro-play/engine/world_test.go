package engine

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// world is one test's fake repo: a real git checkout with an origin, the
// fixture playbooks, a store, the scripted fakes and the engine wired to
// them.
type world struct {
	t      *testing.T
	dir    string // holds bin/, repo/, origin.git, common/ and the fakes' files
	repo   string // the main checkout
	common string
	gitBin string // the real git
	e      *Engine
	now    time.Time
}

// setupText is the fixture's .maestro/project/project-setup.md.
const setupText = "# Project setup\n\n## Project\n\n- Name: tiny-cli\n- Summary: a small CLI.\n\n## Main branch\n\nmain\n\n" +
	"## Checks\n\n- unit: `fake-check unit`\n- lint: `fake-check lint`\n\n## Conventions\n\nNone.\n"

var fakeNames = []string{"maestro-ticket", "claude", "codex", "gh", "git", "fake-check"}

func newWorld(t *testing.T, tickets ...*fakeTicketT) *world {
	t.Helper()
	return newWorldAt(t, "testdata/repo", tickets...)
}

// newWorldAt is newWorld with the playbooks and skills of another fixture.
func newWorldAt(t *testing.T, fixture string, tickets ...*fakeTicketT) *world {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range fakeNames {
		if err := os.Symlink(self, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	gitconfig := filepath.Join(dir, "gitconfig")
	os.WriteFile(gitconfig, []byte("[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644)
	environ := append(os.Environ(), "FAKE_DIR="+dir, "FAKE_COMMON="+filepath.Join(dir, "common"), "FAKE_GIT="+realGit,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL="+gitconfig, "GIT_CONFIG_NOSYSTEM=1",
		// atexit_sleep_ms: a race-built fake would sleep 1s at every exit.
		"GORACE=atexit_sleep_ms=0")
	w := &world{t: t, dir: dir, repo: filepath.Join(dir, "repo"), common: filepath.Join(dir, "common"), gitBin: realGit,
		now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	os.MkdirAll(w.common, 0o755)
	os.MkdirAll(filepath.Join(w.repo, ".maestro", "project"), 0o755)
	os.WriteFile(filepath.Join(w.repo, ".maestro", "project", "project-setup.md"), []byte(setupText), 0o644)
	os.WriteFile(filepath.Join(w.repo, "README.md"), []byte("tiny-cli\n"), 0o644)
	origin := filepath.Join(dir, "origin.git")
	for _, args := range [][]string{
		{"init", "-q", "--bare", origin},
		{"-C", w.repo, "init", "-q"},
		{"-C", w.repo, "add", "README.md"},
		{"-C", w.repo, "commit", "-q", "-m", "first"},
		{"-C", w.repo, "remote", "add", "origin", origin},
		{"-C", w.repo, "push", "-q", "origin", "main"},
	} {
		cmd := exec.Command(realGit, args...)
		cmd.Env = environ
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	store := fakeStore{Tickets: map[string]*fakeTicketT{}, Keys: map[string]string{}, Results: map[string]string{}}
	for _, tk := range tickets {
		store.Tickets[tk.ID] = tk
	}
	w.saveFake("tickets.json", &store)
	repo, _ := filepath.Abs(fixture)
	w.e, err = New(Config{
		Playbooks: filepath.Join(repo, "playbooks"), Skills: filepath.Join(repo, "skills"),
		Common: w.common, Dir: w.repo,
		TicketBin: filepath.Join(bin, "maestro-ticket"), GitBin: filepath.Join(bin, "git"), GhBin: filepath.Join(bin, "gh"),
		ClaudeBin: filepath.Join(bin, "claude"), CodexBin: filepath.Join(bin, "codex"),
		Limits:  map[string]any{"step_visits": 3, "retries": 0},
		Environ: environ,
		Now:     func() time.Time { return w.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) saveFake(name string, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(filepath.Join(w.dir, name), b, 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) script(cfg fakeConfig) { w.saveFake("fake.json", &cfg) }

func (w *world) fake() fakeConfig {
	var cfg fakeConfig
	b, _ := os.ReadFile(filepath.Join(w.dir, "fake.json"))
	json.Unmarshal(b, &cfg)
	return cfg
}

func (w *world) store() fakeStore {
	var s fakeStore
	b, _ := os.ReadFile(filepath.Join(w.dir, "tickets.json"))
	json.Unmarshal(b, &s)
	return s
}

func (w *world) ticket(id string) *fakeTicketT { return w.store().Tickets[id] }

// git runs the real git in dir and returns its trimmed output.
func (w *world) git(dir string, args ...string) string {
	w.t.Helper()
	cmd := exec.Command(w.gitBin, args...)
	cmd.Dir, cmd.Env = dir, w.e.cfg.Environ
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type fakeCall struct {
	Bin    string   `json:"bin"`
	Args   []string `json:"args"`
	Prompt string   `json:"prompt"`
	Cwd    string   `json:"cwd"`
	RunEnv string   `json:"run_env"`
}

func (w *world) calls() []fakeCall {
	f, err := os.Open(filepath.Join(w.dir, "calls.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []fakeCall
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 4<<20)
	for sc.Scan() {
		var c fakeCall
		json.Unmarshal(sc.Bytes(), &c)
		out = append(out, c)
	}
	return out
}

// find returns the calls whose args start with prefix.
func (w *world) find(bin string, prefix ...string) []fakeCall {
	var out []fakeCall
	for _, c := range w.calls() {
		if c.Bin != bin || len(c.Args) < len(prefix) {
			continue
		}
		ok := true
		for i, p := range prefix {
			if c.Args[i] != p {
				ok = false
			}
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// sessions are the harness calls, claude or codex, in order.
func (w *world) sessions() []fakeCall {
	var out []fakeCall
	for _, c := range w.calls() {
		if c.Bin == "claude" || c.Bin == "codex" {
			out = append(out, c)
		}
	}
	return out
}

// resumed reports whether a harness call resumed its session.
func resumed(c fakeCall) bool {
	if c.Bin == "codex" {
		return len(c.Args) > 1 && c.Args[1] == "resume"
	}
	return hasFlag(c.Args, "--resume")
}

func (w *world) run(o RunOpts) Result {
	w.t.Helper()
	if o.Bot == "" {
		o.Bot = "eng-1"
	}
	r, err := w.e.Run(o)
	if err != nil {
		w.t.Fatalf("run: %v", err)
	}
	return r
}

// reply is a scripted harness reply.
func reply(status string, fields map[string]any) map[string]any {
	r := map[string]any{"status": status, "summary": status + " step", "evidence": []any{"go test ./... -> exit 0"}, "retro": "nothing"}
	for k, v := range fields {
		r[k] = v
	}
	return map[string]any{"reply": r}
}

// waitFor is find, for a call a background process makes: it waits up to
// 10s for one.
func (w *world) waitFor(bin string, prefix ...string) []fakeCall {
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if c := w.find(bin, prefix...); len(c) > 0 || time.Now().After(deadline) {
			return c
		}
	}
}
