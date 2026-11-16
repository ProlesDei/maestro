package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// A chain is the fixed next step a run's end starts itself: a build in
// review goes to review, a GO to QA, a send-back to its engineer, a
// Rereview: to review. Each of those was a line a bot passed to another
// bot, which woke, reread its chat and ran job: Grok spent a turn on each
// and decided nothing (owner's ruling, Nov 15 2026). It picks no new work,
// and whatever it can't start stays for the launcher's pickup, as before.
var (
	chainJobs = map[string]bool{"build": true, "review": true, "qa": true, "live": true}
	chainBots = map[string]bool{"junior-engineer": true, "senior-engineer": true, "reviewer": true, "qa-lead": true}
	chainLine = regexp.MustCompile(`^(build|review|qa) (T-[0-9]+)$`)
)

// maxHops is how many ends in a row may each start the next step: past
// it, a bot looks. A ticket's tries end a send-back loop well before.
const maxHops = 10

// chainLimit bounds a chain, so a start that hangs never holds the ring.
const chainLimit = 2 * time.Minute

// chainBy is who acks a run whose end left nothing for a bot to do.
const chainBy = "chain"

// chain runs in a job run's supervisor once the run ended, before the
// first ring. It routes a run that ended done as pickup would and starts
// each fixed handoff itself, as the bot it was for. What is left only tells the
// planner something goes to the planner; a run that leaves nothing to do
// is acked, and rings no one.
func (c *call) chain(ctx context.Context, dir string, r runs.Record) {
	// A failure, a limit or a full disk stays the launcher's: routing one
	// here would spend what pickup shows once, such as a usage limit's
	// line to the planner.
	if out, _ := outcomeOf(r); !chainJobs[r.Job] || out != outDone {
		return
	}
	s, err := c.open(ctx)
	if err != nil {
		return
	}
	rt, err := c.route(ctx, s, r)
	s.Close()
	if err != nil {
		return
	}
	notify := filepath.Join(filepath.Dir(dir), "notify")
	now := time.Now().UTC().Round(time.Millisecond)
	// As pickup would: a job a look handed out lately is on its way.
	sends := pickupSends(filepath.Join(filepath.Dir(dir), "handouts"), r.ID, r.StartedAt, rt.Send, now)
	var chained []string
	left := []send{}
	for _, sd := range sends {
		if c.handOn(ctx, dir, notify, r, sd) {
			chained = append(chained, sd.Bot+": "+sd.Line)
		} else {
			left = append(left, sd)
		}
	}
	rt.Send = left
	rest := leftForBot(rt)
	done := len(left) == 0 && !rest
	toPlanner := len(left) > 0 && !rest && r.Launcher != "planner" &&
		!slices.ContainsFunc(left, func(sd send) bool { return sd.Bot != "planner" }) && hasHook(notify, "planner")
	if len(chained) == 0 && !done && !toPlanner {
		return // as before: the launcher's pickup routes it
	}
	rec, err := runs.Load(dir, r.ID)
	if err != nil {
		return
	}
	rec.Chained = chained
	if rec.Routed, err = json.Marshal(rt); err != nil {
		return
	}
	if toPlanner {
		rec.Notify = "planner"
	}
	if runs.Save(dir, rec) == nil && done {
		runs.Ack(dir, r.ID, chainBy, now)
	}
}

// leftForBot reports whether a route holds more than sends for a bot to
// act on: a command to run, a reply to read, or a write that failed, such
// as follow-ups not filed, which would be lost in a run acked unseen.
func leftForBot(rt routed) bool {
	return len(rt.Do) > 0 || rt.Reply != "" || slices.ContainsFunc(rt.Wrote, func(w string) bool {
		return strings.HasPrefix(w, "follow-ups not filed") || strings.HasPrefix(w, "not parked")
	})
}

// handOn starts one send's job as the bot it was for, and reports whether
// that job now runs: started, or already going. It starts none for a bot
// with no webhook, which would never hear how its run ended.
func (c *call) handOn(ctx context.Context, dir, notify string, r runs.Record, sd send) bool {
	k, id, ok := nextStep(r, sd)
	if !ok || !hasHook(notify, sd.Bot) {
		return false
	}
	cc := *c
	cc.actor, cc.runCache = sd.Bot, nil
	req := jobReq{kind: k, ids: []string{id}, chainOf: r.ID, hops: r.Hops + 1}
	if k.slot != "" {
		req.slots = lastSlots(dir, sd.Bot, k.slot)
	}
	_, _, _, err := cc.startJob(ctx, req)
	var a alreadyGoing
	return err == nil || errors.As(err, &a)
}

// nextStep is the job and ticket of a send that is a fixed handoff to a
// worker bot, which r's end may start itself.
func nextStep(r runs.Record, sd send) (jobKind, string, bool) {
	m := chainLine.FindStringSubmatch(sd.Line)
	if m == nil || !chainBots[sd.Bot] || r.Hops >= maxHops {
		return jobKind{}, "", false
	}
	k, ok := findJob(m[1])
	return k, m[2], ok
}

// hasHook reports whether bot has a webhook to ring.
func hasHook(notify, bot string) bool {
	h, err := runs.Webhook(notify, bot)
	return err == nil && h.URL != ""
}

// lastSlots is the slot count bot passed on its newest run in slot name,
// as its memories set it; 0, the default, if none of its runs did.
func lastSlots(dir, bot, name string) int {
	list, _ := runs.List(dir, time.Now(), true)
	var at time.Time
	n := 0
	for _, r := range list {
		if r.Launcher != bot || !r.StartedAt.After(at) {
			continue
		}
		if i := slices.Index(r.Command, "slot"); i >= 0 && flagValue(r.Command[i:], "--name") == name {
			if v, err := strconv.Atoi(flagValue(r.Command[i:], "--max")); err == nil && v > 0 {
				n, at = v, r.StartedAt
			}
		}
	}
	return n
}
