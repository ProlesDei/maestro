package cli

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// slotUse is who holds a slot or waits for one. Each holder and waiter
// writes it into the file it holds locked, so slot --status and look can
// say who is in line, for what, and since when.
type slotUse struct {
	PID    int       `json:"pid"`
	Run    string    `json:"run,omitempty"`
	Ticket string    `json:"ticket,omitempty"`
	Job    string    `json:"job,omitempty"`
	Actor  string    `json:"actor,omitempty"`
	Cmd    string    `json:"cmd"`
	Since  time.Time `json:"since"`
}

// slotUser describes this process as a slot's user: the run launch
// started it under, with that run's ticket and job, if any.
func (c *call) slotUser(sdir string, command []string) slotUse {
	u := slotUse{PID: os.Getpid(), Actor: c.actor, Cmd: trim(strings.Join(command, " "), 80)}
	if id := c.env.Getenv(runs.RunIDEnv); runs.ValidID(id) {
		u.Run = id
		if r, err := runs.Load(filepath.Join(sdir, "runs"), id); err == nil {
			u.Ticket, u.Job = r.Ticket, r.Job
			u.Actor = cmp.Or(u.Actor, r.Launcher)
		}
	}
	return u
}

// write puts u in f, in place of what an earlier holder left there. Best
// effort: a file with no use in it costs status a name, not a slot.
func (u slotUse) write(f *os.File) {
	b, err := json.Marshal(u)
	if err != nil {
		return
	}
	if f.Truncate(0) == nil {
		f.WriteAt(append(b, '\n'), 0)
	}
}

func (u slotUse) who() string {
	parts := []string{}
	for _, p := range []string{u.Run, u.Ticket, u.Job, u.Actor} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("pid %d", u.PID)
	}
	return strings.Join(parts, " ")
}

// slotGroup is one slot name's holders and its line, oldest first.
type slotGroup struct {
	Name    string    `json:"name"`
	Running []slotUse `json:"running"`
	Waiting []slotUse `json:"waiting"`
}

// deep reports whether more wait in g's line than twice the slots it has
// running: new work that would join it only waits longer.
func (g slotGroup) deep() bool { return len(g.Waiting) > 2*max(len(g.Running), 1) }

// oldest is how long the first in g's line has waited.
func (g slotGroup) oldest(now time.Time) time.Duration {
	if len(g.Waiting) == 0 || g.Waiting[0].Since.IsZero() {
		return 0
	}
	return now.Sub(g.Waiting[0].Since)
}

var (
	slotLockFile = regexp.MustCompile(`^([a-z0-9]+(?:-[a-z0-9]+)*)\.[0-9]+\.lock$`)
	slotLineFile = regexp.MustCompile(`^([a-z0-9]+(?:-[a-z0-9]+)*)\.q\.`)
)

// slotGroups reads every slot group in dir, or only the one named only.
// A file no one holds is free, or a dead waiter's, which it removes, as
// a waiter would.
func slotGroups(dir, only string) ([]slotGroup, error) {
	ents, err := os.ReadDir(dir) // sorted by name: each line oldest first
	if os.IsNotExist(err) {
		return []slotGroup{}, nil
	}
	if err != nil {
		return nil, err
	}
	groups := map[string]*slotGroup{}
	group := func(name string) *slotGroup {
		g := groups[name]
		if g == nil {
			g = &slotGroup{Name: name, Running: []slotUse{}, Waiting: []slotUse{}}
			groups[name] = g
		}
		return g
	}
	for _, e := range ents {
		base := e.Name()
		m, waiting := slotLockFile.FindStringSubmatch(base), false
		if m == nil {
			m, waiting = slotLineFile.FindStringSubmatch(base), true
		}
		if m == nil || (only != "" && m[1] != only) {
			continue
		}
		use, held := readHeld(filepath.Join(dir, base), waiting)
		if !held {
			continue
		}
		if g := group(m[1]); waiting {
			g.Waiting = append(g.Waiting, use)
		} else {
			g.Running = append(g.Running, use)
		}
	}
	out := []slotGroup{}
	for _, g := range groups {
		slices.SortStableFunc(g.Running, func(a, b slotUse) int { return a.Since.Compare(b.Since) })
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b slotGroup) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// readHeld reads the use in a slot or line file someone holds locked. To
// tell, it tries the lock: one it gets it lets go of at once, so a waiter
// that tries the slot meanwhile finds it busy and tries again a second on.
func readHeld(file string, line bool) (slotUse, bool) {
	f, err := os.Open(file)
	if err != nil {
		return slotUse{}, false
	}
	defer f.Close()
	if ok, err := tryLock(f); ok || err != nil {
		if ok && line {
			os.Remove(file) // a dead waiter's
		}
		return slotUse{}, false
	}
	var u slotUse
	b, _ := io.ReadAll(io.LimitReader(f, 4<<10))
	json.Unmarshal(b, &u) // a holder from before uses were written has none
	return u, true
}

// slotStatus prints every slot group's holders and line, or one group's.
func (c *call) slotStatus(only string) error {
	path, _, err := c.storePath()
	if err != nil {
		return err
	}
	groups, err := slotGroups(filepath.Join(filepath.Dir(path), "slots"), only)
	if err != nil {
		return err
	}
	now := time.Now()
	return c.out(map[string]any{"groups": groups}, func(w io.Writer) { printSlotGroups(w, groups, now) })
}

// maxSlotLines is how many waiters status names per group; the rest it
// counts.
const maxSlotLines = 10

func printSlotGroups(w io.Writer, groups []slotGroup, now time.Time) {
	if len(groups) == 0 {
		fmt.Fprintln(w, "no slot is in use")
		return
	}
	ago := func(u slotUse) string {
		if u.Since.IsZero() {
			return "?"
		}
		return roundMin(now.Sub(u.Since))
	}
	for _, g := range groups {
		fmt.Fprintf(w, "%s: %d running, %d waiting", g.Name, len(g.Running), len(g.Waiting))
		if len(g.Waiting) > 0 {
			fmt.Fprintf(w, ", the oldest %s", roundMin(g.oldest(now)))
		}
		fmt.Fprintln(w)
		for _, u := range g.Running {
			fmt.Fprintf(w, "  running %s %s: %s\n", ago(u), u.who(), u.Cmd)
		}
		for i, u := range g.Waiting {
			if i == maxSlotLines {
				fmt.Fprintf(w, "  and %d more waiting\n", len(g.Waiting)-i)
				break
			}
			fmt.Fprintf(w, "  waiting %s %s: %s\n", ago(u), u.who(), u.Cmd)
		}
	}
}
