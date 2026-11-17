package ticket

import (
	"fmt"
	"strings"
	"time"
)

// DefaultStall is how long a handoff may wait in in_review with no one on
// it before alerts and doctor call it a stall.
const DefaultStall = 20 * time.Minute

// Launch is one run piped into log-run, recorded when log-run starts, so a
// run that never writes a result still shows: the box restarted, or the
// pipe was killed. Outcome is empty while the run goes.
type Launch struct {
	ID    int64  `json:"id"`
	Actor string `json:"actor"`
	// Ticket is the ticket the run works on; 0 for none.
	Ticket ID  `json:"ticket,omitempty"`
	PID    int `json:"pid"`
	// Stamp tells this process from a later one with the same pid, as
	// boot|pid namespace|start time (on macOS, boot|darwin|start time).
	// Empty where the OS gives none.
	Stamp     string    `json:"stamp,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// EndedAt is zero while the run goes.
	EndedAt time.Time `json:"ended_at,omitzero"`
	Outcome string    `json:"outcome,omitempty"`
	Session string    `json:"session,omitempty"`
	// Host, Job, Tickets and Limit say whose run it is on a store that
	// machines share, so another machine leaves it to its own. A store on
	// one machine keeps none of them: an empty Host is this machine.
	Host    string `json:"host,omitempty"`
	Job     string `json:"job,omitempty"`
	Tickets []ID   `json:"tickets,omitempty"` // a judge run's
	Limit   string `json:"limit,omitempty"`   // as a run record writes it, such as 45m
}

// LimitOr is the launch's time limit, or d when it names none.
func (l Launch) LimitOr(d time.Duration) time.Duration {
	if v, err := time.ParseDuration(l.Limit); err == nil && v > 0 {
		return v
	}
	return d
}

// Launch outcomes.
const (
	// LaunchResult: the run gave a reply log-run could read.
	LaunchResult = "result"
	// LaunchNoResult: the run ended with no reply, or one log-run couldn't
	// read: it hung and its time limit ended it, or it died.
	LaunchNoResult = "no_result"
	// LaunchDied: log-run itself died before the run ended, so no one saw
	// it end: the box restarted, or the pipe was killed.
	LaunchDied = "died"
)

// StartLaunchRequest records a run as it starts.
type StartLaunchRequest struct {
	Actor  string
	Ticket ID
	PID    int
	Stamp  string
	// As Launch's; a store on one machine ignores them.
	Host    string
	Job     string
	Tickets []ID
	Limit   string
}

// Validate checks the request.
func (r StartLaunchRequest) Validate() error {
	switch {
	case strings.TrimSpace(r.Actor) == "" || HasControl(r.Actor):
		return fmt.Errorf("%w: an actor is one line", ErrInvalid)
	case r.Ticket < 0:
		return fmt.Errorf("%w: bad ticket id %d", ErrInvalid, r.Ticket)
	case r.PID <= 0:
		return fmt.Errorf("%w: a launch needs its process id", ErrInvalid)
	case len(r.Stamp) > 200 || HasControl(r.Stamp):
		return fmt.Errorf("%w: bad process stamp", ErrInvalid)
	case len(r.Host) > 64 || HasControl(r.Host) || strings.ContainsAny(r.Host, " \t"):
		return fmt.Errorf("%w: bad host id", ErrInvalid)
	case len(r.Job) > 40 || HasControl(r.Job):
		return fmt.Errorf("%w: bad job name", ErrInvalid)
	case len(r.Tickets) > 100:
		return fmt.Errorf("%w: a launch names at most 100 tickets", ErrInvalid)
	}
	for _, id := range r.Tickets {
		if id <= 0 {
			return fmt.Errorf("%w: bad ticket id %d", ErrInvalid, id)
		}
	}
	if r.Limit != "" {
		if d, err := time.ParseDuration(r.Limit); err != nil || d <= 0 {
			return fmt.Errorf("%w: bad run limit %q", ErrInvalid, r.Limit)
		}
	}
	return nil
}

// EndLaunchRequest records how a run ended. Ending a run that already
// ended changes nothing, so two looks that both find a dead run count it
// once.
type EndLaunchRequest struct {
	ID      int64
	Outcome string
	Session string
}

// Validate checks the request.
func (r EndLaunchRequest) Validate() error {
	switch r.Outcome {
	case LaunchResult, LaunchNoResult, LaunchDied:
	default:
		return fmt.Errorf("%w: unknown launch outcome %q", ErrInvalid, r.Outcome)
	}
	if r.ID <= 0 {
		return fmt.Errorf("%w: bad launch id %d", ErrInvalid, r.ID)
	}
	if len(r.Session) > 200 || HasControl(r.Session) {
		return fmt.Errorf("%w: a session id is one line of up to 200 characters", ErrInvalid)
	}
	return nil
}

// ETA is a time the team told the owner a goal would be ready by. It is a
// board fact, so the first look after it can check it was met.
type ETA struct {
	Goal string    `json:"goal"`
	Due  time.Time `json:"due"`
	// Tickets, when set, are what the ETA promised; else the whole goal.
	Tickets []ID      `json:"tickets"`
	SetBy   string    `json:"set_by"`
	SetAt   time.Time `json:"set_at"`
	// MetAt is when a look first found it met; it then stays met.
	MetAt time.Time `json:"met_at,omitzero"`
}

// SetETARequest sets or replaces a goal's ETA.
type SetETARequest struct {
	Goal    string
	Due     time.Time
	Tickets []ID
	Actor   string
}

// Validate checks the request.
func (r *SetETARequest) Validate() error {
	r.Goal = strings.TrimSpace(r.Goal)
	if r.Goal == "" {
		return fmt.Errorf("%w: an ETA needs --goal", ErrInvalid)
	}
	if err := ValidateGoal(r.Goal); err != nil {
		return err
	}
	if r.Due.IsZero() {
		return fmt.Errorf("%w: an ETA needs --by", ErrInvalid)
	}
	if err := validateIDs(r.Tickets); err != nil {
		return err
	}
	r.Actor = actorFor(r.Actor, "")
	return nil
}
