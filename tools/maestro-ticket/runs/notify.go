package runs

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"
)

// BotPattern is what a launcher's name must look like: it becomes a file
// name.
var BotPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ChiefRole is the chief of staff, woken through its T3 thread.
const ChiefRole = "chief-of-staff"

// CodeRoles are the roles code serves: a run's end routes their work
// itself, with no setup.
var CodeRoles = []string{"planner", "junior-engineer", "senior-engineer", "reviewer", "qa-lead", "designer"}

// Hook is how a role hears that a run ended: code routes it, or Wake
// rings it, as the chief's T3 thread is woken. A zero Hook, such as a
// person's, hears nothing and reads pickup.
type Hook struct {
	Code  bool
	Chief bool
	Wake  func(ctx context.Context, n Notification) error
}

// Served reports whether something acts on the role's ended runs.
func (h Hook) Served() bool { return h.Code || h.Chief || h.Wake != nil }

// CanRing reports whether the role is woken from outside the supervisor.
func (h Hook) CanRing() bool { return h.Wake != nil }

// Serving returns bot's Hook. The chief's has no Wake: the caller, which
// knows the board, gives it one.
func Serving(bot string) Hook {
	switch {
	case bot == ChiefRole:
		return Hook{Chief: true}
	case slices.Contains(CodeRoles, bot):
		return Hook{Code: true}
	}
	return Hook{}
}

// Notification is the body of a ring: a digest of the ended runs left to
// one bot that it hasn't acked. The body carries only lines that match
// fixed patterns, which the bot acts on as pickup would print them; text
// Claude wrote stays on this machine until pickup. Run, Ticket and State
// name the newest run, for a URL that routes on them, and Launcher is the
// bot the ring is for, as Bot is.
type Notification struct {
	Text     string   `json:"text"`
	Bot      string   `json:"bot"`
	Launcher string   `json:"launcher"`
	Run      string   `json:"run"`
	Ticket   string   `json:"ticket,omitempty"`
	State    string   `json:"state"`
	Lines    []string `json:"lines,omitempty"`
	Ack      string   `json:"ack,omitempty"`
	Runs     []string `json:"runs,omitempty"`
	More     int      `json:"more,omitempty"`
}

// MaxRingRuns is how many runs one ring names; the rest count in More.
const MaxRingRuns = 10

// NoticeFor is the plain digest of recs, the ended runs left to bot, oldest
// first: their ids and nothing of their results, so the bot reads them
// with pickup. A ring with nothing to say has no runs.
func NoticeFor(bot string, recs []Record) Notification {
	n := Notification{Bot: bot, Launcher: bot}
	if len(recs) == 0 {
		return n
	}
	last := recs[len(recs)-1]
	n.Run, n.Ticket, n.State = last.ID, last.Ticket, last.State
	for i, r := range recs {
		if i == MaxRingRuns {
			n.More = len(recs) - i
			break
		}
		n.Runs = append(n.Runs, r.ID)
	}
	n.Text = fmt.Sprintf("Maestro: %d results for %s. Run maestro-ticket pickup to read them, then ack them.", len(recs), bot)
	return n
}

// Ring wakes h with n.
func Ring(ctx context.Context, h Hook, n Notification) error {
	if h.Wake == nil {
		return errors.New("role has no wake")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return h.Wake(ctx, n)
}

// RingErrCode sorts a ring's error into none, timeout or other, for
// stats: never the error's text.
func RingErrCode(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	return "other"
}
