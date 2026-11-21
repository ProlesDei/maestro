package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// routeLines are the lines pickup prints for a routed job, its reply
// aside: one for what happened, then the mismatch, each write, command
// and message to send.
func routeLines(rt routed) []string {
	on, st := "", ""
	if rt.Ticket != "" {
		on = " on " + rt.Ticket
	}
	if rt.Status != "" {
		st = " (" + rt.Status + ")"
	}
	reset := ""
	if rt.Reset != "" {
		reset = ", resets " + rt.Reset
	}
	lines := []string{fmt.Sprintf("%s %s %s%s%s%s: %s", rt.Run, rt.Job, rt.Outcome, on, st, reset, dash(rt.First))}
	if rt.Mismatch != "" {
		lines = append(lines, "mismatch: want "+rt.Mismatch)
	}
	for _, x := range rt.Wrote {
		lines = append(lines, "wrote: "+x)
	}
	for _, x := range rt.Do {
		lines = append(lines, "do: "+x)
	}
	for _, x := range rt.Send {
		lines = append(lines, x.String())
	}
	return lines
}

// ringDigest is the ring for bot at now: the ended runs left to it that
// it hasn't acked, oldest first, by id only. Only the chief of staff is
// rung, and its wake drops the body: it reads its own inbox. While STOP
// or a pause holds, a run waiting to go on is left out, so no ring wakes
// it only to be refused.
func ringDigest(co coord, dir, bot string, now time.Time) (runs.Notification, []string) {
	list, _ := runs.Unacked(dir, bot, now)
	_, held := co.startsHeld(now)
	keep := list[:0]
	for _, r := range list {
		var rt routed
		if held && r.Job != "" && len(r.Routed) > 0 && json.Unmarshal(r.Routed, &rt) == nil && doWaits(rt) {
			continue
		}
		keep = append(keep, r)
	}
	n := runs.NoticeFor(bot, keep)
	return n, n.Runs
}

// startsHeld says why no run may start now, as mayStart refuses: the halt
// stands, or a pause holds.
func (co coord) startsHeld(now time.Time) (string, bool) {
	if stopped, _ := co.s.stopped(); stopped {
		return "maestro/STOP is set", true
	}
	if p, ok := co.readPause(now); ok {
		return "jobs are paused until " + p.Until.Format(time.RFC3339), true
	}
	return "", false
}

// workerBots are the roles a run's end hands a fixed next job to.
var workerBots = []string{"junior-engineer", "senior-engineer", "reviewer", "qa-lead"}

// doWaits reports whether a run waits while starts are held: it has a do:
// line, or it ended done or its work landed and its end left a job for a
// worker role, which would only be refused, so neither pickup nor a ring
// shows its lines, nor does ack count it as more. The run that set the
// pause shows whole, so pickup tells the planner.
func doWaits(rt routed) bool {
	starts := len(rt.Do) > 0 || (rt.Outcome == outDone || rt.Landed) && slices.ContainsFunc(rt.Send, func(sd send) bool { return slices.Contains(workerBots, sd.Bot) })
	return starts && !slices.ContainsFunc(rt.Wrote, func(w string) bool { return strings.HasPrefix(w, "paused every job") })
}

// heldLine is pickup's one line for the runs doWaits holds.
func heldLine(n int, why string) string {
	return fmt.Sprintf("%d runs wait to go on while %s: nothing to do till then", n, why)
}

// waitingLine is pickup's one line for the runs a usage limit holds.
func waitingLine(n int, until time.Time) string {
	return fmt.Sprintf("%d runs wait for the usage limit to lift at %s: nothing to do till then; they ring you as it lifts", n, until.Format(time.RFC3339))
}

// runsCoord is the coord a supervisor or a sweep over the runs folder dir
// uses: the call's, or, if its store can't be read, the files beside dir,
// which still hold this machine's STOP.
func (c *call) runsCoord(dir string) coord {
	if co, err := c.coord(); err == nil {
		return co
	}
	return filesCoord(dir)
}

// filesCoord is the file coord beside the runs folder dir.
func filesCoord(dir string) coord {
	local := filepath.Dir(dir)
	return coord{s: fileCoord{dir: local, handouts: filepath.Join(local, "handouts"), runs: dir}}
}
