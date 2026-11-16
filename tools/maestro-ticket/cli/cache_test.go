//go:build unix

package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// cacheRepo makes a main checkout whose setup lists setupCache under
// ## Cache, with a lockfile and node_modules gitignored, and returns it,
// a git helper and the environment of a review run on its store.
func cacheRepo(t *testing.T, setupCache string) (root string, git func(dir string, args ...string), env map[string]string) {
	t.Helper()
	root, sdir := t.TempDir(), t.TempDir()
	git = func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git(root, "init", "-q", "-b", "main")
	git(root, "config", "user.email", "a@b")
	git(root, "config", "user.name", "a")
	os.MkdirAll(filepath.Join(root, ".maestro", "project"), 0o755)
	os.WriteFile(filepath.Join(root, ".maestro", "project", "project-setup.md"), []byte("# Setup\n\n## Main branch\n\n- `main`\n\n## Cache\n\n"+setupCache+"\n\n## QA\n\n- not: a -> cache line\n"), 0o644)
	os.WriteFile(filepath.Join(root, "package-lock.json"), []byte("lock 1\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n"), 0o644)
	git(root, "add", ".")
	git(root, "commit", "-qm", "x")
	// origin, with main as fetched: a save wants a tree main holds.
	origin := filepath.Join(t.TempDir(), "origin.git")
	git(root, "init", "-q", "--bare", "-b", "main", origin)
	git(root, "remote", "add", "origin", origin)
	git(root, "push", "-q", "origin", "main")
	git(root, "fetch", "-q", "origin")
	r, err := runs.Create(filepath.Join(sdir, "runs"), runs.Record{Launcher: "qa-lead", Ticket: "T-1", Job: "qa"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return root, git, map[string]string{"MAESTRO_TICKET_STORE": filepath.Join(sdir, "tickets.db"), runs.RunIDEnv: r.ID}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func runCacheIn(env map[string]string, dir string, verb ...string) (int, string) {
	var out, errb bytes.Buffer
	code := Run(context.Background(), append([]string{"cache"}, verb...), Env{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string { return env[k] }, Dir: dir, DiskFree: halfFree,
	})
	return code, out.String() + errb.String()
}

// fill makes dir/node_modules as an install would.
func fill(t *testing.T, dir, content string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg", "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte(content), 0o644)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "bin", "run"), []byte("#!/bin/sh\n"), 0o755)
	os.Symlink("pkg/bin/run", filepath.Join(dir, "node_modules", "run"))
}

const nodeLine = "- node: `package-lock.json` -> `node_modules`"

// A review's save keeps the folder; a fresh worktree with the same
// lockfile gets it back whole, one with another lockfile doesn't, and a
// folder that is there is left alone.
func TestCacheSaveAndRestore(t *testing.T) {
	root, git, env := cacheRepo(t, nodeLine)
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	git(root, "worktree", "add", "-q", "--detach", a)
	git(root, "worktree", "add", "-q", "--detach", b)

	if code, out := runCacheIn(env, a, "restore"); code != 0 || !strings.Contains(out, "cache node: miss") {
		t.Fatalf("first restore: %d %s", code, out)
	}
	fill(t, a, "v1")
	// A build, or a session outside launch, never saves.
	build := map[string]string{"MAESTRO_TICKET_STORE": env["MAESTRO_TICKET_STORE"]}
	if code, out := runCacheIn(build, a, "save"); code != 0 || !strings.Contains(out, "not saved: only a review or QA run saves") {
		t.Fatalf("a build's save: %d %s", code, out)
	}
	if code, out := runCacheIn(env, a, "save"); code != 0 || !strings.Contains(out, "cache node: saved (node_modules, key ") {
		t.Fatalf("save: %d %s", code, out)
	}
	if code, out := runCacheIn(env, a, "save"); code != 0 || !strings.Contains(out, "cache node: already saved") {
		t.Fatalf("save again: %d %s", code, out)
	}
	if code, out := runCacheIn(build, b, "restore"); code != 0 || !strings.Contains(out, "cache node: restored, last used ") {
		t.Fatalf("restore: %d %s", code, out)
	}
	got, _ := os.ReadFile(filepath.Join(b, "node_modules", "pkg", "index.js"))
	st, err := os.Stat(filepath.Join(b, "node_modules", "pkg", "bin", "run"))
	link, _ := os.Readlink(filepath.Join(b, "node_modules", "run"))
	if string(got) != "v1" || err != nil || st.Mode().Perm() != 0o755 || link != "pkg/bin/run" {
		t.Fatalf("restored folder: %q %v %v %q", got, st, err, link)
	}
	// What the restoring session changes never reaches the kept copy.
	os.WriteFile(filepath.Join(b, "node_modules", "pkg", "index.js"), []byte("hacked"), 0o644)
	if code, out := runCacheIn(build, b, "restore"); code != 0 || !strings.Contains(out, "kept: the folder is there") {
		t.Fatalf("restore over a folder: %d %s", code, out)
	}
	c := filepath.Join(t.TempDir(), "c")
	git(root, "worktree", "add", "-q", "--detach", c)
	runCacheIn(build, c, "restore")
	if got, _ := os.ReadFile(filepath.Join(c, "node_modules", "pkg", "index.js")); string(got) != "v1" {
		t.Fatalf("a restore's change reached the kept copy: %q", got)
	}
	// Another lockfile is another key.
	os.RemoveAll(filepath.Join(c, "node_modules"))
	os.WriteFile(filepath.Join(c, "package-lock.json"), []byte("lock 2\n"), 0o644)
	if code, out := runCacheIn(build, c, "restore"); code != 0 || !strings.Contains(out, "cache node: miss") {
		t.Fatalf("another lockfile: %d %s", code, out)
	}
	// A checkout with changes saves nothing: what it holds isn't the tree.
	fill(t, c, "v2")
	if code, out := runCacheIn(env, c, "save"); code != 0 || !strings.Contains(out, "not saved: the checkout has changes") {
		t.Fatalf("a dirty save: %d %s", code, out)
	}
	// Nor does a tree main doesn't hold: a PR's code may have written to
	// the folder, and the key is only the lockfile.
	git(c, "commit", "-qam", "a PR bumps the lock")
	if code, out := runCacheIn(env, c, "save"); code != 0 || !strings.Contains(out, "not saved: this checkout's tree is not main's") {
		t.Fatalf("a PR's save: %d %s", code, out)
	}
	// Once that commit is on main, as after QA's merge, the same checkout
	// saves, by the merge commit's sha too.
	head := strings.TrimSpace(gitOut(t, c, "rev-parse", "HEAD"))
	if code, out := runCacheIn(env, c, "save", "--at", head); code != 0 || !strings.Contains(out, "is not on main as last fetched") {
		t.Fatalf("a sha not on main: %d %s", code, out)
	}
	git(c, "push", "-q", "origin", "HEAD:main")
	git(c, "fetch", "-q", "origin")
	if code, out := runCacheIn(env, c, "save", "--at", head); code != 0 || !strings.Contains(out, "cache node: saved (") {
		t.Fatalf("a save at the merge: %d %s", code, out)
	}
	if code, out := runCacheIn(env, c, "save", "--at", "HEAD~1"); code != 2 {
		t.Fatalf("--at takes a sha only: %d %s", code, out)
	}
	// A folder git tracks files in, one with a link to an absolute path,
	// and a key file that is no plain file all save nothing.
	d := filepath.Join(t.TempDir(), "d")
	git(root, "worktree", "add", "-q", "--detach", d, head)
	os.WriteFile(filepath.Join(d, "package-lock.json"), []byte("lock 3\n"), 0o644)
	fill(t, d, "v3")
	os.Symlink(filepath.Join(d, "package-lock.json"), filepath.Join(d, "node_modules", "abs"))
	git(d, "commit", "-qam", "lock 3")
	git(d, "push", "-q", "origin", "HEAD:main")
	git(d, "fetch", "-q", "origin")
	if code, out := runCacheIn(env, d, "save"); code != 0 || !strings.Contains(out, "not saved: abs links to an absolute path") {
		t.Fatalf("an absolute link: %d %s", code, out)
	}
	os.Remove(filepath.Join(d, "node_modules", "abs"))
	git(d, "add", "-f", "node_modules/pkg/index.js")
	git(d, "commit", "-qm", "planted")
	git(d, "push", "-q", "origin", "HEAD:main")
	git(d, "fetch", "-q", "origin")
	if code, out := runCacheIn(env, d, "save"); code != 0 || !strings.Contains(out, "not saved: git tracks files in the folder") {
		t.Fatalf("a tracked folder: %d %s", code, out)
	}
	os.Remove(filepath.Join(d, "package-lock.json"))
	os.Symlink("/dev/zero", filepath.Join(d, "package-lock.json"))
	if code, out := runCacheIn(build, d, "restore"); code != 0 || !strings.Contains(out, "skipped: package-lock.json is no plain file") {
		t.Fatalf("a key file that is a link: %d %s", code, out)
	}
	// drop removes what is kept, so a bad copy can go.
	if code, out := runCacheIn(build, d, "drop", "node"); code != 0 || !strings.Contains(out, "cache: dropped 2 kept folders") {
		t.Fatalf("drop: %d %s", code, out)
	}
	if code, out := runCacheIn(build, b, "restore"); code != 0 || !strings.Contains(out, "kept: the folder is there") {
		t.Fatalf("after drop: %d %s", code, out)
	}
}

// A folder whose path runs through a link is skipped: a PR's tree can
// point one anywhere, to read on save or to write on restore.
func TestCacheSkipsALinkedParent(t *testing.T) {
	root, git, env := cacheRepo(t, "- web: `package-lock.json` -> `web/node_modules`")
	wt, outside := filepath.Join(t.TempDir(), "wt"), t.TempDir()
	git(root, "worktree", "add", "-q", "--detach", wt)
	os.Symlink(outside, filepath.Join(wt, "web"))
	for _, verb := range []string{"restore", "save"} {
		code, out := runCacheIn(env, wt, verb)
		if verb == "restore" && (code != 0 || !strings.Contains(out, "cache web: skipped: a folder above it is a link")) {
			t.Fatalf("%s through a link: %d %s", verb, code, out)
		}
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatalf("wrote outside the worktree: %v", ents)
	}
}

// The list comes from the main checkout, never the worktree's tree, and a
// line that leaves the worktree, or names .git or .maestro, is skipped.
func TestCacheReadsMainSetupAndRefusesUnsafePaths(t *testing.T) {
	root, git, env := cacheRepo(t, "<the folders an install fills>")
	if code, out := runCacheIn(env, root, "restore"); code != 0 || !strings.Contains(out, "nothing listed under ## Cache") {
		t.Fatalf("an empty section: %d %s", code, out)
	}
	wt := filepath.Join(t.TempDir(), "pr")
	git(root, "worktree", "add", "-q", "--detach", wt)
	os.WriteFile(filepath.Join(wt, ".maestro", "project", "project-setup.md"), []byte("## Cache\n\n"+nodeLine+"\n"), 0o644)
	fill(t, wt, "planted")
	if code, out := runCacheIn(env, wt, "save"); code != 0 || !strings.Contains(out, "nothing listed under ## Cache") {
		t.Fatalf("the PR's own setup was read: %d %s", code, out)
	}
	lines := []string{nodeLine, "- up: package-lock.json -> ../out", "- git: package-lock.json -> .git/hooks", "- abs: /etc/passwd -> node_modules",
		"- maestro: package-lock.json -> .maestro/skills", "- node: package-lock.json -> other", "- Bad Name: a -> b", "- nokey: -> node_modules", "- dots: a/../../b -> c"}
	entries, bad := cacheEntries("## cache\n" + strings.Join(lines, "\n") + "\n## Next\n- later: a -> b\n")
	if len(entries) != 1 || entries[0].Name != "node" || entries[0].Dir != "node_modules" || len(bad) != len(lines)-1 {
		t.Fatalf("entries %+v, %d bad: %v", entries, len(bad), bad)
	}
}

// Sessions that save one key at once keep one copy and leave no half one;
// a name keeps only its newest folders.
func TestCacheSavesAtOnceAndPrunes(t *testing.T) {
	root, git, env := cacheRepo(t, nodeLine)
	var wts []string
	for i := 0; i < 8; i++ {
		wt := filepath.Join(t.TempDir(), "w")
		git(root, "worktree", "add", "-q", "--detach", wt)
		fill(t, wt, "same")
		wts = append(wts, wt)
	}
	var wg sync.WaitGroup
	outs := make([]string, len(wts))
	for i, wt := range wts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, outs[i] = runCacheIn(env, wt, "save")
		}()
	}
	wg.Wait()
	saved := 0
	for _, o := range outs {
		switch {
		case strings.Contains(o, "cache node: saved ("):
			saved++
		case !strings.Contains(o, "cache node: already saved"):
			t.Errorf("a save at once: %s", o)
		}
	}
	dir := filepath.Join(filepath.Dir(env["MAESTRO_TICKET_STORE"]), "cache", "node")
	ents, _ := os.ReadDir(dir)
	if saved < 1 || len(ents) != 1 || strings.HasPrefix(ents[0].Name(), ".tmp-") {
		t.Fatalf("%d saved, %d folders kept: %v", saved, len(ents), ents)
	}
	// Five more keys, each older than the last: the three used last stay,
	// with a dead session's copy from two hours ago gone too.
	now := time.Now()
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, "old"+string(rune('a'+i)))
		os.MkdirAll(p, 0o755)
		os.Chtimes(p, now, now.Add(-time.Duration(i+1)*time.Hour))
	}
	dead := filepath.Join(dir, ".tmp-x-dead")
	os.MkdirAll(dead, 0o755)
	os.Chtimes(dead, now, now.Add(-2*time.Hour))
	pruneCache(filepath.Dir(dir), map[string]bool{"node": true}, now)
	ents, _ = os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if len(names) != 3 || !strings.Contains(strings.Join(names, " "), "olda") || !strings.Contains(strings.Join(names, " "), "oldb") {
		t.Fatalf("after prune: %v", names)
	}
	// A folder with a read-only folder in it goes whole, never by half,
	// and a name ## Cache no longer lists goes with all it kept.
	ro := filepath.Join(filepath.Dir(dir), "gone", "k1", "ro")
	os.MkdirAll(ro, 0o755)
	os.WriteFile(filepath.Join(ro, "f"), []byte("x"), 0o444)
	os.Chmod(ro, 0o555)
	pruneCache(filepath.Dir(dir), map[string]bool{"node": true}, now)
	if ents, _ := os.ReadDir(filepath.Join(filepath.Dir(dir), "gone")); len(ents) != 0 {
		t.Fatalf("a retired name kept: %v", ents)
	}
}

// A save right after others were used is kept: prune reads when a folder
// was last used, never the time cp carried over from the source.
func TestCacheSaveSurvivesPrune(t *testing.T) {
	root, git, env := cacheRepo(t, nodeLine)
	dir := filepath.Join(filepath.Dir(env["MAESTRO_TICKET_STORE"]), "cache", "node")
	now := time.Now()
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, "used"+string(rune('a'+i)))
		os.MkdirAll(p, 0o755)
		os.Chtimes(p, now, now.Add(-time.Minute))
	}
	wt := filepath.Join(t.TempDir(), "w")
	git(root, "worktree", "add", "-q", "--detach", wt)
	fill(t, wt, "new")
	old := now.Add(-20 * 24 * time.Hour)
	os.Chtimes(filepath.Join(wt, "node_modules"), old, old)
	if code, out := runCacheIn(env, wt, "save"); code != 0 || !strings.Contains(out, "cache node: saved (") {
		t.Fatalf("save: %d %s", code, out)
	}
	other := filepath.Join(t.TempDir(), "o")
	git(root, "worktree", "add", "-q", "--detach", other)
	if code, out := runCacheIn(env, other, "restore"); code != 0 || !strings.Contains(out, "cache node: restored, last used 0s ago") && !strings.Contains(out, "cache node: restored, last used 0m") {
		t.Fatalf("the new save was pruned: %d %s", code, out)
	}
}
