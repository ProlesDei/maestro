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
	"sort"
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
		if n <= 0 {
			return fmt.Errorf("%w: bad ticket id %d (want T-<n>)", ErrInvalid, n)
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
	StatusBacklog    Status = "backlog"
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusInReview   Status = "in_review"
	StatusDone       Status = "done"
	StatusBlocked    Status = "blocked"
	StatusCancelled  Status = "cancelled"
)

// Statuses lists every status in workflow order.
var Statuses = []Status{
	StatusBacklog, StatusTodo, StatusInProgress, StatusInReview,
	StatusBlocked, StatusDone, StatusCancelled,
}

// workflows lists the moves each ticket type allows. Ship tickets reach done
// only from in_review, where the reviewer sets them done; scout tickets end
// at in_review, where the owner reads the report. Blocked is not listed as a source: a blocked
// ticket returns only to the status it was blocked from, or is cancelled
// (see Ticket.NextStatuses). Done and cancelled are terminal.
var workflows = map[Type]map[Status][]Status{
	TypeShip: {
		StatusBacklog:    {StatusTodo, StatusBlocked, StatusCancelled},
		StatusTodo:       {StatusInProgress, StatusBacklog, StatusBlocked, StatusCancelled},
		StatusInProgress: {StatusInReview, StatusTodo, StatusBlocked, StatusCancelled},
		StatusInReview:   {StatusDone, StatusInProgress, StatusBlocked, StatusCancelled},
	},
	TypeScout: {
		StatusBacklog:    {StatusTodo, StatusBlocked, StatusCancelled},
		StatusTodo:       {StatusInProgress, StatusBacklog, StatusBlocked, StatusCancelled},
		StatusInProgress: {StatusInReview, StatusTodo, StatusBlocked, StatusCancelled},
		StatusInReview:   {StatusDone, StatusInProgress, StatusBlocked, StatusCancelled},
	},
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	for _, x := range Statuses {
		if s == x {
			return true
		}
	}
	return false
}

// Terminal reports whether no transition leaves s.
func (s Status) Terminal() bool { return s == StatusDone || s == StatusCancelled }

// WritesCode reports whether work in status s changes code: only then does
// a claim wait on dependencies and areas, and hold its areas. Claims for
// review or grooming don't.
func (s Status) WritesCode() bool { return s == StatusTodo || s == StatusInProgress }

// IsSendBack reports whether from -> to sends work back from review, which
// adds one to a ship ticket's tries. An owner sending a scout report back
// is not a try: there is no code to fix.
func IsSendBack(from, to Status) bool {
	return to == StatusInProgress && from == StatusInReview
}

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

// Type is a ticket's shape. Ship tickets deliver changes and go through
// review. Scout tickets end at an evidenced report.
type Type string

const (
	TypeShip  Type = "ship"
	TypeScout Type = "scout"
)

// Valid reports whether t is a known type.
func (t Type) Valid() bool { return t == TypeShip || t == TypeScout }

// ParseType validates a type name.
func ParseType(s string) (Type, error) {
	t := Type(strings.TrimSpace(s))
	if !t.Valid() {
		return "", fmt.Errorf("%w: unknown ticket type %q (want ship or scout)", ErrInvalid, s)
	}
	return t, nil
}

// SystemActor is the actor on events the store writes by itself, such as
// lease_expired.
const SystemActor = "maestro-ticket"

// Roles. Only the reviewer sets ship tickets done, after it merges the PR, or
// the planner when the PR merged outside a review.
// The QA lead records results as a comment starting "QA: pass" or
// "QA: fail", not as a status.
const (
	RoleChiefOfStaff = "chief-of-staff"
	RolePlanner      = "planner"
	RoleEngineer     = "engineer"
	RoleQALead       = "qa-lead"
	RoleReviewer     = "reviewer"
)

// Roles lists every role a ticket or an actor may have.
var Roles = []string{RoleChiefOfStaff, RolePlanner, RoleEngineer, RoleQALead, RoleReviewer}

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
	holderPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._:-]*$`)
)

// ValidRole reports whether role is one of Roles.
func ValidRole(role string) bool {
	for _, r := range Roles {
		if role == r {
			return true
		}
	}
	return false
}

// ValidateRole checks a role name against Roles, empty allowed.
func ValidateRole(role string) error {
	if role != "" && !ValidRole(role) {
		return fmt.Errorf("%w: unknown role %q (want %s)", ErrInvalid, role, strings.Join(Roles, ", "))
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
	Holder string `json:"holder"`
	// Token is the lease number: it grows on every claim of the ticket,
	// including a holder re-claiming its own lease. Writes to a claimed
	// ticket must carry it, so a session that lost its lease is fenced out.
	Token     int64     `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	// Expired is computed at read time. An expired claim no longer blocks
	// anyone. The reaper, or the next write to the ticket, clears it and
	// logs lease_expired.
	Expired bool `json:"expired"`
}

// Ticket is one unit of work.
type Ticket struct {
	ID        ID     `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Type      Type   `json:"type"`
	Status    Status `json:"status"`
	Role      string `json:"role"`
	Priority  int    `json:"priority"`
	CreatedBy string `json:"created_by"`
	// Owner asked for the work. On scout tickets the owner sets done.
	Owner string `json:"owner"`
	// Tries counts send-backs from review.
	Tries int `json:"tries"`
	// LeaseExpiries counts the leases on this ticket that expired instead of
	// being released: sessions that died or stalled while holding it.
	LeaseExpiries int `json:"lease_expiries"`
	// DependsOn lists tickets that must be done before this one is handed
	// out.
	DependsOn []ID `json:"depends_on"`
	// Areas are the code areas the ticket touches. Two tickets whose areas
	// overlap are never claimed at the same time.
	Areas []string `json:"areas"`
	// BlockedFrom is the status a blocked ticket returns to.
	BlockedFrom Status    `json:"blocked_from,omitempty"`
	Version     int64     `json:"version"`
	Claim       *Claim    `json:"claim"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// NextStatuses returns the statuses t may move to.
func (t Ticket) NextStatuses() []Status {
	if t.Status == StatusBlocked {
		back := t.BlockedFrom
		if !back.Valid() || back == StatusBlocked || back.Terminal() {
			back = StatusTodo
		}
		return []Status{back, StatusCancelled}
	}
	return append([]Status(nil), workflows[t.Type][t.Status]...)
}

// CanMove reports whether t may move to status to.
func (t Ticket) CanMove(to Status) bool {
	for _, s := range t.NextStatuses() {
		if s == to {
			return true
		}
	}
	return false
}

// CheckGate decides whether actor, acting as actorRole, may move t to
// status to. Only the reviewer sets a ship ticket done, or the planner once
// its PR merged outside a review; only its owner sets a scout ticket done.
// Run it after CanMove.
func (t Ticket) CheckGate(to Status, actor, actorRole string) error {
	if to != StatusDone {
		return nil
	}
	switch t.Type {
	case TypeShip:
		if actorRole != RoleReviewer && actorRole != RolePlanner {
			return fmt.Errorf("%w: only %s or %s sets ship tickets done (actor role %q; pass --actor-role)",
				ErrForbidden, RoleReviewer, RolePlanner, actorRole)
		}
	case TypeScout:
		if t.Owner == "" || t.Owner == "unknown" || actor != t.Owner {
			return fmt.Errorf("%w: only the owner (%s) sets scout ticket %s done, not %q", ErrForbidden, t.Owner, t.ID, actor)
		}
	}
	return nil
}

// LiveClaim returns the claim if it has not expired.
func (t Ticket) LiveClaim() *Claim {
	if t.Claim == nil || t.Claim.Expired {
		return nil
	}
	return t.Claim
}

// CheckFence decides whether a write by holder, carrying lease token, may
// touch t. A ticket under a live claim takes writes only from its holder
// with the current token. A writer that names a holder or token must hold
// the live claim, so a session whose lease expired or was re-claimed is
// fenced out even when nobody holds the ticket now. A writer naming neither
// may write to an unclaimed ticket.
func (t Ticket) CheckFence(holder string, token int64) error {
	c := t.LiveClaim()
	switch {
	case c == nil && holder == "" && token == 0:
		return nil
	case c == nil:
		return fmt.Errorf("%w: %s has no live claim by %s with token %d; claim it again", ErrLeaseLost, t.ID, holder, token)
	case holder == "":
		return fmt.Errorf("%w: %s is held by %s; pass --holder and --token to act on your own claim", ErrClaimed, t.ID, c.Holder)
	case holder != c.Holder:
		return fmt.Errorf("%w: %s is held by %s until %s", ErrClaimed, t.ID, c.Holder, c.ExpiresAt.Format(time.RFC3339))
	case token == 0:
		return fmt.Errorf("%w: %s is claimed; pass --token with the lease token from your claim", ErrInvalid, t.ID)
	case token != c.Token:
		return fmt.Errorf("%w: %s lease token is %d, not %d; it was re-claimed", ErrLeaseLost, t.ID, c.Token, token)
	}
	return nil
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
	EventUpdated      = "updated"
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

// CreateRequest creates a ticket. Type defaults to ship. Status may be
// backlog, todo (default) or blocked; a ticket created blocked returns to
// todo. Owner defaults to the actor.
type CreateRequest struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Type      Type     `json:"type"`
	Role      string   `json:"role"`
	Priority  *int     `json:"priority,omitempty"`
	Status    Status   `json:"status"`
	Owner     string   `json:"owner"`
	DependsOn []ID     `json:"depends_on,omitempty"`
	Areas     []string `json:"areas,omitempty"`
	// SplitFrom names the ticket this one splits or rewrites. The new
	// ticket starts with its tries, so a rewrite can't reset the count
	// that sends repeated send-backs up.
	SplitFrom      ID     `json:"split_from,omitempty"`
	Actor          string `json:"actor"`
	IdempotencyKey string `json:"-"`
}

// Validate checks the request and fills defaults.
func (r *CreateRequest) Validate() error {
	var err error
	if r.SplitFrom < 0 {
		return fmt.Errorf("%w: bad ticket id %d", ErrInvalid, r.SplitFrom)
	}
	if r.Areas, err = NormalizeAreas(r.Areas); err != nil {
		return err
	}
	if err := validateIDs(r.DependsOn); err != nil {
		return err
	}
	r.Title = strings.TrimSpace(r.Title)
	if r.Title == "" {
		return fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if strings.ContainsAny(r.Title, "\r\n") {
		return fmt.Errorf("%w: title must be one line", ErrInvalid)
	}
	if r.Type == "" {
		r.Type = TypeShip
	}
	if !r.Type.Valid() {
		return fmt.Errorf("%w: unknown ticket type %q (want ship or scout)", ErrInvalid, r.Type)
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
	if r.Status != StatusBacklog && r.Status != StatusTodo && r.Status != StatusBlocked {
		return fmt.Errorf("%w: new tickets start as backlog, todo or blocked, not %q", ErrInvalid, r.Status)
	}
	r.Actor = actorFor(r.Actor, "")
	r.Owner = actorFor(r.Owner, r.Actor)
	if strings.ContainsAny(r.Owner, "\r\n") {
		return fmt.Errorf("%w: owner must be one line", ErrInvalid)
	}
	if r.Type == TypeScout && r.Owner == "unknown" {
		return fmt.Errorf("%w: scout tickets need an owner to close them (--owner or --actor)", ErrInvalid)
	}
	return nil
}

var areaPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// NormalizeAreas validates code areas, trims a trailing slash, and returns
// them sorted with duplicates removed. An area is a repo-relative path such
// as "tools/maestro-ticket" or "skills".
func NormalizeAreas(areas []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, a := range areas {
		a = strings.TrimSuffix(strings.TrimSpace(a), "/")
		if a == "" {
			continue
		}
		if !areaPattern.MatchString(a) || len(a) > 200 || hasDotSegment(a) {
			return nil, fmt.Errorf("%w: area %q must be a repo-relative path like tools/maestro-ticket", ErrInvalid, a)
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out, nil
}

// hasDotSegment reports whether path a has a "." or ".." segment. Those
// spell another path ("tools/x/.." is "tools"), which the overlap check
// would miss. Areas compare case-sensitively, as git paths do.
func hasDotSegment(a string) bool {
	for _, seg := range strings.Split(a, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// AreasOverlap reports whether two areas cover any common path: they are
// equal, or one is a directory containing the other.
func AreasOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// AnyAreaOverlaps reports whether any area in as overlaps any in bs.
func AnyAreaOverlaps(as, bs []string) (string, string, bool) {
	for _, a := range as {
		for _, b := range bs {
			if AreasOverlap(a, b) {
				return a, b, true
			}
		}
	}
	return "", "", false
}

func validateIDs(ids []ID) error {
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("%w: bad ticket id %d", ErrInvalid, id)
		}
	}
	return nil
}

// ParseIDs parses a comma-separated ticket id list.
func ParseIDs(s string) ([]ID, error) {
	var out []ID
	for _, part := range strings.Split(s, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		id, err := ParseID(part)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// SplitList splits a comma-separated flag value, dropping empty parts.
func SplitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// UpdateRequest changes a ticket's dependencies or areas. Areas, when not
// nil, replaces the whole list. Holder and Token are fenced like a
// transition's.
type UpdateRequest struct {
	ID             ID        `json:"id"`
	AddDeps        []ID      `json:"add_deps,omitempty"`
	RemoveDeps     []ID      `json:"remove_deps,omitempty"`
	Areas          *[]string `json:"areas,omitempty"`
	Holder         string    `json:"holder,omitempty"`
	Token          int64     `json:"token,omitempty"`
	Actor          string    `json:"actor"`
	IdempotencyKey string    `json:"-"`
}

// Validate checks the request and fills defaults.
func (r *UpdateRequest) Validate() error {
	if len(r.AddDeps) == 0 && len(r.RemoveDeps) == 0 && r.Areas == nil {
		return fmt.Errorf("%w: nothing to update (pass --add-deps, --remove-deps or --areas)", ErrInvalid)
	}
	if err := validateIDs(r.AddDeps); err != nil {
		return err
	}
	if err := validateIDs(r.RemoveDeps); err != nil {
		return err
	}
	for _, id := range r.AddDeps {
		if id == r.ID {
			return fmt.Errorf("%w: %s can't depend on itself", ErrInvalid, id)
		}
	}
	if r.Areas != nil {
		a, err := NormalizeAreas(*r.Areas)
		if err != nil {
			return err
		}
		r.Areas = &a
	}
	if r.Holder != "" {
		if err := ValidateHolder(r.Holder); err != nil {
			return err
		}
	}
	if r.Token < 0 {
		return fmt.Errorf("%w: token must be positive", ErrInvalid)
	}
	r.Actor = actorFor(r.Actor, r.Holder)
	return nil
}

// ListFilter selects tickets. Zero values mean "any".
type ListFilter struct {
	Statuses []Status
	Role     string
	Holder   string // tickets with a live claim by this holder
	Claimed  *bool  // true: live claim; false: no live claim
	// Ready keeps only tickets a claim could start now, as ClaimNext picks
	// them: no live claim, and in todo or in_progress every dep done and no
	// area overlapping a live claim's.
	Ready bool
	Limit int
}

// ClaimRequest leases the next matching ticket, or a specific one when ID is
// set. For --next claims Statuses defaults to [todo]. Backlog tickets are
// claimed only by id, when the planner grooms them, never by --next.
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
		if s == StatusBacklog && r.ID == 0 {
			return fmt.Errorf("%w: backlog tickets are claimed by id while grooming, not with --next", ErrInvalid)
		}
	}
	r.Actor = actorFor(r.Actor, r.Holder)
	return nil
}

// HeartbeatRequest extends the live lease held by Holder with Token. The
// token is required, so a leftover process of a session that re-claimed
// can't keep the newer lease alive.
type HeartbeatRequest struct {
	ID     ID
	Holder string
	Token  int64
	TTL    time.Duration
}

// Validate checks the request and fills defaults.
func (r *HeartbeatRequest) Validate() error {
	if err := ValidateHolder(r.Holder); err != nil {
		return err
	}
	if err := requireToken(r.Token); err != nil {
		return err
	}
	ttl, err := ValidateTTL(r.TTL)
	r.TTL = ttl
	return err
}

func requireToken(token int64) error {
	if token <= 0 {
		return fmt.Errorf("%w: --token is required: pass the lease token from your claim", ErrInvalid)
	}
	return nil
}

// ReleaseRequest drops the lease Holder holds with Token. The token is
// required, so a stale session can't drop a newer lease of the same holder.
// Releasing a lease that is already gone (released, expired or re-claimed
// and since freed) is a no-op, so cleanup can run twice.
type ReleaseRequest struct {
	ID             ID     `json:"id"`
	Holder         string `json:"holder"`
	Token          int64  `json:"token,omitempty"`
	Actor          string `json:"actor"`
	IdempotencyKey string `json:"-"`
}

// Validate checks the request and fills defaults.
func (r *ReleaseRequest) Validate() error {
	if err := ValidateHolder(r.Holder); err != nil {
		return err
	}
	if err := requireToken(r.Token); err != nil {
		return err
	}
	r.Actor = actorFor(r.Actor, r.Holder)
	return nil
}

// TransitionRequest moves a ticket to a new status. From and ExpectVersion
// are optional optimistic checks. Holder and Token are fenced (see
// Ticket.CheckFence): if the ticket has a live claim, both must match it.
type TransitionRequest struct {
	ID             ID     `json:"id"`
	From           Status `json:"from,omitempty"`
	To             Status `json:"to"`
	ExpectVersion  int64  `json:"expect_version,omitempty"`
	Holder         string `json:"holder,omitempty"`
	Token          int64  `json:"token,omitempty"`
	Role           string `json:"role,omitempty"` // reassign, e.g. on escalation
	ActorRole      string `json:"actor_role,omitempty"`
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
	if r.Token < 0 {
		return fmt.Errorf("%w: token must be positive", ErrInvalid)
	}
	if err := ValidateRole(r.Role); err != nil {
		return err
	}
	if err := ValidateRole(r.ActorRole); err != nil {
		return err
	}
	r.Actor = actorFor(r.Actor, r.Holder)
	return nil
}

// CommentRequest adds a comment. Holder and Token are fenced like a
// transition's: a claimed ticket takes comments only from its holder.
type CommentRequest struct {
	ID             ID     `json:"id"`
	Body           string `json:"body"`
	Holder         string `json:"holder,omitempty"`
	Token          int64  `json:"token,omitempty"`
	Actor          string `json:"actor"`
	IdempotencyKey string `json:"-"`
}

// Validate checks the request and fills defaults.
func (r *CommentRequest) Validate() error {
	if strings.TrimSpace(r.Body) == "" {
		return fmt.Errorf("%w: comment body is required", ErrInvalid)
	}
	if r.Holder != "" {
		if err := ValidateHolder(r.Holder); err != nil {
			return err
		}
	}
	if r.Token < 0 {
		return fmt.Errorf("%w: token must be positive", ErrInvalid)
	}
	r.Actor = actorFor(r.Actor, r.Holder)
	return nil
}

// EventFilter selects events with Seq > Since, in order.
type EventFilter struct {
	Since    int64
	TicketID ID
	Type     string
	Limit    int
}

// ReapResult lists the tickets whose expired leases a reap freed.
type ReapResult struct {
	Reaped []ID `json:"reaped"`
}

// Export is the whole store as JSON, read in one snapshot: every ticket with
// its comments, and every event.
type Export struct {
	SchemaVersion int      `json:"schema_version"`
	LastSeq       int64    `json:"last_seq"`
	Tickets       []Detail `json:"tickets"`
	Events        []Event  `json:"events"`
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
	// oldest id). A ticket is claimable when its lease is missing or expired,
	// every ticket it depends on is done, and none of its areas overlaps an
	// area of a ticket under a live claim. Returns ErrNoWork when nothing
	// matches. Claiming by id fails with ErrWaitingOnDeps or ErrAreaBusy
	// instead, except for backlog tickets, which are claimed to groom them.
	ClaimNext(ctx context.Context, req ClaimRequest) (Ticket, error)
	Heartbeat(ctx context.Context, req HeartbeatRequest) (Ticket, error)
	// Reap frees every expired lease, logging lease_expired for each and
	// counting it on the ticket, so a session that died is reported without
	// waiting for someone to claim its ticket. Any write to a ticket with an
	// expired lease does the same for that ticket first.
	Reap(ctx context.Context) (ReapResult, error)
	Release(ctx context.Context, req ReleaseRequest) (Ticket, error)
	Transition(ctx context.Context, req TransitionRequest) (Ticket, error)
	Comment(ctx context.Context, req CommentRequest) (Comment, error)
	// Update changes dependencies or areas. Adding a dependency that would
	// make a cycle fails with ErrInvalid.
	Update(ctx context.Context, req UpdateRequest) (Ticket, error)
	Events(ctx context.Context, f EventFilter) ([]Event, error)
	// LastSeq returns the newest event seq, or 0 for an empty log.
	LastSeq(ctx context.Context) (int64, error)
	Doctor(ctx context.Context) (DoctorReport, error)
	// Backup writes a consistent snapshot of the store to a new file at
	// path, without blocking writers, and checks it opens clean.
	Backup(ctx context.Context, path string) error
	// Export reads the whole store in one snapshot.
	Export(ctx context.Context) (Export, error)
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
