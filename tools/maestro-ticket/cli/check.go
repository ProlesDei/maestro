package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// checkPass is the record of a check that passed on one tree: the files
// the command ran on, named by git's tree id, so any session whose
// checkout holds the same files can carry it instead of running it again.
type checkPass struct {
	Name    string    `json:"name"`
	Tree    string    `json:"tree"`
	Dir     string    `json:"dir,omitempty"` // where it ran, from the repo root
	Head    string    `json:"head"`
	Command []string  `json:"command"`
	At      time.Time `json:"at"`
	Took    string    `json:"took"`
	Run     string    `json:"run,omitempty"`
	Ticket  string    `json:"ticket,omitempty"`
	Job     string    `json:"job,omitempty"`
	Actor   string    `json:"actor,omitempty"`
}

// checkKeep is how long a pass stays on record.
const checkKeep = 14 * 24 * time.Hour

// carriers are the jobs whose passes carry: review and QA test in a
// worktree they made fresh at the sha. A build's checkout may hold what a
// fresh one lacks, such as a package installed by hand, so an engineer's
// own pass never stands in for the reviewer's run.
var carriers = []string{"review", "qa", "live"}

// runCheck runs a check through a slot, as slot does, unless a review or
// QA run passed the same command on the same tree before: then it says
// where and exits 0 without running it. A pass is recorded only from such
// a run, from a clean checkout, and only if the run left it clean on the
// same tree; a failure never is, so a red check always runs again.
func runCheck(ctx context.Context, c *call) error {
	var command []string
	if i := slices.Index(c.args, "--"); i >= 0 {
		c.args, command = c.args[:i], c.args[i+1:]
	}
	slot := c.fs.String("slot", "checks", "slot group the command waits in, as slot's --name")
	max := c.fs.Int("max", 4, "how many commands in the slot group run at once")
	wait := c.fs.Duration("wait", 0, "give up after this long with no free slot (0: wait until one frees)")
	fresh := c.fs.Bool("fresh", false, "run it even if it passed on this tree, and record the new result")
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) != 1 {
		return usagef("check: name the check, as ## Checks does, such as: check test --max 2 -- make test")
	}
	name := c.args[0]
	if !slotName.MatchString(name) {
		return usagef("check: name %q: use lowercase letters, digits and single hyphens", name)
	}
	if len(command) == 0 {
		return usagef("check: name the command to run after --")
	}
	if err := checkSlotFlags("check", *slot, *max, *wait); err != nil {
		return err
	}
	dir, err := c.besideStore("checks")
	if err != nil {
		return err
	}
	tree, head, why := c.cleanTree()
	// The same command in another folder runs other tests: a monorepo's
	// make test in a/ says nothing of b/.
	sub, _ := c.git("rev-parse", "--show-prefix")
	key := checkKey(name, tree, sub, command)
	if why == "" && !*fresh {
		if p, ok := loadCheckPass(dir, key); ok && p.Tree == tree && p.Dir == sub && slices.Equal(p.Command, command) {
			fmt.Fprintf(c.env.Stdout, "check %s: carried: passed on tree %s (head %s) %s ago, by %s, in %s; not run again (--fresh runs it)\n",
				name, abbrev(tree), abbrev(p.Head), roundMin(time.Since(p.At)), p.who(), p.Took)
			return nil
		}
	}
	begun := time.Now()
	err = c.inSlot(ctx, *slot, *max, *wait, command)
	if _, failed := err.(silentExit); failed && why == "" {
		// It failed on this tree: a pass kept from before, which --fresh
		// passed over, must not carry to the next session.
		os.Remove(filepath.Join(dir, key+".json"))
	}
	if err != nil {
		return err
	}
	path, _, _ := c.storePath()
	u := c.slotUser(filepath.Dir(path), command)
	if !slices.Contains(carriers, u.Job) {
		return nil // only review and QA passes carry
	}
	if why != "" {
		fmt.Fprintf(c.env.Stderr, "maestro-ticket: check %s: passed, not recorded: %s\n", name, why)
		return nil
	}
	if after, _, why := c.cleanTree(); why != "" || after != tree {
		fmt.Fprintf(c.env.Stderr, "maestro-ticket: check %s: passed, not recorded: the run changed the checkout\n", name)
		return nil
	}
	p := checkPass{Name: name, Tree: tree, Dir: sub, Head: head, Command: command, At: time.Now().UTC(),
		Took: roundSec(time.Since(begun)), Run: u.Run, Ticket: u.Ticket, Job: u.Job, Actor: u.Actor}
	if err := saveCheckPass(dir, key, p); err != nil {
		// The check passed: a lost record costs the next session a run.
		fmt.Fprintf(c.env.Stderr, "maestro-ticket: check %s: passed, not recorded: %v\n", name, err)
	}
	return nil
}

// cleanTree returns the tree and head of the checkout the command runs
// in, or why a pass there can't be carried: no git, or changes git sees
// (tracked or untracked, not ignored), which the tree id leaves out.
func (c *call) cleanTree() (tree, head, why string) {
	tree, err := c.git("rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", "", "not in a git checkout with a commit"
	}
	head, err = c.git("rev-parse", "HEAD")
	if err != nil {
		return "", "", "not in a git checkout with a commit"
	}
	st, err := c.git("status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return "", "", "git status failed: " + err.Error()
	}
	if st != "" {
		return "", "", "the checkout has changes git hasn't committed"
	}
	return tree, head, ""
}

func checkKey(name, tree, sub string, command []string) string {
	h := sha256.New()
	h.Write([]byte(name + "\x00" + tree + "\x00" + sub))
	for _, a := range command {
		h.Write([]byte("\x00" + a))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func loadCheckPass(dir, key string) (checkPass, bool) {
	b, err := os.ReadFile(filepath.Join(dir, key+".json"))
	if err != nil {
		return checkPass{}, false
	}
	var p checkPass
	if json.Unmarshal(b, &p) != nil || time.Since(p.At) > checkKeep {
		return checkPass{}, false
	}
	return p, true
}

// saveCheckPass writes p by rename, so a reader never sees half a record,
// and drops records past checkKeep.
func saveCheckPass(dir, key string, p checkPass) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, key+".tmp.*")
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(dir, key+".json"))
	}
	if err != nil {
		os.Remove(f.Name())
		return err
	}
	pruneChecks(dir, time.Now())
	return nil
}

// pruneChecks removes passes past checkKeep, and temp files a writer that
// died left an hour ago or more.
func pruneChecks(dir string, now time.Time) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue
		}
		age := now.Sub(info.ModTime())
		if (strings.HasSuffix(e.Name(), ".json") && age > checkKeep) || (strings.Contains(e.Name(), ".tmp.") && age > time.Hour) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func (p checkPass) who() string {
	parts := []string{}
	for _, s := range []string{p.Run, p.Ticket, p.Job, p.Actor} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "a session outside launch"
	}
	return strings.Join(parts, " ")
}

func abbrev(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func roundSec(d time.Duration) string { return d.Round(time.Second).String() }
