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

// runCache copies in, or keeps, the folders ## Cache lists, so a fresh
// review or QA worktree doesn't install and build what another run
// already did for the same lockfile. restore is anyone's; save is only a
// review's or QA's, from a clean checkout, as with a check's pass: a
// build's folder may hold what a session changed by hand.
func runCache(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) != 1 || (c.args[0] != "restore" && c.args[0] != "save") {
		return usagef("cache: want restore or save")
	}
	save := c.args[0] == "save"
	root, err := c.git("rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("%w: cache: not in a git worktree", ticket.ErrInvalid)
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
	dir, err := c.besideStore("cache")
	if err != nil {
		return err
	}
	if save {
		path, _, _ := c.storePath()
		if u := c.slotUser(filepath.Dir(path), nil); !slices.Contains(carriers, u.Job) {
			fmt.Fprintln(c.env.Stdout, "cache: not saved: only a review or QA run saves, so a folder a build changed by hand reaches no other session")
			return nil
		}
		if _, _, why := c.cleanTree(); why != "" {
			fmt.Fprintf(c.env.Stdout, "cache: not saved: %s (gitignore each cached folder)\n", why)
			return nil
		}
		min, err := c.minFree()
		if err != nil {
			return err
		}
		if err := c.headroom(path, min); err != nil {
			fmt.Fprintf(c.env.Stdout, "cache: not saved: %v\n", err)
			return nil
		}
	}
	for _, e := range entries {
		key, why := cacheKey(root, e)
		if why != "" {
			fmt.Fprintf(c.env.Stdout, "cache %s: skipped: %s\n", e.Name, why)
			continue
		}
		kept := filepath.Join(dir, e.Name, key)
		folder := filepath.Join(root, filepath.FromSlash(e.Dir))
		var msg string
		if save {
			msg = cacheSave(folder, kept)
		} else {
			msg = cacheRestore(folder, kept)
		}
		fmt.Fprintf(c.env.Stdout, "cache %s: %s (%s, key %s)\n", e.Name, msg, e.Dir, key[:12])
	}
	return nil
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
			if st, err := os.Stat(filepath.Join(w, ".maestro", "src")); err == nil && st.IsDir() {
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
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(k)))
		if err != nil {
			return "", "no " + k + " in this worktree"
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
func cacheRestore(folder, kept string) string {
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
	if err := copyTree(kept, tmp); err != nil {
		os.RemoveAll(tmp)
		return "miss: the copy failed: " + err.Error()
	}
	if err := os.Rename(tmp, folder); err != nil {
		os.RemoveAll(tmp)
		return "miss: " + err.Error()
	}
	now := time.Now()
	os.Chtimes(kept, now, now) // used: prune keeps it
	return "restored, saved " + roundMin(now.Sub(st.ModTime())) + " ago"
}

// cacheSave keeps the folder under its key, by rename from a copy beside
// it: of sessions that save one key at once, one wins and the rest drop
// their copy.
func cacheSave(folder, kept string) string {
	st, err := os.Lstat(folder)
	if err != nil || !st.IsDir() {
		return "not saved: no folder to keep"
	}
	if _, err := os.Stat(kept); err == nil {
		now := time.Now()
		os.Chtimes(kept, now, now)
		return "already saved"
	}
	parent := filepath.Dir(kept)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "not saved: " + err.Error()
	}
	tmp := filepath.Join(parent, ".tmp-"+filepath.Base(kept)+"-"+randHex())
	if err := copyTree(folder, tmp); err != nil {
		os.RemoveAll(tmp)
		return "not saved: the copy failed: " + err.Error()
	}
	if err := os.Rename(tmp, kept); err != nil {
		os.RemoveAll(tmp) // another session saved it first
		if _, serr := os.Stat(kept); serr == nil {
			return "already saved"
		}
		return "not saved: " + err.Error()
	}
	pruneCache(parent, time.Now())
	return "saved"
}

// pruneCache drops a name's folders unused past cacheKeep, all but the
// cacheMax used last, and copies a session that died left an hour ago.
func pruneCache(parent string, now time.Time) {
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
				os.RemoveAll(p)
			}
			continue
		}
		all = append(all, kept{p, info.ModTime()})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].used.After(all[j].used) })
	for i, k := range all {
		if i >= cacheMax || now.Sub(k.used) > cacheKeep {
			os.RemoveAll(k.path)
		}
	}
}

// copyTree copies src to dst, which must not exist, with cp: modes, links
// and times kept, and blocks shared where the filesystem can, so a copy of
// a large folder costs little time or space.
func copyTree(src, dst string) error {
	fast := []string{"-a"}
	switch runtime.GOOS {
	case "linux":
		fast = []string{"-a", "--reflink=auto"}
	case "darwin":
		fast = []string{"-a", "-c"}
	}
	run := func(flags []string) error {
		out, err := exec.Command("cp", append(flags, src, dst)...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, trim(strings.TrimSpace(string(out)), 200))
		}
		return nil
	}
	err := run(fast)
	if err != nil && len(fast) > 1 {
		os.RemoveAll(dst) // a cp with no such flag: copy plainly
		err = run([]string{"-a"})
	}
	return err
}

func randHex() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}
