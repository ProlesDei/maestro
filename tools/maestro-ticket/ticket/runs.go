package ticket

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Run is one Claude Code session's spend, as the newest reply logged for
// it reports. A resumed session reports its running total, so each
// session is one Run, however often it is logged.
type Run struct {
	Session string `json:"session"`
	// Ticket is the ticket the session worked on; 0 for none.
	Ticket           ID        `json:"ticket,omitempty"`
	Actor            string    `json:"actor"`
	CostUSD          float64   `json:"cost_usd"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// LogRunRequest records a session's spend so far.
type LogRunRequest struct {
	Session          string
	Ticket           ID
	Actor            string
	CostUSD          float64
	InputTokens      int64
	OutputTokens     int64
	CacheWriteTokens int64
	CacheReadTokens  int64
}

// Validate checks the request.
func (r LogRunRequest) Validate() error {
	switch {
	case strings.TrimSpace(r.Session) == "" || len(r.Session) > 200 || HasControl(r.Session):
		return fmt.Errorf("%w: a session id is one line of up to 200 characters", ErrInvalid)
	case strings.TrimSpace(r.Actor) == "":
		return fmt.Errorf("%w: an actor is required", ErrInvalid)
	case r.Ticket < 0:
		return fmt.Errorf("%w: bad ticket id %d", ErrInvalid, r.Ticket)
	case r.CostUSD < 0 || math.IsNaN(r.CostUSD) || math.IsInf(r.CostUSD, 0):
		return fmt.Errorf("%w: cost must be a number of dollars, 0 or more", ErrInvalid)
	case r.InputTokens < 0 || r.OutputTokens < 0 || r.CacheWriteTokens < 0 || r.CacheReadTokens < 0:
		return fmt.Errorf("%w: token counts can't be negative", ErrInvalid)
	}
	return nil
}

// RunFilter selects runs. Zero values select all.
type RunFilter struct {
	Ticket ID
	// Since keeps sessions first logged at or after it.
	Since time.Time
}

// RunRow is a run with what its ticket says about where the work came
// from, for grouping. All three are empty for a run with no ticket.
type RunRow struct {
	Run
	Goal string `json:"goal,omitempty"`
	// Origin is who created the ticket, or "split" for a ticket made with
	// --split-from: a rewrite or split of another.
	Origin string `json:"origin,omitempty"`
	Tries  int    `json:"tries"`
}
