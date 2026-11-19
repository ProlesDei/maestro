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
