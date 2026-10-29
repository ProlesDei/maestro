package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type harness struct {
	t   *testing.T
	dir string
	env map[string]string
	now time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	return &harness{t: t, dir: dir, now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC),
		env: map[string]string{"MAESTRO_USAGE_DB": filepath.Join(dir, "usage.db")}}
}

func (h *harness) run(stdin string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(context.Background(), args, Env{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string { return h.env[k] }, Dir: h.dir,
		Now: func() time.Time { return h.now },
	})
	return code, out.String(), errb.String()
}

// want runs a command, checks its exit code and decodes JSON output into v.
func (h *harness) want(code int, v any, stdin string, args ...string) string {
	h.t.Helper()
	got, out, errs := h.run(stdin, args...)
	if got != code {
		h.t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, got, code, out, errs)
	}
	if v != nil {
		if err := json.Unmarshal([]byte(out), v); err != nil {
			h.t.Fatalf("%v: bad json %v: %s", args, err, out)
		}
	}
	return out
}

func (h *harness) budgets(js string) {
	h.t.Helper()
	p := filepath.Join(h.dir, "budgets.json")
	if err := os.WriteFile(p, []byte(js), 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.env["MAESTRO_BUDGETS"] = p
}

type reportRow struct {
	Group    string  `json:"harness"`
	Sessions int     `json:"sessions"`
	USD      float64 `json:"usd"`
	Input    int     `json:"input"`
	Cached   int     `json:"cached"`
	Output   int     `json:"output"`
	Failures int     `json:"failures"`
	Unpriced int     `json:"unpriced"`
}

func (h *harness) report() []reportRow {
	h.t.Helper()
	var rows []reportRow
	h.want(0, &rows, "", "report", "--by", "harness", "--json")
	return rows
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "usage", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// fake is a harness command that prints a fixture and exits with code.
func fake(t *testing.T, name, code string) []string {
	return []string{"--", "sh", "-c", `cat "$0"; exit ` + code, fixture(t, name)}
}

func TestRunPassesThroughAndRecordsEachInvocation(t *testing.T) {
	h := newHarness(t)
	want, _ := os.ReadFile(fixture(t, "claude-json.json"))
	args := append([]string{"run", "--ticket", "T-12", "--role", "junior-engineer"}, fake(t, "claude-json.json", "0")...)
	for range 2 {
		if out := h.want(0, nil, "", args...); out != string(want) {
			t.Fatalf("output changed:\n%q\nwant\n%q", out, want)
		}
	}
	// Same output twice, still two rows: a resumed session repeats its ID.
	rows := h.report()
	if len(rows) != 1 || rows[0].Group != "claude" || rows[0].Sessions != 2 || rows[0].Input != 2*4112 || rows[0].USD != 2*0.1834 {
		t.Fatalf("report = %+v", rows)
	}
}

func TestRunExitCodes(t *testing.T) {
	h := newHarness(t)
	// A successful result with a failing exit code is a failure.
	h.want(3, nil, "", append([]string{"run", "--role", "r"}, fake(t, "claude-json.json", "3")...)...)
	h.want(127, nil, "", "run", "--role", "r", "--", "no-such-harness-binary")
	h.want(64, nil, "", "run", "--role", "r")
	h.want(64, nil, "", append([]string{"run", "--harness", "gemini"}, fake(t, "codex.jsonl", "0")...)...)
	var br struct {
		Failures       int `json:"failures"`
		RecentSessions int `json:"recent_sessions"`
	}
	h.want(0, &br, "", "breaker", "--role", "r")
	if br.Failures != 2 || br.RecentSessions != 2 {
		t.Fatalf("breaker = %+v, want 2 failed runs recorded", br)
	}
}

type closedPipe struct{}

func (closedPipe) Write([]byte) (int, error) { return 0, os.ErrClosed }

// A caller that stops reading (run ... | head) must not lose the row.
func TestRunRecordsWhenStdoutCloses(t *testing.T) {
	h := newHarness(t)
	code := Run(context.Background(), append([]string{"run", "--role", "r"}, fake(t, "claude-json.json", "0")...), Env{
		Stdin: strings.NewReader(""), Stdout: closedPipe{}, Stderr: &bytes.Buffer{},
		Getenv: func(k string) string { return h.env[k] }, Dir: h.dir, Now: func() time.Time { return h.now },
	})
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if rows := h.report(); len(rows) != 1 || rows[0].Group != "claude" || rows[0].Failures != 0 || rows[0].USD != 0.1834 {
		t.Fatalf("report = %+v", rows)
	}
}

func TestRunRecordKeyIsIdempotent(t *testing.T) {
	h := newHarness(t)
	for range 3 {
		h.want(0, nil, "", append([]string{"run", "--record-key", "T-1:step-2"}, fake(t, "codex.jsonl", "0")...)...)
	}
	if rows := h.report(); len(rows) != 1 || rows[0].Sessions != 1 {
		t.Fatalf("report = %+v", rows)
	}
}

func TestRunPricesCodexFromModel(t *testing.T) {
	h := newHarness(t)
	h.budgets(`{"prices_per_million_tokens": {"gpt-5.5": {"input": 2, "cached_input": 0.5, "output": 10}}}`)
	h.want(0, nil, "", append([]string{"run", "--model", "gpt-5.5"}, fake(t, "codex.jsonl", "0")...)...)
	h.want(0, nil, "", append([]string{"run"}, fake(t, "codex.jsonl", "0")...)...)
	rows := h.report()
	// 1315*2 + 24448*0.5 + 152*10 per million.
	want := (1315*2 + 24448*0.5 + 152*10) / 1e6
	if len(rows) != 1 || rows[0].Sessions != 2 || rows[0].Unpriced != 1 || abs(rows[0].USD-want) > 1e-12 {
		t.Fatalf("report = %+v, want usd %v with one unpriced", rows, want)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func TestRecordFromStdin(t *testing.T) {
	h := newHarness(t)
	data, _ := os.ReadFile(fixture(t, "grok-json.json"))
	var r struct {
		Recorded string   `json:"recorded"`
		Harness  string   `json:"harness"`
		Status   string   `json:"status"`
		CostUSD  *float64 `json:"cost_usd"`
	}
	h.want(0, &r, string(data), "record", "--ticket", "T-3")
	if r.Harness != "grok" || r.Status != "success" || r.CostUSD == nil || *r.CostUSD != 0.01268905 ||
		!strings.HasPrefix(r.Recorded, "grok:abc123:") {
		t.Fatalf("record = %+v", r)
	}
	// A retried pipe of the same output replaces its row.
	h.want(0, nil, string(data), "record", "--ticket", "T-3")
	h.want(0, &r, "garbage\n", "record", "--ticket", "T-3")
	if r.Harness != "unknown" || r.Status != "unparsed" || r.CostUSD != nil {
		t.Fatalf("garbage record = %+v", r)
	}
	if rows := h.report(); len(rows) != 2 || rows[0].Group != "grok" || rows[0].Sessions != 1 {
		t.Fatalf("report = %+v", rows)
	}
}

func TestRecordManual(t *testing.T) {
	h := newHarness(t)
	var r struct {
		Harness string   `json:"harness"`
		CostUSD *float64 `json:"cost_usd"`
	}
	h.want(0, &r, "", "record", "--manual", "--role", "chief-of-staff", "--input-tokens", "1000",
		"--output-tokens", "200", "--cost-usd", "0.05")
	if r.Harness != "manual" || r.CostUSD == nil || *r.CostUSD != 0.05 {
		t.Fatalf("record = %+v", r)
	}
	h.want(0, nil, "", "record", "--manual", "--harness", "grok-bot", "--status", "error")
	h.want(64, nil, "", "record", "--input-tokens", "5")
	h.want(64, nil, "", "record", "--manual", "--status", "maybe")
	h.want(64, nil, "", "record", "--manual", "extra")
	for _, bad := range [][]string{
		{"--cost-usd", "-1"}, {"--cost-usd", "NaN"}, {"--cost-usd", "1.5abc"}, {"--input-tokens", "-5"},
	} {
		h.want(64, nil, "", append([]string{"record", "--manual"}, bad...)...)
	}
	if rows := h.report(); len(rows) != 2 {
		t.Fatalf("report = %+v", rows)
	}
}

func TestCheckExitCodes(t *testing.T) {
	h := newHarness(t)
	h.budgets(`{"tickets_default": {"usd_total": 1}, "roles": {"qa": {"tokens_24h": 100}}}`)
	type result struct {
		Status       string            `json:"status"`
		USDRemaining *float64          `json:"usd_remaining"`
		Findings     []json.RawMessage `json:"findings"`
	}
	var res result
	h.want(0, &res, "", "check", "--ticket", "T-1")
	if res.Status != "ok" || len(res.Findings) != 0 || *res.USDRemaining != 1 {
		t.Fatalf("check = %+v", res)
	}
	h.want(0, &res, "", "check", "--ticket", "T-1", "--verbose")
	if len(res.Findings) != 1 {
		t.Fatalf("verbose check = %+v", res)
	}
	h.want(0, nil, "", "record", "--manual", "--ticket", "T-1", "--cost-usd", "0.85")
	h.want(1, &res, "", "check", "--ticket", "T-1")
	h.want(0, nil, "", "record", "--manual", "--ticket", "T-1", "--cost-usd", "0.15")
	h.want(2, &res, "", "check", "--ticket", "T-1")
	if res.Status != "over" || *res.USDRemaining != 0 || len(res.Findings) != 1 {
		t.Fatalf("check = %+v", res)
	}
	// Tokens are checked too, for subscription plans with no dollar bill.
	h.want(0, nil, "", "record", "--manual", "--role", "qa", "--input-tokens", "150")
	h.want(2, nil, "", "check", "--role", "qa")
	// The window slides.
	h.now = h.now.Add(25 * time.Hour)
	h.want(0, nil, "", "check", "--role", "qa")

	// Config mistakes are usage errors, never "over budget".
	h.budgets(`{"tickets_default": {"usd_totl": 1}}`)
	h.want(64, nil, "", "check", "--ticket", "T-1")
	h.env["MAESTRO_BUDGETS"] = filepath.Join(h.dir, "missing.json")
	h.want(64, nil, "", "check")
	h.want(64, nil, "", "check", "--bogus")
}

// A bot's gate: check before each session, breaker after each failure. The
// loop must stop once the budget runs out, and the pool must pause once
// failures spike.
func TestCheckAndBreakerGateSessions(t *testing.T) {
	h := newHarness(t)
	h.budgets(`{"tickets_default": {"usd_total": 0.5}, "breaker": {"window": 4, "min_samples": 3, "max_failure_rate": 0.5}}`)
	sessions := 0
	for range 10 {
		if code, out, _ := h.run("", "check", "--ticket", "T-9"); code != 0 {
			if code != 2 || !strings.Contains(out, `"over"`) {
				t.Fatalf("check exit %d: %s", code, out)
			}
			break
		}
		h.want(0, nil, "", append([]string{"run", "--ticket", "T-9", "--role", "junior-engineer"}, fake(t, "claude-json.json", "0")...)...)
		sessions++
	}
	// 0.1834 a session: the third pushes spend to 0.55, so the fourth is refused.
	if sessions != 3 {
		t.Fatalf("ran %d sessions, want 3", sessions)
	}

	paused := false
	for range 10 {
		if code, _, _ := h.run("", "breaker", "--role", "senior-engineer"); code == 2 {
			paused = true
			break
		}
		h.run("", append([]string{"run", "--role", "senior-engineer"}, fake(t, "claude-error.json", "1")...)...)
	}
	if !paused {
		t.Fatal("breaker never tripped")
	}
	h.want(0, nil, "", "breaker", "--role", "junior-engineer")
	h.want(64, nil, "", "breaker")
}

func TestReportTable(t *testing.T) {
	h := newHarness(t)
	h.want(0, nil, "", append([]string{"run", "--workstream", "auth"}, fake(t, "codex.jsonl", "0")...)...)
	h.want(0, nil, "", append([]string{"run", "--workstream", "auth"}, fake(t, "claude-json.json", "0")...)...)
	out := h.want(0, nil, "", "report", "--since", "1h")
	if !strings.Contains(out, "auth") || !strings.Contains(out, "* some sessions have no cost") {
		t.Fatalf("report:\n%s", out)
	}
	h.want(64, nil, "", "report", "--by", "bot")
	h.want(64, nil, "", "report", "--since", "1w")
}

func TestHelpAndUnknown(t *testing.T) {
	h := newHarness(t)
	h.want(0, nil, "", "help")
	h.want(64, nil, "")
	h.want(64, nil, "", "spend")
}

// Without MAESTRO_USAGE_DB the db lives in the git common dir, so every
// worktree of a repo shares it.
func TestDefaultDBIsSharedByWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	wt := filepath.Join(t.TempDir(), "wt")
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", repo, "worktree", "add", "-q", wt},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, dir := range []string{repo, wt} {
		h := newHarness(t)
		delete(h.env, "MAESTRO_USAGE_DB")
		h.dir = dir
		h.want(0, nil, "", "record", "--manual", "--cost-usd", "1")
	}
	h := newHarness(t)
	delete(h.env, "MAESTRO_USAGE_DB")
	h.dir = wt
	if rows := h.report(); len(rows) != 1 || rows[0].Sessions != 2 {
		t.Fatalf("report = %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "maestro", "usage.db")); err != nil {
		t.Fatal(err)
	}
	// Budgets come from the worktree's .maestro/project/budgets.json.
	if err := os.MkdirAll(filepath.Join(wt, ".maestro", "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt, ".maestro", "project", "budgets.json"), []byte(`{"global": {"usd_total": 2}}`), 0o644)
	h.want(2, nil, "", "check")
}
