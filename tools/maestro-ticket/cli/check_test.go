//go:build unix

package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// checkRepo makes a git repo with good/ok holding 0 and bad/ok holding 1,
// and the environment of a review run on its store.
func checkRepo(t *testing.T) (root string, env map[string]string) {
	t.Helper()
	root, sdir := t.TempDir(), t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "a@b")
	git("config", "user.name", "a")
	for d, v := range map[string]string{"good": "0", "bad": "1"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
		os.WriteFile(filepath.Join(root, d, "ok"), []byte(v+"\n"), 0o644)
	}
	git("add", ".")
	git("commit", "-qm", "x")
	r, err := runs.Create(filepath.Join(sdir, "runs"), runs.Record{Launcher: "reviewer", Ticket: "T-1", Job: "review"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return root, map[string]string{"MAESTRO_TICKET_STORE": filepath.Join(sdir, "tickets.db"), runs.RunIDEnv: r.ID}
}

func runCheckIn(env map[string]string, dir string, args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := Run(context.Background(), append([]string{"check"}, args...), Env{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string { return env[k] }, Dir: dir, DiskFree: halfFree,
	})
	return code, out.String() + errb.String()
}

// Exits with what ./ok holds.
var okCmd = []string{"--", "sh", "-c", "exit $(cat ok)"}

// A review's pass carries in the same folder, and only there: the same
// command in another folder of the repo runs.
func TestCheckCarriesOnlyInItsFolder(t *testing.T) {
	root, env := checkRepo(t)
	good, bad := filepath.Join(root, "good"), filepath.Join(root, "bad")
	if code, out := runCheckIn(env, good, append([]string{"test"}, okCmd...)...); code != 0 {
		t.Fatalf("good: %d %s", code, out)
	}
	if code, out := runCheckIn(env, good, append([]string{"test"}, okCmd...)...); code != 0 || !strings.Contains(out, "carried") {
		t.Fatalf("good again didn't carry: %d %s", code, out)
	}
	if code, out := runCheckIn(env, bad, append([]string{"test"}, okCmd...)...); code != 1 {
		t.Fatalf("bad/ carried good/'s pass: %d %s", code, out)
	}
}

// A --fresh run that fails drops the pass it passed over, so the next
// check runs again.
func TestCheckFreshFailDropsPass(t *testing.T) {
	root, env := checkRepo(t)
	flag := filepath.Join(t.TempDir(), "fail")
	cmd := []string{"test", "--", "sh", "-c", "test ! -e " + flag}
	if code, out := runCheckIn(env, root, cmd...); code != 0 {
		t.Fatalf("first: %d %s", code, out)
	}
	os.WriteFile(flag, nil, 0o644)
	if code, out := runCheckIn(env, root, append([]string{"--fresh"}, cmd...)...); code == 0 {
		t.Fatalf("--fresh passed: %s", out)
	}
	if code, out := runCheckIn(env, root, cmd...); code == 0 {
		t.Fatalf("a pass carried after a failing --fresh run: %s", out)
	}
}
