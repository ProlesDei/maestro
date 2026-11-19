//go:build unix

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

func TestSetupEnvReadsNamesOnly(t *testing.T) {
	env := map[string]string{"GITHUB_BLITZLINK_TOKEN": "s3cret", "NODE": "test"}
	setup := "## Run\n\n- GH_TOKEN: from NODE\n\n## Env\n\n<placeholder>\n" +
		"- GH_TOKEN: from GITHUB_BLITZLINK_TOKEN\n" +
		"* `APP_ENV: from NODE`\n" +
		"- GH_TOKEN: from NODE\n" +
		"- MAESTRO_RUN_ID: from NODE\n" +
		"- MISSING: from NOT_SET\n" +
		"- RAW: s3cret\n" +
		"\n## Checks\n\n- X: from NODE\n"
	kv, skipped := setupEnv(setup, func(k string) string { return env[k] })
	if want := []string{"GH_TOKEN=s3cret", "APP_ENV=test"}; !slices.Equal(kv, want) {
		t.Fatalf("kv %q, want %q", kv, want)
	}
	if len(skipped) != 4 {
		t.Fatalf("want 4 skipped (twice, MAESTRO_, unset, a raw value), got %q", skipped)
	}
	// A skip names names, even for a value written into the file.
	if strings.Contains(strings.Join(skipped, "\n"), "s3cret") {
		t.Fatalf("a skip printed a value: %q", skipped)
	}
	if kv, skipped := setupEnv("## Project\n\nx\n", func(string) string { return "v" }); kv != nil || skipped != nil {
		t.Fatalf("no ## Env: %q %q", kv, skipped)
	}
}

// A bullet that isn't NAME: from NAME prints its line number, and its name
// only when that looks like one: never a value written in, with or
// without a colon, nor a token pasted in a name's place.
func TestSetupEnvNeverPrintsAValue(t *testing.T) {
	env := map[string]string{"SRC": "v"}
	for _, l := range []string{
		"GH_TOKEN=secret",
		"GH_TOKEN secret",
		"export GH_TOKEN=\"secret\"",
		"secret",
		"GH_TOKEN: secret",
		"secret: oops",
	} {
		kv, skipped := setupEnv("## Env\n\n- "+l+"\n", func(k string) string { return env[k] })
		if len(kv) != 0 || len(skipped) != 1 {
			t.Fatalf("%q: kv %q skipped %q, want one skip", l, kv, skipped)
		}
		if strings.Contains(skipped[0], "secret") || !strings.HasPrefix(skipped[0], "line 3") {
			t.Fatalf("%q: skip %q", l, skipped[0])
		}
	}
	// A token in a name's place: a valid bullet, but its skip line
	// prints "a name", never the token.
	_, skipped := setupEnv("## Env\n- GH_TOKEN: from ghp_secretsecretsecret\n- ghp_secret: from SRC\n- ghp_secret: from SRC\n", func(k string) string { return env[k] })
	if len(skipped) != 2 || strings.Contains(strings.Join(skipped, "\n"), "secret") {
		t.Fatalf("a token as a name: %q", skipped)
	}
	_, skipped = setupEnv("## Env\n- GH_TOKEN: secret\n", func(string) string { return "" })
	if want := "line 2 (GH_TOKEN): want"; len(skipped) != 1 || !strings.HasPrefix(skipped[0], want) {
		t.Fatalf("a name before a colon: %q, want %q…", skipped, want)
	}
}

// ## Env sets none of the names that change what a session runs, loads or
// reads, nor maestro-ticket's own.
func TestSetupEnvRefusesReservedNames(t *testing.T) {
	setup := "## Env\n"
	for _, n := range []string{"PATH", "HOME", "LD_PRELOAD", "LD_LIBRARY_PATH", "GIT_DIR", "GIT_SSH_COMMAND", "CLAUDE_CONFIG_DIR", "MAESTRO_RUN_ID", "path"} {
		setup += "- " + n + ": from SRC\n"
	}
	setup += "- GH_TOKEN: from SRC\n"
	kv, skipped := setupEnv(setup, func(string) string { return "v" })
	if !slices.Equal(kv, []string{"GH_TOKEN=v"}) || len(skipped) != 9 {
		t.Fatalf("kv %q, skipped %q", kv, skipped)
	}
}

// A job's session gets ## Env's names from the launcher's environment, by
// the supervisor's environment: never in the run's command, its prompt or
// what the job prints.
func TestJobExportsSetupEnv(t *testing.T) {
	h := newRulesHarness(t, "planner")
	if out, err := exec.Command("git", "-C", h.dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	os.MkdirAll(filepath.Join(h.dir, ".maestro", "project"), 0o755)
	setup := "# Setup\n\n## Env\n\n- GH_TOKEN: from GITHUB_BLITZLINK_TOKEN\n- MISSING: from NOT_SET\n"
	if err := os.WriteFile(filepath.Join(h.dir, ".maestro", "project", "project-setup.md"), []byte(setup), 0o644); err != nil {
		t.Fatal(err)
	}
	const secret = "ghp_unit_test_value"
	h.env["GITHUB_BLITZLINK_TOKEN"] = secret

	dir := filepath.Join(h.dir, "runs")
	var supEnv []string
	old := startSupervisor
	t.Cleanup(func() { startSupervisor = old })
	startSupervisor = func(sup *exec.Cmd) error {
		supEnv = sup.Env
		r, err := runs.Load(dir, sup.Args[2])
		if err != nil {
			return err
		}
		r.State = runs.Running
		return runs.Save(dir, r)
	}
	code, out, errs := h.run("goal g: ship it\n", "job", "plan")
	if code != 0 {
		t.Fatalf("job plan: exit %d\n%s%s", code, out, errs)
	}
	if !slices.Contains(supEnv, "GH_TOKEN="+secret) {
		t.Fatalf("the session's environment has no GH_TOKEN from GITHUB_BLITZLINK_TOKEN")
	}
	if slices.ContainsFunc(supEnv, func(kv string) bool { return strings.HasPrefix(kv, "MISSING=") }) {
		t.Fatalf("an unset source gave the session a name")
	}
	if !strings.Contains(errs, "env: skipped: MISSING: NOT_SET is not set") {
		t.Fatalf("no skip line for the unset source: %q", errs)
	}
	if strings.Contains(out+errs, secret) {
		t.Fatalf("job printed the value")
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if strings.Contains(string(b), secret) {
			t.Fatalf("runs/%s holds the value", e.Name())
		}
	}
}
