package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/render"
)

// GateWindow is how often, at most, a role rings while a run its last
// ring listed waits for an ack: one ring carries all its results. A var only so
// tests can shorten it.
var GateWindow = 4 * time.Minute

// gate is a bot's last ring: when, the runs it covered, and whether the
// wake took it. It lives in <bot>.gate in the notify folder, and
// <bot>.gate.lock lets one ringer at a time read and write it.
type gate struct {
	At     time.Time `json:"at"`
	Listed []string  `json:"listed"`
	Failed bool      `json:"failed,omitempty"`
}

// DigestFunc builds a bot's ring at now: its body, and the runs it covers,
// whose lines ride in it or which it holds whole for pickup. A run it
// only counts in More is not covered.
type DigestFunc func(now time.Time) (Notification, []string)

// Gated is what one RingBot did.
type Gated struct {
	Tried bool // it rang
	Rang  bool // and the wake took it
	// Covered are the ids asked about that are acked, or that a ring the
	// wake took lists: this one's, or one under GateWindow old.
	Covered []string
	// At is when the ring that covered them rang.
	At time.Time
	// Next, for a ring the gate held back, is when the gate opens.
	Next time.Time
}

// Covers reports whether id needs no ring of its own.
func (g Gated) Covers(id string) bool { return slices.Contains(g.Covered, id) }

// Held reports whether the gate held the ring back.
func (g Gated) Held() bool { return !g.Next.IsZero() }

// RingBot rings bot once with build's digest of every result
// left to it, unless a ring under GateWindow old listed a run that is
// still unacked: then it rings nothing, and says which of ids that ring
// covered and when the gate opens. Of any number of callers at once, on
// any number of runs, one rings and the rest read its gate.
func RingBot(ctx context.Context, dir, gateDir, bot string, ids []string, h Hook, now time.Time, build DigestFunc) (Gated, error) {
	var g Gated
	open := []string{}
	for _, id := range ids {
		if Acked(dir, id) {
			g.Covered = append(g.Covered, id)
		} else {
			open = append(open, id)
		}
	}
	if len(ids) > 0 && len(open) == 0 {
		return g, nil
	}
	if !BotPattern.MatchString(bot) {
		return g, fmt.Errorf("bot id %q: use lowercase letters, digits and single hyphens", bot)
	}
	if err := os.MkdirAll(gateDir, 0o700); err != nil {
		return g, err
	}
	f, err := os.OpenFile(filepath.Join(gateDir, bot+".gate.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return g, err
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		return g, err
	}
	name := filepath.Join(gateDir, bot+".gate")
	var last gate
	if b, err := os.ReadFile(name); err == nil {
		json.Unmarshal(b, &last)
	}
	if now.Sub(last.At) < GateWindow && slices.ContainsFunc(last.Listed, func(id string) bool { return !Acked(dir, id) }) {
		if !last.Failed {
			for _, id := range open {
				if slices.Contains(last.Listed, id) {
					g.Covered = append(g.Covered, id)
				}
			}
		}
		g.At, g.Next = last.At, last.At.Add(GateWindow)
		return g, nil
	}
	n, listed := build(now)
	if len(n.Runs) == 0 && len(n.Lines) == 0 && n.More == 0 {
		return g, nil // nothing left to say
	}
	rerr := Ring(ctx, h, n)
	if ctx.Err() != nil {
		return g, rerr // a ring cut short isn't one: the next ringer rings
	}
	g.Tried, g.Rang, g.At = true, rerr == nil, now
	b, _ := json.Marshal(gate{At: now, Listed: listed, Failed: rerr != nil})
	if err := render.WriteAtomic(name, append(b, '\n')); err != nil && rerr == nil {
		rerr = err
	}
	if g.Rang {
		for _, id := range open {
			if slices.Contains(listed, id) {
				g.Covered = append(g.Covered, id)
			}
		}
	}
	return g, rerr
}
