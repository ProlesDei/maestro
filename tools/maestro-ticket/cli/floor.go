package cli

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// Admission floors: rules set floor inodes <pct> and floor load <n> keep
// the machine from taking more runs than it holds. While its free inodes
// are under the floor, or its 1-minute load average over it, look hands
// out and starts nothing and job, job again and resume refuse with
// paused, as under a usage limit; runs going already go on. Unset, no
// floor holds: claim and slot keep their own margin (MAESTRO_TICKET_MIN_FREE,
// 10% by default), which this never tightens.

// admit is mayStart for a start of a new run: stopped, paused, or past a
// floor, it refuses. The Stop hook reads mayStart alone, so a session
// already going is still held to its verdict past a floor.
func (c *call) admit() error {
	if err := c.mayStart(); err != nil {
		return err
	}
	if _, why := c.overFloor(); why != "" {
		return fmt.Errorf("%w: paused: %s: start no run until it clears", ticket.ErrPastFloor, why)
	}
	return nil
}

// overFloor names the floor the machine is past, inodes or load, and
// why, or "". Each filesystem a run writes to counts: the store's, the
// temp dir's and the working directory's. What it can't measure passes.
func (c *call) overFloor() (floor, why string) {
	dir, err := c.standingDir()
	if err != nil {
		return "", ""
	}
	b, _, err := readBoard(dir)
	if err != nil {
		return "", ""
	}
	if b.FloorInodes != nil && *b.FloorInodes > 0 {
		free := c.diskFree()
		for _, d := range []string{dir, c.tempDir(), c.env.Dir} {
			f, ok, err := free(d)
			if err != nil || !ok || f.Inodes < 0 {
				continue
			}
			if f.Inodes < float64(*b.FloorInodes) {
				return "inodes", fmt.Sprintf("inodes: %s has %.1f%% of its inodes free, under the %d%% floor", d, f.Inodes, *b.FloorInodes)
			}
		}
	}
	if b.FloorLoad != nil && *b.FloorLoad > 0 {
		load := envcheck.LoadAvg
		if c.env.LoadAvg != nil {
			load = c.env.LoadAvg
		}
		if l, ok := load(); ok && l > *b.FloorLoad {
			return "load", fmt.Sprintf("load: the 1-minute load average is %.1f, over the %s floor", l, loadText(*b.FloorLoad))
		}
	}
	return "", ""
}

// setFloor is rules set floor inodes <pct>|load <n>; 0 drops the floor.
func (c *call) setFloor(args []string) error {
	if len(args) != 2 || (args[0] != "inodes" && args[0] != "load") {
		return usagef("rules set floor: want inodes <pct> (such as 15%%) or load <n>; 0 drops it")
	}
	kind, val := args[0], args[1]
	var pct int
	var load float64
	var err error
	if kind == "inodes" {
		pct, err = strconv.Atoi(strings.TrimSuffix(val, "%"))
		if err != nil || pct < 0 || pct > 99 {
			return usagef("rules set floor inodes: want a whole percent from 0 to 99, such as 15%%")
		}
	} else {
		load, err = strconv.ParseFloat(val, 64)
		if err != nil || load < 0 || math.IsInf(load, 0) || math.IsNaN(load) {
			return usagef("rules set floor load: want a 1-minute load average, 0 or more")
		}
	}
	if err := c.refuseRulesWrite("floor"); err != nil {
		return err
	}
	dir, err := c.standingDir()
	if err != nil {
		return err
	}
	unlock, err := lockBoard(dir)
	if err != nil {
		return err
	}
	defer unlock()
	b, _, err := readBoard(dir)
	if err != nil {
		return err
	}
	var old, now string
	if kind == "inodes" {
		old, now = pctText(b.FloorInodes), "none"
		b.FloorInodes = nil
		if pct > 0 {
			b.FloorInodes, now = &pct, pctText(&pct)
		}
	} else {
		old, now = loadPtrText(b.FloorLoad), "none"
		b.FloorLoad = nil
		if load > 0 {
			b.FloorLoad, now = &load, loadText(load)
		}
	}
	if err := c.writeBoard(dir, b); err != nil {
		return err
	}
	if err := c.logRules(dir, "floor_"+kind, old, now); err != nil {
		return err
	}
	return c.out(map[string]any{"key": "floor_" + kind, "value": now, "was": old}, func(w io.Writer) {
		fmt.Fprintf(w, "floor %s: %s (was %s)\n", kind, now, old)
	})
}

// floorText is rules show's floor line.
func floorText(b board) string {
	var l []string
	if b.FloorInodes != nil {
		l = append(l, "inodes "+pctText(b.FloorInodes))
	}
	if b.FloorLoad != nil {
		l = append(l, "load "+loadText(*b.FloorLoad))
	}
	if len(l) == 0 {
		return "none"
	}
	return strings.Join(l, ", ")
}

func pctText(p *int) string {
	if p == nil {
		return "none"
	}
	return strconv.Itoa(*p) + "%"
}

func loadText(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func loadPtrText(p *float64) string {
	if p == nil {
		return "none"
	}
	return loadText(*p)
}
