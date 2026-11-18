package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// A ring's body is the routine's own input, a stronger place for a planted
// line than pickup's output. So it carries a run's lines only when each
// matches one of these fixed shapes; a run with any other line, a reply, or
// no stored route rides whole in pickup instead, behind one
// "card waiting: run pickup" line. Every shape is anchored and bounded.
var (
	bodyDo = regexp.MustCompile(`^do: maestro-ticket (resume R-[a-z2-7]{8} --(died|go-on|opus|escalate|decide)|job again R-[a-z2-7]{8}( --full)?( --limit [0-9]+[hm]([0-9]+m)?)?)$`)
	limitD = `[0-9]+[hms](?:[0-9]+[ms]){0,2}`
	rfc    = `[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:Z|[+-][0-9]{2}:[0-9]{2})`
	tid    = `T-[0-9]{1,9}`
	rid    = `R-[a-z2-7]{8}`
	// bodySend are the payloads a send may carry, after "send <bot>: ".
	bodySend = regexp.MustCompile(`^(?:` + strings.Join([]string{
		`(?:build|review|qa|live|design) ` + tid,
		`kickback ` + tid + ` tries [0-9]{1,4}`,
		`(?:mismatch|live fail|merged|slot free|died twice) ` + tid,
		`died twice`,
		`goal [a-z0-9][a-z0-9-]{0,59} done`,
		`timed_out ` + tid + `(?: after its verdict|: no Handoff: sha to name in an Escalation)?`,
		`(?:failed|timed_out|refused|question|died) (?:` + tid + `|` + rid + `)`,
		`usage_limit(?: ` + tid + `)?(?: resets (?:` + rfc + `|[0-9]{1,2}(?::[0-9]{2})? ?[ap]m))?`,
		`block ` + tid + `: (?:review ran past ` + limitD + `|QA ran past ` + limitD + ` twice)`,
	}, "|") + `)$`)
	// bodyFault are the fault lines a send to the chief of staff may carry:
	// a run that keeps failing, never a card.
	bodyFault = regexp.MustCompile(`^(?:` + strings.Join([]string{
		`(?:failed twice|failed|refused) (?:plan|rule|finish) ` + rid + `(?: ` + tid + `| goal [a-z0-9][a-z0-9-]{0,59})?`,
		`[a-z][a-z-]{0,11} (?:failed|timed_out|refused|question) ` + rid + `(?: ` + tid + `)?: nothing routed`,
	}, "|") + `)$`)
	// bodyWrote are the writes a route reports, after "wrote: ".
	bodyWrote = regexp.MustCompile(`^(?:` + strings.Join([]string{
		`paused every job until ` + rfc,
		`released ` + tid + ` (?:till the limit lifts|for the planner|for the retry)`,
		tid + ` moved on`,
		tid + ` sent up: (?:failed twice|failed|refused)`,
		tid + ` sent up: design ready`,
		tid + ` waits on the card, sent up`,
		tid + ` blocked: its builds (?:stopped 3 times|timed out or failed twice)`,
		tid + ` blocked: its [a-z]{1,12} ended 3 times with no verdict`,
		tid + ` blocked: QA ran past ` + limitD + ` twice`,
		tid + ` blocked: Escalation: review at [0-9a-f]{7,40}: ran past ` + limitD,
		`[0-9]{1,4} follow-ups from ` + tid + ` on ` + tid,
	}, "|") + `)$`)
)

// cardWaiting is the one line a body carries for every run it holds.
const cardWaiting = "card waiting: run pickup"

// maxBodyLines bounds the bytes of a body's lines, so the whole body stays
// under 4 KB with its ids and ack.
const maxBodyLines = 3 << 10

// routeLines are the lines pickup prints for a routed job, its reply
// aside: one for what happened, then the mismatch, each write, command
// and message to send. The ring's body takes its lines from here too.
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

// bodyLines are the lines of rt a ring's body may carry, and whether it
// may carry the run at all. It leaves out the head line and the mismatch
// detail, which only inform: what to do rides on the sends. A reply, or
// any line of a shape not listed, holds the whole run for pickup. An owner
// card never rides: a line to the chief of staff rides only as a fixed
// fault line.
func bodyLines(rt routed) ([]string, bool) {
	if rt.Reply != "" {
		return nil, false
	}
	lines := []string{}
	for _, x := range rt.Wrote {
		if !bodyWrote.MatchString(x) {
			return nil, false
		}
		lines = append(lines, "wrote: "+x)
	}
	for _, x := range rt.Do {
		if !bodyDo.MatchString("do: " + x) {
			return nil, false
		}
		lines = append(lines, "do: "+x)
	}
	for _, x := range rt.Send {
		switch {
		case x.Bot == "chief-of-staff":
			if !bodyFault.MatchString(x.Line) {
				return nil, false
			}
		case !runs.BotPattern.MatchString(x.Bot) || !bodySend.MatchString(x.Line):
			return nil, false
		}
		lines = append(lines, x.String())
	}
	return lines, true
}

// ringDigest is the ring for bot at now: one body for every settled result
// left to it that it hasn't acked, in pickup's order and with pickup's
// rules (a send a look made lately left out, a usage limit's runs waiting
// as one line while the pause holds, a run with a do: line left out of
// ack, since the command acks it), but for one: while STOP or a pause
// holds, a run with a do: line is left out, so no ring wakes a bot only to
// be refused. It returns the runs the body covers:
// those whose lines ride, those it holds for pickup and those waiting;
// one only counted in More is not covered.
func ringDigest(co coord, dir, bot string, now time.Time) (runs.Notification, []string) {
	list, _ := runs.Unacked(dir, bot, now)
	n := runs.NoticeFor(bot, list)
	n.Runs, n.More = nil, 0
	if len(list) == 0 {
		return n, nil
	}
	pause, paused := co.readPause(now)
	_, held := co.startsHeld(now)
	var listed, ack, waiting []string
	cards, size := false, 0
	for _, r := range list {
		var rt routed
		ok := r.Job != "" && len(r.Routed) > 0 && json.Unmarshal(r.Routed, &rt) == nil
		if ok && paused && rt.Outcome == outUsageLimit && !slices.ContainsFunc(rt.Wrote, func(w string) bool { return strings.HasPrefix(w, "paused every job") }) {
			waiting = append(waiting, r.ID)
			continue
		}
		if ok && held && doWaits(rt) {
			// Nothing to say till it lifts: no line, and not covered,
			// so a ring after the lift, or the orphan sweep's, carries it.
			continue
		}
		if len(n.Runs) == runs.MaxRingRuns || size >= maxBodyLines {
			n.More++
			continue
		}
		var lines []string
		if ok {
			// A store that can't say which sends went holds the run for
			// pickup, which shows why.
			var err error
			if rt.Send, err = co.pickupSends(r.ID, r.StartedAt, rt.Send, now); err == nil {
				lines, ok = bodyLines(rt)
			} else {
				ok = false
			}
		}
		cost := 0
		for _, l := range lines {
			cost += len(l) + 4
		}
		if size+cost > maxBodyLines {
			n.More++
			size = maxBodyLines
			continue
		}
		n.Runs = append(n.Runs, r.ID)
		listed = append(listed, r.ID)
		if !ok {
			// Whole in pickup: not acked here, and counted in More so the
			// bot reads it there.
			cards = true
			n.More++
			continue
		}
		size += cost
		n.Lines = append(n.Lines, lines...)
		if len(rt.Do) == 0 {
			ack = append(ack, r.ID)
		}
	}
	if len(waiting) > 0 {
		n.Lines = append(n.Lines, waitingLine(len(waiting), pause.Until))
		listed = append(listed, waiting...)
	}
	if cards {
		n.Lines = append(n.Lines, cardWaiting)
	}
	if len(ack) > 0 {
		n.Ack = "MAESTRO_TICKET_ACTOR=" + bot + " maestro-ticket ack " + strings.Join(ack, " ")
	}
	n.Text = fmt.Sprintf("Maestro: %d results for %s. Do each line, then run ack.", len(list), bot)
	return n, listed
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

// doWaits reports whether a run waits while starts are held: it has a do:
// line, or its work landed and its end left a job for a worker bot, which
// would only be refused, so neither pickup nor a ring shows its lines, nor
// does ack count it as more. The run that set the pause shows whole, so
// its bot tells the planner.
func doWaits(rt routed) bool {
	starts := len(rt.Do) > 0 || rt.Landed && slices.ContainsFunc(rt.Send, func(sd send) bool { return chainBots[sd.Bot] })
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
