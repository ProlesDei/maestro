// Package ticket is the storage-independent domain for the maestro-ticket tool:
// ticket types, the status workflow, lease rules, errors and the Store
// interface. Drivers (SQLite today, Postgres later) implement Store; the CLI
// only talks to Store.
package ticket

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ID identifies a ticket. It prints and marshals as "T-<n>".
type ID int64

func (id ID) String() string { return "T-" + strconv.FormatInt(int64(id), 10) }

// MarshalJSON writes the ID as "T-<n>".
func (id ID) MarshalJSON() ([]byte, error) { return json.Marshal(id.String()) }

// UnmarshalJSON accepts "T-<n>", "<n>" or a bare number.
func (id *ID) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		var n int64
		if err2 := json.Unmarshal(b, &n); err2 != nil {
			return err
		}
		*id = ID(n)
		return nil
	}
	v, err := ParseID(s)
	if err != nil {
		return err
	}
	*id = v
	return nil
}

// ParseID accepts "T-12", "t-12" or "12".
func ParseID(s string) (ID, error) {
	t := strings.TrimSpace(s)
	if len(t) > 2 && (t[:2] == "T-" || t[:2] == "t-") {
		t = t[2:]
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%w: bad ticket id %q (want T-<n>)", ErrInvalid, s)
	}
	return ID(n), nil
}

// Status is a ticket's place in the workflow.
type Status string

const (
	StatusTodo           Status = "todo"
	StatusInProgress     Status = "in_progress"
	StatusInReview       Status = "in_review"
	StatusInQA           Status = "in_qa"
	StatusReadyToRelease Status = "ready_to_release"
	StatusDone           Status = "done"
	StatusBlocked        Status = "blocked"
	StatusCancelled      Status = "cancelled"
)

// Statuses lists every status in workflow order.
var Statuses = []Status{
	StatusTodo, StatusInProgress, StatusInReview, StatusInQA,
	StatusReadyToRelease, StatusBlocked, StatusDone, StatusCancelled,
}

// transitions is the workflow. in_progress -> done closes work with nothing
// to review or release (tracking tickets, infrastructure requests); the
// review and QA gates for code are enforced by role playbooks and
// permissions, not here.
var transitions = map[Status][]Status{
	StatusTodo:           {StatusInProgress, StatusBlocked, StatusCancelled},
	StatusInProgress:     {StatusInReview, StatusTodo, StatusBlocked, StatusDone, StatusCancelled},
	StatusInReview:       {StatusInQA, StatusInProgress, StatusBlocked, StatusCancelled},
	StatusInQA:           {StatusReadyToRelease, StatusInProgress, StatusBlocked, StatusCancelled},
	StatusReadyToRelease: {StatusDone, StatusInProgress, StatusBlocked, StatusCancelled},
	StatusBlocked:        {StatusTodo, StatusInProgress, StatusInReview, StatusInQA, StatusReadyToRelease, StatusCancelled},
	StatusDone:           nil,
	StatusCancelled:      nil,
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool { _, ok := transitions[s]; return ok }

// Terminal reports whether no transition leaves s.
func (s Status) Terminal() bool { return s.Valid() && len(transitions[s]) == 0 }

// CanTransition reports whether the workflow allows from -> to.
func CanTransition(from, to Status) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// NextStatuses returns the statuses reachable from s.
func NextStatuses(s Status) []Status { return append([]Status(nil), transitions[s]...) }

// ParseStatus validates a status name.
func ParseStatus(s string) (Status, error) {
	st := Status(strings.TrimSpace(s))
	if !st.Valid() {
		return "", fmt.Errorf("%w: unknown status %q", ErrInvalid, s)
	}
	return st, nil
}

// ParseStatuses parses a comma-separated status list.
func ParseStatuses(s string) ([]Status, error) {
	var out []Status
	for _, part := range strings.Split(s, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		st, err := ParseStatus(part)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// Priority runs 0 (most urgent) to 3.
const (
	MinPriority     = 0
	MaxPriority     = 3
	DefaultPriority = 2
)

// Lease limits.
const (
	DefaultTTL = 30 * time.Minute
	MinTTL     = 5 * time.Second
	MaxTTL     = 24 * time.Hour
)

var (
	namePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	holderPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._:-]*$`)
)

// ValidateRole checks a role name (lowercase, hyphens), empty allowed.
func ValidateRole(role string) error {
	if role != "" && !namePattern.MatchString(role) {
		return fmt.Errorf("%w: role %q must be lowercase letters, digits and hyphens", ErrInvalid, role)
	}
	return nil
}

// ValidateHolder checks a lease holder of the form <bot-id>/<session-id>.
func ValidateHolder(holder string) error {
	if !holderPattern.MatchString(holder) || len(holder) > 200 {
		return fmt.Errorf("%w: holder %q must be <bot-id>/<session-id>", ErrInvalid, holder)
	}
	return nil
}

// HolderBot returns the bot-id part of a holder.
func HolderBot(holder string) string {
	bot, _, _ := strings.Cut(holder, "/")
	return bot
}

// ValidateTTL checks a lease TTL and applies the default for zero.
func ValidateTTL(ttl time.Duration) (time.Duration, error) {
	if ttl == 0 {
		return DefaultTTL, nil
	}
	if ttl < MinTTL || ttl > MaxTTL {
		return 0, fmt.Errorf("%w: ttl %s out of range [%s, %s]", ErrInvalid, ttl, MinTTL, MaxTTL)
	}
	return ttl, nil
}

// Claim is a session-scoped lease on a ticket.
type Claim struct {
	Holder    string    `json:"holder"`
	ExpiresAt time.Time `json:"expires_at"`
	// Expired is computed at read time. An expired claim no longer blocks
	// anyone; the next claim or release clears it.
	Expired bool `json:"expired"`
}

// Ticket is one unit of work.
type Ticket struct {
	ID        ID        `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Status    Status    `json:"status"`
	Role      string    `json:"role"`
	Priority  int       `json:"priority"`
	CreatedBy string    `json:"created_by"`
	Version   int64     `json:"version"`
	Claim     *Claim    `json:"claim"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LiveClaim returns the claim if it has not expired.
func (t Ticket) LiveClaim() *Claim {
	if t.Claim == nil || t.Claim.Expired {
		return nil
	}
	return t.Claim
}

// Comment is a note on a ticket (handoff notes, review findings, reports).
type Comment struct {
	ID        int64     `json:"id"`
	TicketID  ID        `json:"ticket_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// Detail is a ticket with its comments.
type Detail struct {
	Ticket
	Comments []Comment `json:"comments"`
}

// Event types written to the append-only event log.
const (
	EventCreated      = "created"
	EventClaimed      = "claimed"
	EventLeaseExpired = "lease_expired"
	EventReleased     = "released"
	EventTransitioned = "transitioned"
	EventCommented    = "commented"
)

// Event is one entry in the append-only log. Seq is strictly increasing and
// never reused, so a reader can keep a cursor and ask for everything after it.
type Event struct {
	Seq       int64           `json:"seq"`
	TicketID  ID              `json:"ticket_id"`
	Type      string          `json:"type"`
	Actor     string          `json:"actor"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

// CreateRequest creates a ticket. Status may be todo (default) or blocked.
type CreateRequest struct {
	Title          string `json:"title"`
	Body           string `json:"body"`
	Role           string `json:"role"`
	Priority       *int   `json:"priority,omitempty"`
	Status         Status `json:"status"`
	Actor          string `json:"actor"`
	IdempotencyKey string `json:"-"`
}

// Validate checks the request and fills defaults.
func (r *CreateRequest) Validate() error {
	r.Title = strings.TrimSpace(r.Title)
	if r.Title == "" {
		return fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if strings.ContainsAny(r.Title, "\r\n") {
		return fmt.Errorf("%w: title must be one line", ErrInvalid)
	}
	if err := ValidateRole(r.Role); err != nil {
		return err
	}
	if r.Priority == nil {
		p := DefaultPriority
		r.Priority = &p
	}
	if *r.Priority < MinPriority || *r.Priority > MaxPriority {
		return fmt.Errorf("%w: priority %d out of range [%d, %d]", ErrInvalid, *r.Priority, MinPriority, MaxPriority)
	}
	if r.Status == "" {
		r.Status = StatusTodo
	}
	if r.Status != StatusTodo && r.Status != StatusBlocked {
		return fmt.Errorf("%w: new tickets start as todo or blocked, not %q", ErrInvalid, r.Status)
	}
	r.Actor = actorFor(r.Actor, "")
	return nil
}

// ListFilter selects tickets. Zero values mean "any".
type ListFilter struct {
	Statuses []Status
	Role     string
	Holder   string // tickets with a live claim by this holder
	Claimed  *bool  // true: live claim; false: no live claim
	Limit    int
}

// ClaimRequest leases the next matching ticket, or a specific one when ID is
// set. For --next claims Statuses defaults to [todo].
type ClaimRequest struct {
	ID             ID            `json:"id,omitempty"`
	Statuses       []Status      `json:"statuses,omitempty"`
	Role           string        `json:"role,omitempty"`
	Holder         string        `json:"holder"`
	TTL            time.Duration `json:"ttl"`
	Actor          string        `json:"actor"`
	IdempotencyKey string        `json:"-"`
}

// Validate checks the request and fills defaults.
func (r *ClaimRequest) Validate() error {
	if err := ValidateHolder(r.Holder); err != nil {
		return err
	}
	if err := ValidateRole(r.Role); err != nil {
		return err
	}
	ttl, err := ValidateTTL(r.TTL)
	if err != nil {
		return err
	}
	r.TTL = ttl
	if r.ID == 0 && len(r.Statuses) == 0 {
		r.Statuses = []Status{StatusTodo}
	}
	for _, s := range r.Statuses {
		if s.Terminal() {
			return fmt.Errorf("%w: cannot claim %s tickets", ErrInvalid, s)
		}
	}
	r.Actor = actorFor(r.Actor, r.Holder)
	return nil
}

// HeartbeatRequest extends a live lease held by Holder.
type HeartbeatRequest struct {
	ID     ID
	Holder string
	TTL    time.Duration
}

// Validate checks the request and fills defaults.
func (r *HeartbeatRequest) Validate() error {
	if err := ValidateHolder(r.Holder); err != nil {
		return err
	}
	ttl, err := ValidateTTL(r.TTL)
	r.TTL = ttl
	return err
}

// ReleaseRequest drops Holder's lease. Releasing a ticket with no live lease
// is a no-op, so cleanup can run twice.
type ReleaseRequest struct {
	ID             ID     `json:"id"`
	Holder         string `json:"holder"`
	Actor          string `json:"actor"`
	IdempotencyKey string `json:"-"`
}

// Validate checks the request and fills defaults.
func (r *ReleaseRequest) Validate() error {
	if err := ValidateHolder(r.Holder); err != nil {
		return err
	}
	r.Actor = actorFor(r.Actor, r.Holder)
	return nil
}

// TransitionRequest moves a ticket to a new status. From and ExpectVersion
// are optional optimistic checks. If the ticket has a live claim, Holder must
// match it.
type TransitionRequest struct {
	ID             ID     `json:"id"`
	From           Status `json:"from,omitempty"`
	To             Status `json:"to"`
	ExpectVersion  int64  `json:"expect_version,omitempty"`
	Holder         string `json:"holder,omitempty"`
	Role           string `json:"role,omitempty"` // reassign, e.g. on escalation
	Note           string `json:"note,omitempty"`
	Release        bool   `json:"release,omitempty"`
	Actor          string `json:"actor"`
	IdempotencyKey string `json:"-"`
}

// Validate checks the request and fills defaults.
func (r *TransitionRequest) Validate() error {
	if !r.To.Valid() {
		return fmt.Errorf("%w: unknown status %q", ErrInvalid, r.To)
	}
	if r.From != "" && !r.From.Valid() {
		return fmt.Errorf("%w: unknown status %q", ErrInvalid, r.From)
	}
	if r.Holder != "" {
		if err := ValidateHolder(r.Holder); err != nil {
			return err
		}
	}
	if r.ExpectVersion < 0 {
		return fmt.Errorf("%w: expect-version must be positive", ErrInvalid)
	}
	if err := ValidateRole(r.Role); err != nil {
		return err
	}
	r.Actor = actorFor(r.Actor, r.Holder)
	return nil
}

// CommentRequest adds a comment.
type CommentRequest struct {
	ID             ID     `json:"id"`
	Body           string `json:"body"`
	Actor          string `json:"actor"`
	IdempotencyKey string `json:"-"`
}

// Validate checks the request and fills defaults.
func (r *CommentRequest) Validate() error {
	if strings.TrimSpace(r.Body) == "" {
		return fmt.Errorf("%w: comment body is required", ErrInvalid)
	}
	r.Actor = actorFor(r.Actor, "")
	return nil
}

// EventFilter selects events with Seq > Since, in order.
type EventFilter struct {
	Since    int64
	TicketID ID
	Type     string
	Limit    int
}

// Check is one doctor result.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// DoctorReport is the result of Store.Doctor.
type DoctorReport struct {
	Checks []Check `json:"checks"`
}

// OK reports whether every check passed.
func (r DoctorReport) OK() bool {
	for _, c := range r.Checks {
		if !c.OK {
			return false
		}
	}
	return true
}

// Store is the ticket storage contract. Every write is atomic: it either
// applies fully, with its event appended, or not at all. Events must become
// visible in seq order, so a reader's cursor never skips an event. Writes that carry an
// idempotency key replay the stored result when retried with the same request
// and fail with ErrIdempotencyConflict when the key is reused for a different
// request.
type Store interface {
	Create(ctx context.Context, req CreateRequest) (Ticket, error)
	Get(ctx context.Context, id ID) (Detail, error)
	List(ctx context.Context, f ListFilter) ([]Ticket, error)
	// ClaimNext leases the most urgent claimable ticket (priority, then
	// oldest id). A ticket is claimable when its lease is missing or expired.
	// Returns ErrNoWork when nothing matches.
	ClaimNext(ctx context.Context, req ClaimRequest) (Ticket, error)
	Heartbeat(ctx context.Context, req HeartbeatRequest) (Ticket, error)
	Release(ctx context.Context, req ReleaseRequest) (Ticket, error)
	Transition(ctx context.Context, req TransitionRequest) (Ticket, error)
	Comment(ctx context.Context, req CommentRequest) (Comment, error)
	Events(ctx context.Context, f EventFilter) ([]Event, error)
	// LastSeq returns the newest event seq, or 0 for an empty log.
	LastSeq(ctx context.Context) (int64, error)
	Doctor(ctx context.Context) (DoctorReport, error)
	Close() error
}

func actorFor(actor, holder string) string {
	if a := strings.TrimSpace(actor); a != "" {
		return a
	}
	if holder != "" {
		return HolderBot(holder)
	}
	return "unknown"
}
