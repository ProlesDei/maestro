package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-play/engine"
)

const fixture = "../engine/testdata/repo"

func run(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(Env{
		Args: args, Stdout: &out, Stderr: &errb, Dir: t.TempDir(),
		Getenv: func(k string) string { return env[k] },
	})
	return code, out.String(), errb.String()
}

func TestLint(t *testing.T) {
	code, out, _ := run(t, nil, "lint", fixture+"/playbooks", "--skills", fixture+"/skills")
	if code != 0 || out != "7 playbooks, 0 problems\n" {
		t.Fatalf("exit %d: %s", code, out)
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "playbooks", "engineer"), 0o755)
	os.WriteFile(filepath.Join(dir, "playbooks", "engineer", "x.yaml"), []byte("name: x\nrole: engineer\ndescription: d\nsteps: []\n"), 0o644)
	code, out, _ = run(t, nil, "lint", "--playbooks", filepath.Join(dir, "playbooks"))
	if code != 1 || !strings.Contains(out, "no steps") || !strings.Contains(out, "missing _contract.md") {
		t.Fatalf("exit %d: %s", code, out)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"frob"},
		{"run", "engineer/build-ticket"},
		{"run", "--bot", "b", "--next", "engineer"}, // --next is gone
		{"resume"},
		{"resume", "r1", "--choice", "x"}, // so is --choice
		{"render", "engineer/build-ticket"},
		{"lint", "a", "b"},
		{"run", "--nope"},
	} {
		if code, _, _ := run(t, nil, args...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
	if code, out, _ := run(t, nil); code != 2 || !strings.Contains(out, "Usage:") {
		t.Errorf("no args: %d", code)
	}
	if code, _, _ := run(t, nil, "--help"); code != 0 {
		t.Errorf("--help: %d", code)
	}
	for _, v := range []string{"soon", "-5m", "30"} {
		env := map[string]string{"MAESTRO_COMMON": t.TempDir(), "MAESTRO_CHECK_TIMEOUT": v}
		if code, _, errs := run(t, env, "run", "engineer/build-ticket", "--ticket", "T-1", "--bot", "b"); code != 2 || !strings.Contains(errs, "MAESTRO_CHECK_TIMEOUT") {
			t.Errorf("MAESTRO_CHECK_TIMEOUT=%s: exit %d: %s", v, code, errs)
		}
	}
}

// The timeout variables reach the engine; 0 is no limit, unset the default.
func TestTimeoutEnv(t *testing.T) {
	var got engine.Config
	var out, errb bytes.Buffer
	env := map[string]string{"MAESTRO_COMMON": t.TempDir(), "MAESTRO_SESSION_TIMEOUT": "90s", "MAESTRO_GIT_TIMEOUT": "0",
		"MAESTRO_CLAUDE_BIN": "no-such-claude"}
	Main(Env{Args: []string{"run", "engineer/build-ticket", "--ticket", "T-1", "--bot", "b"}, Stdout: &out, Stderr: &errb,
		Dir: t.TempDir(), Getenv: func(k string) string { return env[k] }, Engine: func(c *engine.Config) { got = *c }})
	if got.SessionTimeout != 90*time.Second || got.GitTimeout != -1 || got.CheckTimeout != 0 {
		t.Errorf("timeouts %v %v %v", got.SessionTimeout, got.GitTimeout, got.CheckTimeout)
	}
}

func TestRenderFromFiles(t *testing.T) {
	dir := t.TempDir()
	tf := filepath.Join(dir, "t.json")
	os.WriteFile(tf, []byte(`{"id":"T-3","status":"todo","body":"Type: chore"}`), 0o644)
	env := map[string]string{"MAESTRO_PLAYBOOKS": fixture + "/playbooks", "MAESTRO_COMMON": dir}
	code, out, errs := run(t, env, "render", "engineer/build-ticket", "--step", "verify", "--ticket-file", tf)
	if code != 0 || !strings.Contains(out, "Ticket T-3 (chore)") {
		t.Fatalf("exit %d: %s %s", code, out, errs)
	}
}

func TestRunReportsJSON(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{
		"MAESTRO_PLAYBOOKS": fixture + "/playbooks", "MAESTRO_COMMON": dir,
		"MAESTRO_CLAUDE_BIN": filepath.Join(dir, "no-such-claude"),
	}
	code, out, _ := run(t, env, "run", "engineer/build-ticket", "--ticket", "T-1", "--bot", "eng-1")
	var r map[string]any
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 1 || r["state"] != "failed" || !strings.Contains(out, "no-such-claude not found") {
		t.Fatalf("exit %d: %s", code, out)
	}
	env["MAESTRO_HARNESS"] = "codex"
	env["MAESTRO_CODEX_BIN"] = filepath.Join(dir, "no-such-codex")
	if code, out, _ = run(t, env, "run", "engineer/build-ticket", "--ticket", "T-1", "--bot", "eng-1"); code != 1 || !strings.Contains(out, "no-such-codex not found") {
		t.Fatalf("codex: exit %d: %s", code, out)
	}
	os.WriteFile(filepath.Join(dir, "STOP"), nil, 0o644)
	code, out, _ = run(t, env, "run", "engineer/build-ticket", "--ticket", "T-1", "--bot", "eng-1")
	if code != 7 || !strings.Contains(out, `"state":"stopped"`) {
		t.Fatalf("with the stop file: exit %d: %s", code, out)
	}
}
