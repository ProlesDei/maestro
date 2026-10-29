// Package usage holds harness usage records, the parsers that read them from
// harness output, and the budget and breaker rules that judge them. Nothing
// here spends tokens: every check is arithmetic over a local store.
package usage

import (
	"context"
	"errors"
	"time"
)

// Status of one harness invocation.
const (
	StatusSuccess  = "success"
	StatusError    = "error"
	StatusUnparsed = "unparsed" // no usage found in the output
)

// Where a record's cost came from.
const (
	CostHarness = "harness-estimate"
	CostPrices  = "price-table"
	CostManual  = "manual"
)

// Usage is what one harness invocation reported.
type Usage struct {
	SessionID    string
	Model        string
	InputTokens  int64 // uncached input, including cache writes
	CachedTokens int64 // cache reads
	OutputTokens int64
	CostUSD      *float64 // nil when unknown, never a fake zero
	CostSource   string
	Status       string
	DurationMS   *int64
}

// Empty is the usage of output that held nothing parseable.
func Empty() Usage { return Usage{Status: StatusUnparsed} }

// Tokens is the total token count.
func (u Usage) Tokens() int64 { return u.InputTokens + u.CachedTokens + u.OutputTokens }

// Context names who spent the tokens.
type Context struct {
	Ticket, Workstream, Role, Bot string
}

// Record is one row: one harness invocation, never one session, because a
// resumed session reports each step under the same session ID.
type Record struct {
	Key        string // unique; saving an existing key replaces its row
	RecordedAt time.Time
	Harness    string
	Context
	Usage
}

// Field is a column usage can be filtered or grouped by.
type Field string

const (
	FieldTicket     Field = "ticket"
	FieldWorkstream Field = "workstream"
	FieldRole       Field = "role"
	FieldModel      Field = "model"
	FieldHarness    Field = "harness"
)

// Valid reports whether f is a known field.
func (f Field) Valid() bool {
	switch f {
	case FieldTicket, FieldWorkstream, FieldRole, FieldModel, FieldHarness:
		return true
	}
	return false
}

// Filter selects records. Zero values select everything.
type Filter struct {
	Field Field // "" for all records
	Value string
	Since time.Time // zero for all time
}

// Totals is spending over a set of records.
type Totals struct {
	USD    float64
	Tokens int64
}

// ReportRow is one group in a report.
type ReportRow struct {
	Group    string
	Sessions int64
	USD      float64
	Input    int64
	Cached   int64
	Output   int64
	Failures int64 // status other than success
	Unpriced int64 // rows with no cost
}

// ErrInvalid marks bad input: flags, config or filters.
var ErrInvalid = errors.New("invalid")

// Store keeps usage records. Implementations must be safe for many
// processes writing at once.
type Store interface {
	Save(ctx context.Context, r Record) error
	Spent(ctx context.Context, f Filter) (Totals, error)
	// RecentStatuses returns the statuses of a role's newest n records,
	// newest first.
	RecentStatuses(ctx context.Context, role string, n int) ([]string, error)
	// Report groups records since a time (zero for all), most spent first.
	Report(ctx context.Context, by Field, since time.Time) ([]ReportRow, error)
	Close() error
}
