package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// cacheEntry is one line under ## Cache in project-setup.md: a folder an
// install or build fills, and the files that decide what goes in it.
type cacheEntry struct {
	Name string   // a slot-style name, such as node
	Keys []string // files, from the worktree's root, whose content keys the folder
	Dir  string   // the folder, from the worktree's root
}

const (
	// cacheKeep is how long a saved folder stays with no one using it,
	// and cacheMax how many a name keeps: a lockfile changes rarely, and
	// each copy may be hundreds of megabytes.
	cacheKeep = 14 * 24 * time.Hour
	cacheMax  = 3
)

// cacheSavers are the jobs that may keep a folder: a run that tests in a
// worktree it made fresh. A live run reuses no cache, and a build's
// folder may hold what a session changed by hand.
var cacheSavers = []string{"review", "qa"}

// runCache copies in, keeps or drops the folders ## Cache lists, so a
// fresh worktree doesn't install what another run already did for the
// same lockfile. restore is anyone's. save is only for a review or QA run
// whose checkout is clean and holds a tree the main branch has: a folder
// is keyed by its lockfile alone, so one filled while a PR's code ran
// must reach no other change until that code is reviewed, tested and
// merged. In practice QA saves, right after its pass merges.
func runCache(ctx context.Context, c *call) error {
	at := c.fs.String("at", "", "save: the commit on the main branch whose tree this checkout holds (default: origin/<main>)")
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) < 1 || !slices.Contains([]string{"restore", "save", "drop"}, c.args[0]) || len(c.args) > 2 || (len(c.args) == 2 && c.args[0] != "drop") {
		return usagef("cache: want restore, save [--at SHA] or drop [<name>]")
	}
	verb := c.args[0]
	dir, err := c.besideStore("cache")
	if err != nil {
		return err
	}
	if verb == "drop" {
		// A kept folder that turned out bad: the next QA merge saves anew.
		target := dir
		if len(c.args) == 2 {
			if !slotName.MatchString(c.args[1]) {
				return usagef("cache drop: name %q: use lowercase letters, digits and single hyphens", c.args[1])
			}
			target = filepath.Join(dir, c.args[1])
		}
		n := 0
		filepath.WalkDir(target, func(p string, e os.DirEntry, err error) error {
			if err != nil || !e.IsDir() || p == target {
				return nil
			}
			if filepath.Dir(p) == dir && target == dir {
				return nil // a name's folder: its keys are one level down
			}
			if removeTree(p) == nil && !strings.HasPrefix(e.Name(), ".tmp-") {
				n++
			}
			return filepath.SkipDir
		})
		fmt.Fprintf(c.env.Stdout, "cache: dropped %d kept folders\n", n)
		return nil
	}
	root, err := c.git("rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("%w: cache: not in a git worktree", ticket.ErrInvalid)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	setup, err := c.mainSetup()
	if err != nil {
		return err
	}
	entries, bad := cacheEntries(setup)
	for _, b := range bad {
		fmt.Fprintln(c.env.Stderr, "maestro-ticket: cache: skipped: "+b)
	}
	if len(entries) == 0 {
		fmt.Fprintln(c.env.Stdout, "cache: nothing listed under ## Cache in project-setup.md")
		return nil
	}
	if verb == "save" {
		if why, err := c.mayNotSave(setup, *at); err != nil {
			return err
		} else if why != "" {
			fmt.Fprintln(c.env.Stdout, "cache: not saved: "+why)
			return nil
		}
	}
	for _, e := range entries {
		folder := filepath.Join(root, filepath.FromSlash(e.Dir))
		key, why := cacheKey(root, e)
		if why == "" {
			why = strayParent(root, folder)
		}
		if why != "" {
			fmt.Fprintf(c.env.Stdout, "cache %s: skipped: %s\n", e.Name, why)
			continue
		}
		kept := filepath.Join(dir, e.Name, key)
		var msg string
		if verb == "save" {
			if tracked, _ := c.git("ls-files", "--", e.Dir); tracked != "" {
				msg = "not saved: git tracks files in the folder"
			} else {
				msg = cacheSave(ctx, folder, kept)
			}
		} else {
			msg = cacheRestore(ctx, folder, kept)
		}
		fmt.Fprintf(c.env.Stdout, "cache %s: %s (%s, key %s)\n", e.Name, msg, e.Dir, key[:12])
	}
	if verb == "save" {
		names := map[string]bool{}
		for _, e := range entries {
			names[e.Name] = true
		}
		pruneCache(dir, names, time.Now())
	}
	return nil
}

// mayNotSave says why this checkout may keep no folder, or "" when it may.
func (c *call) mayNotSave(setup, at string) (string, error) {
	path, _, _ := c.storePath()
	if u := c.slotUser(filepath.Dir(path), nil); !slices.Contains(cacheSavers, u.Job) {
		return "only a review or QA run saves, so a folder a build changed by hand reaches no other session", nil
	}
	tree, _, why := c.cleanTree()
	if why != "" {
		return why + " (gitignore each cached folder)", nil
	}
	main := setupLine(setup, "main branch")
	if main == "" {
		return "no branch under ## Main branch in project-setup.md", nil
	}
	ref := "refs/remotes/origin/" + main
	if at == "" {
		at = ref
	} else if !shaPattern.MatchString(at) {
		return "", usagef("cache save: --at wants a commit sha, not %q", at)
	}
	// The commit named is on main, and this checkout holds its tree: what
	// filled the folder is code that was reviewed, tested and merged.
	on, err := c.git("rev-parse", "--verify", "--quiet", at+"^{tree}")
	if err != nil {
		return "no commit " + at + " here: fetch first", nil
	}
	if _, err := c.git("merge-base", "--is-ancestor", at, ref); err != nil {
		return at + " is not on " + main + " as last fetched", nil
	}
	if on != tree {
		return "this checkout's tree is not " + main + "'s, so its folders may hold what an unmerged change wrote: QA saves after its pass merges", nil
	}
	min, err := c.minFree()
	if err != nil {
		return "", err
	}
	if err := c.headroom(path, min); err != nil {
		return err.Error(), nil
	}
	return "", nil
}

// setupLine is the first line of text under a ## heading of
// project-setup.md, as install.sh check reads one: the heading's case, a
// leading bullet and backticks don't count.
func setupLine(setup, heading string) string {
	on := false
	for _, l := range strings.Split(lineBreaks.Replace(setup), "\n") {
		l = strings.TrimSpace(l)
		if h, ok := strings.CutPrefix(l, "## "); ok {
			on = strings.EqualFold(strings.TrimSpace(h), heading)
			continue
		}
		if on && l != "" && !strings.HasPrefix(l, "#") && !strings.HasPrefix(l, "<") {
			return strings.Trim(strings.TrimLeft(l, "-* "), "`* ")
		}
	}
	return ""
}

// strayParent says why a folder's path can't be trusted: a part of it
// above the folder is a link, which a PR's tree can point anywhere.
func strayParent(root, folder string) string {
	parent := filepath.Dir(folder)
	real, err := filepath.EvalSymlinks(parent)
	if errors.Is(err, os.ErrNotExist) {
		return "" // restore makes it
	}
	if err != nil || real != parent {
		return "a folder above it is a link"
	}
	return ""
}

// mainSetup reads project-setup.md from the main checkout, never this
// worktree's tree: a PR could list a folder there to plant, or to read.
// Where the main repo is bare, the install sits in the linked worktree
// that holds .maestro/src.
func (c *call) mainSetup() (string, error) {
	out, err := c.git("worktree", "list", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("%w: cache: %v", ticket.ErrInvalid, err)
	}
	var wts []string
	bare := false
	for _, l := range strings.Split(out, "\n") {
		if w, ok := strings.CutPrefix(l, "worktree "); ok {
			wts = append(wts, w)
		}
		if l == "bare" && len(wts) == 1 {
			bare = true
		}
	}
	if len(wts) == 0 {
		return "", fmt.Errorf("%w: cache: git lists no worktree", ticket.ErrInvalid)
	}
	main := wts[0]
	if bare {
		for _, w := range wts[1:] {
			// Its .git: git tracks no path of that name, so no PR's tree
			// can pass for the install.
			if _, err := os.Lstat(filepath.Join(w, ".maestro", "src", ".git")); err == nil {
				main = w
				break
			}
		}
	}
	b, err := os.ReadFile(filepath.Join(main, ".maestro", "project", "project-setup.md"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// cacheEntries reads the bullets under ## Cache: a name, a colon, the key
// files joined by commas, ->, and the folder; backticks don't count. It
// returns the lines it can't use, each with why.
func cacheEntries(setup string) (entries []cacheEntry, bad []string) {
	on := false
	seen := map[string]bool{}
	for _, l := range strings.Split(lineBreaks.Replace(setup), "\n") {
		l = strings.TrimSpace(l)
		if h, ok := strings.CutPrefix(l, "## "); ok {
			on = strings.EqualFold(strings.TrimSpace(h), "cache")
			continue
		}
		if !on || !(strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ")) {
			continue
		}
		l = strings.ReplaceAll(strings.TrimSpace(l[2:]), "`", "")
		name, rest, ok := strings.Cut(l, ":")
		keys, dir, ok2 := strings.Cut(rest, "->")
		e := cacheEntry{Name: strings.TrimSpace(name), Dir: strings.TrimSpace(dir)}
		for _, k := range strings.Split(keys, ",") {
			if k = strings.TrimSpace(k); k != "" {
				e.Keys = append(e.Keys, k)
			}
		}
		switch {
		case !ok || !ok2 || len(e.Keys) == 0 || e.Dir == "":
			bad = append(bad, fmt.Sprintf("%q: want <name>: <key file>[, <key file>] -> <folder>", l))
		case !slotName.MatchString(e.Name):
			bad = append(bad, fmt.Sprintf("%q: the name takes lowercase letters, digits and single hyphens", l))
		case seen[e.Name]:
			bad = append(bad, fmt.Sprintf("%q: the name %s is listed twice", l, e.Name))
		case !slices.ContainsFunc(append(slices.Clone(e.Keys), e.Dir), unsafeRel):
			seen[e.Name] = true
			entries = append(entries, e)
		default:
			bad = append(bad, fmt.Sprintf("%q: every path stays inside the worktree, and outside .git and .maestro", l))
		}
	}
	return entries, bad
}

// unsafeRel reports a path that isn't a plain one inside the worktree.
func unsafeRel(p string) bool {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	if p == "" || clean != strings.TrimSuffix(p, "/") || clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "/") {
		return true
	}
	first, _, _ := strings.Cut(clean, "/")
	return first == ".." || first == ".git" || first == ".maestro"
}

// cacheKey names the folder's content: the entry, the platform, and each
// key file's bytes. A missing key file leaves the folder unkeyed.
func cacheKey(root string, e cacheEntry) (key, why string) {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s/%s\x00%s", e.Name, runtime.GOOS, runtime.GOARCH, e.Dir)
	for _, k := range e.Keys {
		p := filepath.Join(root, filepath.FromSlash(k))
		if st, err := os.Lstat(p); err != nil {
			return "", "no " + k + " in this worktree"
		} else if !st.Mode().IsRegular() {
			return "", k + " is no plain file"
		}
		f, err := os.Open(p)
		if err != nil {
			return "", "can't read " + k
		}
		fmt.Fprintf(h, "\x00%s\x00", k)
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", "can't read " + k
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:32], ""
}

// cacheRestore copies the kept folder in, by rename from a copy beside
// it, so a copy that dies halfway never looks like a whole folder.
func cacheRestore(ctx context.Context, folder, kept string) string {
	// What a restore that died here left an hour ago or more.
	if old, _ := filepath.Glob(folder + ".maestro-cache-*"); len(old) > 0 {
		for _, o := range old {
			if st, err := os.Lstat(o); err == nil && time.Since(st.ModTime()) > time.Hour {
				removeTree(o)
			}
		}
	}
	if _, err := os.Lstat(folder); err == nil {
		return "kept: the folder is there"
	}
	st, err := os.Stat(kept)
	if err != nil || !st.IsDir() {
		return "miss"
	}
	tmp := folder + ".maestro-cache-" + randHex()
	if err := os.MkdirAll(filepath.Dir(folder), 0o755); err != nil {
		return "miss: " + err.Error()
	}
	if err := copyTree(ctx, kept, tmp); err != nil {
		removeTree(tmp)
		return "miss: the copy failed: " + err.Error()
	}
	if err := os.Rename(tmp, folder); err != nil {
		removeTree(tmp)
		return "miss: " + err.Error()
	}
	now := time.Now()
	os.Chtimes(kept, now, now) // used: prune keeps it
	return "restored, last used " + roundMin(now.Sub(st.ModTime())) + " ago"
}

// cacheSave keeps the folder under its key, by rename from a copy beside
// it: of sessions that save one key at once, one wins and the rest drop
// their copy.
func cacheSave(ctx context.Context, folder, kept string) string {
	st, err := os.Lstat(folder)
	if err != nil || !st.IsDir() {
		return "not saved: no folder to keep"
	}
	now := time.Now()
	if _, err := os.Stat(kept); err == nil {
		os.Chtimes(kept, now, now)
		return "already saved"
	}
	if l := absLink(folder); l != "" {
		return "not saved: " + l + " links to an absolute path, which another worktree can't use"
	}
	parent := filepath.Dir(kept)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "not saved: " + err.Error()
	}
	tmp := filepath.Join(parent, ".tmp-"+filepath.Base(kept)+"-"+randHex())
	if err := copyTree(ctx, folder, tmp); err != nil {
		removeTree(tmp)
		return "not saved: the copy failed: " + err.Error()
	}
	// cp keeps the folder's own time; prune reads this one as last used.
	os.Chtimes(tmp, now, now)
	if err := os.Rename(tmp, kept); err != nil {
		removeTree(tmp) // another session saved it first
		if _, serr := os.Stat(kept); serr == nil {
			return "already saved"
		}
		return "not saved: " + err.Error()
	}
	return "saved"
}

// absLink names a link in the folder that points to an absolute path, or
// "" when none does.
func absLink(folder string) string {
	found := ""
	filepath.WalkDir(folder, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.Type()&os.ModeSymlink == 0 {
			return nil
		}
		if t, err := os.Readlink(p); err == nil && filepath.IsAbs(t) {
			found, _ = filepath.Rel(folder, p)
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// pruneCache drops, for each name ## Cache lists, the folders unused past
// cacheKeep and all but the cacheMax used last; every folder of a name it
// no longer lists; and copies a session that died left an hour ago.
func pruneCache(dir string, names map[string]bool, now time.Time) {
	nameDirs, _ := os.ReadDir(dir)
	for _, nd := range nameDirs {
		if !nd.IsDir() {
			continue
		}
		parent := filepath.Join(dir, nd.Name())
		ents, _ := os.ReadDir(parent)
		type kept struct {
			path string
			used time.Time
		}
		var all []kept
		for _, e := range ents {
			info, err := e.Info()
			if err != nil {
				continue
			}
			p := filepath.Join(parent, e.Name())
			if strings.HasPrefix(e.Name(), ".tmp-") {
				if now.Sub(info.ModTime()) > time.Hour {
					removeTree(p)
				}
				continue
			}
			all = append(all, kept{p, info.ModTime()})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].used.After(all[j].used) })
		for i, k := range all {
			if !names[nd.Name()] || i >= cacheMax || now.Sub(k.used) > cacheKeep {
				removeTree(k.path)
			}
		}
	}
}

// removeTree deletes a folder so that no reader ever finds half of it: it
// goes out of its name first, by rename, then is made writable (a module
// cache holds read-only folders) and removed.
func removeTree(path string) error {
	gone := path
	if base := filepath.Base(path); !strings.HasPrefix(base, ".tmp-") {
		gone = filepath.Join(filepath.Dir(path), ".tmp-drop-"+randHex())
		if err := os.Rename(path, gone); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
	}
	filepath.WalkDir(gone, func(p string, e os.DirEntry, err error) error {
		if err == nil && e.IsDir() {
			os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(gone)
}

// copyTree copies src to dst, which must not exist, with cp: modes, links
// and times kept, and blocks shared where the filesystem can, so a copy of
// a large folder costs little time or space.
func copyTree(ctx context.Context, src, dst string) error {
	fast := []string{"-a"}
	switch runtime.GOOS {
	case "linux":
		fast = []string{"-a", "--reflink=auto"}
	case "darwin":
		fast = []string{"-a", "-c"}
	}
	run := func(flags []string) (string, error) {
		out, err := exec.CommandContext(ctx, "cp", append(append(flags, "--"), src, dst)...).CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("%v: %s", err, trim(strings.TrimSpace(string(out)), 200))
		}
		return "", nil
	}
	out, err := run(fast)
	// Only a cp that doesn't know the flag gets a plain second try.
	if err != nil && len(fast) > 1 && strings.Contains(out, "option") {
		removeTree(dst)
		_, err = run([]string{"-a"})
	}
	return err
}

func randHex() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}
