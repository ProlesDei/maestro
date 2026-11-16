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
	runCacheIn(build, d, "restore")
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
	if code, out := runCacheIn(build, d, "restore"); code != 0 || !strings.Contains(out, "skipped: package-lock.json is a link") {
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

// A folder or key file whose path runs through a link, at any depth, is
// skipped: a PR's tree can point one anywhere, to read on save or to
// write on restore.
func TestCacheSkipsAPathThroughALink(t *testing.T) {
	root, git, env := cacheRepo(t, "- web: `package-lock.json` -> `apps/web/node_modules`\n- cfg: `cfg/lock` -> `vendor`")
	// Something kept under web's key, so a restore would have a copy to write.
	seed := filepath.Join(t.TempDir(), "seed")
	git(root, "worktree", "add", "-q", "--detach", seed)
	runCacheIn(env, seed, "restore")
	os.MkdirAll(filepath.Join(seed, "apps", "web", "node_modules"), 0o755)
	os.WriteFile(filepath.Join(seed, "apps", "web", "node_modules", "f"), []byte("x"), 0o644)
	if code, out := runCacheIn(env, seed, "save"); code != 0 || !strings.Contains(out, "cache web: saved (") {
		t.Fatalf("seed: %d %s", code, out)
	}
	wt, outside := filepath.Join(t.TempDir(), "wt"), t.TempDir()
	git(root, "worktree", "add", "-q", "--detach", wt)
	os.Symlink(outside, filepath.Join(wt, "apps"))
	os.Symlink(outside, filepath.Join(wt, "cfg"))
	os.WriteFile(filepath.Join(outside, "lock"), []byte("l"), 0o644)
	for _, verb := range []string{"restore", "save"} {
		code, out := runCacheIn(env, wt, verb)
		if verb == "restore" && (code != 0 || !strings.Contains(out, "cache web: skipped: apps is a link") || !strings.Contains(out, "cache cfg: skipped: cfg is a link")) {
			t.Fatalf("%s through a link: %d %s", verb, code, out)
		}
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 1 {
		t.Fatalf("wrote outside the worktree: %v", ents)
	}
}

// A folder is kept only under the key it was filled for: after a merge
// that moves the lockfile, with the install skipped because the folder
// is there, a save keeps nothing. So does a save where restore never ran.
func TestCacheSaveWantsTheKeyItWasFilledFor(t *testing.T) {
	root, git, env := cacheRepo(t, nodeLine)
	wt := filepath.Join(t.TempDir(), "w")
	git(root, "worktree", "add", "-q", "--detach", wt)
	fill(t, wt, "no restore ran")
	if code, out := runCacheIn(env, wt, "save"); code != 0 || !strings.Contains(out, "not saved: its key files changed since the folder was filled, or cache restore never ran") {
		t.Fatalf("a save with no restore: %d %s", code, out)
	}
	os.RemoveAll(filepath.Join(wt, "node_modules"))
	runCacheIn(env, wt, "restore")
	fill(t, wt, "installed for lock 1")
	// main moves the lockfile; the worktree merges it and keeps its folder.
	os.WriteFile(filepath.Join(root, "package-lock.json"), []byte("lock 2\n"), 0o644)
	git(root, "commit", "-qam", "lock 2")
	git(root, "push", "-q", "origin", "main")
	git(wt, "fetch", "-q", "origin")
	git(wt, "merge", "-q", "--ff", "origin/main")
	if code, out := runCacheIn(env, wt, "restore"); code != 0 || !strings.Contains(out, "kept: the folder is there") {
		t.Fatalf("restore over the old folder: %d %s", code, out)
	}
	if code, out := runCacheIn(env, wt, "save"); code != 0 || !strings.Contains(out, "not saved: its key files changed since the folder was filled") {
		t.Fatalf("an old folder under a new lockfile's key: %d %s", code, out)
	}
	// From a subfolder, a folder git tracks files in is still refused.
	os.RemoveAll(filepath.Join(wt, "node_modules"))
	runCacheIn(env, wt, "restore")
	fill(t, wt, "v")
	git(wt, "add", "-f", "node_modules/pkg/index.js")
	git(wt, "commit", "-qm", "planted")
	git(wt, "push", "-q", "origin", "HEAD:main")
	git(wt, "fetch", "-q", "origin")
	os.MkdirAll(filepath.Join(wt, "sub"), 0o755)
	if code, out := runCacheIn(env, filepath.Join(wt, "sub"), "save"); code != 0 || !strings.Contains(out, "not saved: git tracks files in the folder") {
		t.Fatalf("a tracked folder, from a subfolder: %d %s", code, out)
	}
}

// A copy still on its way in is taken out of its name before it is
// removed, so the save that is writing it keeps nothing, never half.
func TestCacheDropTakesACopyOutOfItsName(t *testing.T) {
	dir := t.TempDir()
	tmp, kept := filepath.Join(dir, ".tmp-0123-abc"), filepath.Join(dir, "0123456789abcdef0123456789abcdef")
	os.MkdirAll(filepath.Join(tmp, "half"), 0o755)
	if err := removeTree(tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, kept); err == nil {
		t.Fatal("a dropped copy could still be kept")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("left behind: %v", ents)
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
		runCacheIn(env, wt, "restore")
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
	dir := filepath.Join(filepath.Dir(env["MAESTRO_TICKET_STORE"]), "dep-cache", "node")
	ents, _ := os.ReadDir(dir)
	if saved < 1 || len(ents) != 1 || strings.HasPrefix(ents[0].Name(), ".tmp-") {
		t.Fatalf("%d saved, %d folders kept: %v", saved, len(ents), ents)
	}
	// Five more keys, each older than the last: the three used last stay,
	// with a dead session's copy from two hours ago gone too.
	now := time.Now()
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, strings.Repeat(string(rune('a'+i)), 32))
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
	if len(names) != 3 || !strings.Contains(strings.Join(names, " "), strings.Repeat("a", 32)) || !strings.Contains(strings.Join(names, " "), strings.Repeat("b", 32)) {
		t.Fatalf("after prune: %v", names)
	}
	// A folder with a read-only folder in it goes whole, never by half,
	// and a name ## Cache no longer lists goes with all it kept.
	ro := filepath.Join(filepath.Dir(dir), "gone", strings.Repeat("f", 32), "ro")
	// What this command didn't name stays, whatever the list says.
	os.MkdirAll(filepath.Join(filepath.Dir(dir), "gone", "theirs"), 0o755)
	os.MkdirAll(ro, 0o755)
	os.WriteFile(filepath.Join(ro, "f"), []byte("x"), 0o444)
	os.Chmod(ro, 0o555)
	pruneCache(filepath.Dir(dir), map[string]bool{"node": true}, now)
	if ents, _ := os.ReadDir(filepath.Join(filepath.Dir(dir), "gone")); len(ents) != 1 || ents[0].Name() != "theirs" {
		t.Fatalf("a retired name's folders: %v", ents)
	}
	// With a line that didn't parse, no name counts as retired.
	os.MkdirAll(filepath.Join(filepath.Dir(dir), "odd", strings.Repeat("e", 32)), 0o755)
	pruneCache(filepath.Dir(dir), nil, now)
	if ents, _ := os.ReadDir(filepath.Join(filepath.Dir(dir), "odd")); len(ents) != 1 {
		t.Fatalf("an unparsed name's folders went: %v", ents)
	}
}

// A save right after others were used is kept: prune reads when a folder
// was last used, never the time cp carried over from the source.
func TestCacheSaveSurvivesPrune(t *testing.T) {
	root, git, env := cacheRepo(t, nodeLine)
	dir := filepath.Join(filepath.Dir(env["MAESTRO_TICKET_STORE"]), "dep-cache", "node")
	now := time.Now()
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, strings.Repeat(string(rune('a'+i)), 32))
		os.MkdirAll(p, 0o755)
		os.Chtimes(p, now, now.Add(-time.Minute))
	}
	wt := filepath.Join(t.TempDir(), "w")
	git(root, "worktree", "add", "-q", "--detach", wt)
	runCacheIn(env, wt, "restore")
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
