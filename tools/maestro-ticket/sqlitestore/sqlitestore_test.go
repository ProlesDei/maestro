package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)}
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "maestro", "tickets.db"), Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, clk
}

func mustCreate(t *testing.T, s *Store, title, role string, prio int) ticket.Ticket {
	t.Helper()
	tk, err := s.Create(context.Background(), ticket.CreateRequest{Title: title, Role: role, Priority: &prio, Actor: "cos"})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v; want %v", err, want)
	}
}

func TestCreateGetList(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	a := mustCreate(t, s, "a", "engineer", 2)
	b := mustCreate(t, s, "b", "reviewer", 0)
	if a.ID != 1 || b.ID != 2 || a.Version != 1 || a.Status != ticket.StatusTodo || a.CreatedBy != "cos" {
		t.Fatalf("unexpected tickets %+v %+v", a, b)
	}
	d, err := s.Get(ctx, a.ID)
	if err != nil || d.Title != "a" || len(d.Comments) != 0 {
		t.Fatalf("Get = %+v, %v", d, err)
	}
	_, err = s.Get(ctx, 99)
	wantErr(t, err, ticket.ErrNotFound)

	all, _ := s.List(ctx, ticket.ListFilter{})
	if len(all) != 2 || all[0].ID != b.ID {
		t.Fatalf("List should order by priority: %+v", all)
	}
	jse, _ := s.List(ctx, ticket.ListFilter{Role: "engineer"})
	if len(jse) != 1 || jse[0].ID != a.ID {
		t.Fatalf("role filter: %+v", jse)
	}
	none, _ := s.List(ctx, ticket.ListFilter{Statuses: []ticket.Status{ticket.StatusDone}})
	if len(none) != 0 {
		t.Fatalf("status filter: %+v", none)
	}
}

// List order is part of the contract: the playbook engine takes the first
// match. Priority first (0 is most urgent), then id as a number, so T-2
// comes before T-10.
func TestListOrderPriorityThenID(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	prios := []int{2, 1, 2, 0, 1, 2, 2, 2, 2, 2, 1}
	for i, p := range prios {
		mustCreate(t, s, fmt.Sprint("t", i+1), "chief-of-staff", p)
	}
	all, err := s.List(ctx, ticket.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tk := range all {
		got = append(got, tk.ID.String())
	}
	want := "[T-4 T-2 T-5 T-11 T-1 T-3 T-6 T-7 T-8 T-9 T-10]"
	if fmt.Sprint(got) != want {
		t.Fatalf("order %v; want %s", got, want)
	}
	top, _ := s.List(ctx, ticket.ListFilter{Limit: 2})
	if len(top) != 2 || top[0].ID != 4 || top[1].ID != 2 {
		t.Fatalf("limit keeps the order: %+v", top)
	}
}

func TestClaimNextOrderAndFilters(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	mustCreate(t, s, "low", "engineer", 3)
	urgent := mustCreate(t, s, "urgent", "engineer", 0)
	mustCreate(t, s, "senior", "reviewer", 0)

	got, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", Role: "engineer"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != urgent.ID || got.Claim == nil || got.Claim.Holder != "jse/s1" || got.Version != 2 {
		t.Fatalf("claimed %+v", got)
	}
	if got.Status != ticket.StatusTodo {
		t.Fatal("claiming must not change status")
	}
	if want := time.Date(2026, 10, 28, 9, 30, 0, 0, time.UTC); !got.Claim.ExpiresAt.Equal(want) {
		t.Fatalf("expires %v; want %v", got.Claim.ExpiresAt, want)
	}
	// Same bot, second session, gets the next one.
	got2, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s2", Role: "engineer"})
	if err != nil || got2.Title != "low" {
		t.Fatalf("second claim %+v, %v", got2, err)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s3", Role: "engineer"})
	wantErr(t, err, ticket.ErrNoWork)

	// Status filter: nothing in review yet.
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "cr/s1", Statuses: []ticket.Status{ticket.StatusInReview}})
	wantErr(t, err, ticket.ErrNoWork)

	mine, _ := s.List(ctx, ticket.ListFilter{Holder: "jse/s1"})
	if len(mine) != 1 || mine[0].ID != urgent.ID {
		t.Fatalf("holder filter %+v", mine)
	}
	yes, no := true, false
	claimed, _ := s.List(ctx, ticket.ListFilter{Claimed: &yes})
	free, _ := s.List(ctx, ticket.ListFilter{Claimed: &no})
	if len(claimed) != 2 || len(free) != 1 {
		t.Fatalf("claimed=%d free=%d", len(claimed), len(free))
	}
}

func TestClaimByID(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "engineer", 2)

	got, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s1"})
	if err != nil || got.Claim.Holder != "jse/s1" || got.Claim.Token != 1 {
		t.Fatalf("claim by id %+v, %v", got, err)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "sse/s1"})
	wantErr(t, err, ticket.ErrClaimed)

	// Re-claiming your own live lease refreshes it under a new lease token
	// and version, so writes with the old token are fenced.
	again, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s1", TTL: time.Hour})
	if err != nil || again.Version != got.Version+1 || again.Claim.Token != got.Claim.Token+1 ||
		!again.Claim.ExpiresAt.After(got.Claim.ExpiresAt) {
		t.Fatalf("refresh %+v, %v", again, err)
	}
	evs, _ := s.Events(ctx, ticket.EventFilter{TicketID: tk.ID, Type: ticket.EventClaimed})
	var data map[string]any
	json.Unmarshal(evs[len(evs)-1].Data, &data)
	if len(evs) != 2 || data["renewed"] != true || data["token"] != float64(2) {
		t.Fatalf("re-claim events %d, last %v", len(evs), data)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s1", Statuses: []ticket.Status{ticket.StatusInReview}})
	wantErr(t, err, ticket.ErrStatusConflict)
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s1", Role: "reviewer"})
	wantErr(t, err, ticket.ErrStatusConflict)
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: 404, Holder: "jse/s1"})
	wantErr(t, err, ticket.ErrNotFound)
}

func TestLeaseExpiryAndHeartbeat(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	c, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/crashed", TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	tok := c.Claim.Token

	clk.Advance(9 * time.Minute)
	hb, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/crashed", Token: tok, TTL: 10 * time.Minute})
	if err != nil || !hb.Claim.ExpiresAt.Equal(clk.Now().Add(10*time.Minute)) || hb.Version != 2 {
		t.Fatalf("heartbeat %+v, %v", hb, err)
	}
	_, err = s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/other", Token: tok})
	wantErr(t, err, ticket.ErrLeaseLost)

	// The session crashes: no more heartbeats. Still blocked before expiry.
	clk.Advance(9 * time.Minute)
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/fresh"})
	wantErr(t, err, ticket.ErrNoWork)

	clk.Advance(2 * time.Minute)
	got, _ := s.Get(ctx, tk.ID)
	if got.Claim == nil || !got.Claim.Expired || got.LiveClaim() != nil {
		t.Fatalf("lease should read as expired: %+v", got.Claim)
	}
	taken, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/fresh"})
	if err != nil || taken.Claim.Holder != "jse/fresh" || taken.Claim.Expired || taken.LeaseExpiries != 1 {
		t.Fatalf("expired lease should be claimable: %+v, %v", taken, err)
	}
	_, err = s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/crashed", Token: tok})
	wantErr(t, err, ticket.ErrLeaseLost)

	evs, _ := s.Events(ctx, ticket.EventFilter{TicketID: tk.ID})
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	want := []string{"created", "claimed", "lease_expired", "claimed"}
	if fmt.Sprint(types) != fmt.Sprint(want) {
		t.Fatalf("events %v; want %v", types, want)
	}
	// The store expired the lease, not the session that claimed next.
	var data map[string]any
	json.Unmarshal(evs[2].Data, &data)
	if evs[2].Actor != ticket.SystemActor || data["holder"] != "jse/crashed" || data["token"] != float64(tok) || data["expiries"] != float64(1) {
		t.Fatalf("lease_expired event actor %q data %v", evs[2].Actor, data)
	}
}

func TestHeartbeatAfterExpiryFails(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	c, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", TTL: time.Minute})
	clk.Advance(2 * time.Minute)
	_, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/s1", Token: c.Claim.Token})
	wantErr(t, err, ticket.ErrLeaseLost)
}

// A heartbeat must carry the current token: a leftover process of a session
// that re-claimed can't keep the newer lease alive, and can't extend a lease
// without saying which one.
func TestHeartbeatFencedByToken(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	old, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", TTL: time.Minute})
	cur, _ := s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s1", TTL: time.Minute})
	_, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/s1"})
	wantErr(t, err, ticket.ErrInvalid)
	_, err = s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/s1", Token: old.Claim.Token, TTL: time.Hour})
	wantErr(t, err, ticket.ErrLeaseLost)
	if got, _ := s.Get(ctx, tk.ID); !got.Claim.ExpiresAt.Equal(cur.Claim.ExpiresAt) {
		t.Fatalf("stale heartbeat extended the lease to %s", got.Claim.ExpiresAt)
	}
	if _, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/s1", Token: cur.Claim.Token, TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(30 * time.Minute)
	if got, _ := s.Get(ctx, tk.ID); got.LiveClaim() == nil {
		t.Fatal("current heartbeat didn't extend the lease")
	}
}

func TestRelease(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	c, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1"})
	tok := c.Claim.Token

	_, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s2", Token: tok})
	wantErr(t, err, ticket.ErrClaimed)
	// The token is required.
	_, err = s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s1"})
	wantErr(t, err, ticket.ErrInvalid)

	r, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s1", Token: tok})
	if err != nil || r.Claim != nil || r.Version != 3 {
		t.Fatalf("release %+v, %v", r, err)
	}
	// Twice is a no-op.
	r2, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s1", Token: tok})
	if err != nil || r2.Version != 3 {
		t.Fatalf("second release %+v, %v", r2, err)
	}
	// Releasing an expired lease, ours or not, is a no-op for the caller, and
	// the store logs it as expired, not released.
	c3, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s3", TTL: time.Minute})
	clk.Advance(2 * time.Minute)
	before, _ := s.LastSeq(ctx)
	r3, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s3", Token: c3.Claim.Token})
	if err != nil || r3.Claim != nil || r3.LeaseExpiries != 1 || r3.Version != c3.Version {
		t.Fatalf("own expired release %+v, %v", r3, err)
	}
	evs, _ := s.Events(ctx, ticket.EventFilter{Since: before})
	if len(evs) != 1 || evs[0].Type != ticket.EventLeaseExpired {
		t.Fatalf("events after releasing an expired lease: %+v; want one lease_expired", evs)
	}
	if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s1", Token: tok}); err != nil {
		t.Fatal(err)
	}
}

func TestTransitionChecks(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "engineer", 2)
	c, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1"})

	// Claimed by someone else, or no holder given.
	_, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress, Holder: "jse/s2"})
	wantErr(t, err, ticket.ErrClaimed)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress})
	wantErr(t, err, ticket.ErrClaimed)
	// Wrong from / version / disallowed transition.
	tok := c.Claim.Token
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, From: ticket.StatusInReview, To: ticket.StatusInProgress, Holder: "jse/s1", Token: tok})
	wantErr(t, err, ticket.ErrStatusConflict)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress, Holder: "jse/s1", Token: tok, ExpectVersion: 1})
	wantErr(t, err, ticket.ErrVersionConflict)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusDone, Holder: "jse/s1", Token: tok})
	wantErr(t, err, ticket.ErrInvalidTransition)
	// The holder without its token.
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress, Holder: "jse/s1"})
	wantErr(t, err, ticket.ErrInvalid)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: 404, To: ticket.StatusDone})
	wantErr(t, err, ticket.ErrNotFound)

	got, err := s.Transition(ctx, ticket.TransitionRequest{
		ID: tk.ID, From: ticket.StatusTodo, To: ticket.StatusInProgress, Holder: "jse/s1", Token: tok, ExpectVersion: c.Version,
	})
	if err != nil || got.Status != ticket.StatusInProgress || got.Version != c.Version+1 || got.Claim == nil {
		t.Fatalf("transition %+v, %v", got, err)
	}
	// Escalation: back to todo, reassigned, claim dropped in the same write.
	esc, err := s.Transition(ctx, ticket.TransitionRequest{
		ID: tk.ID, To: ticket.StatusTodo, Holder: "jse/s1", Token: tok, Role: "chief-of-staff",
		Note: "2 failed tries", Release: true,
	})
	if err != nil || esc.Role != "chief-of-staff" || esc.Claim != nil {
		t.Fatalf("escalate %+v, %v", esc, err)
	}
	evs, _ := s.Events(ctx, ticket.EventFilter{TicketID: tk.ID, Type: ticket.EventTransitioned})
	var data map[string]any
	json.Unmarshal(evs[len(evs)-1].Data, &data)
	if data["note"] != "2 failed tries" || data["role"] != "chief-of-staff" || data["released"] != "jse/s1" {
		t.Fatalf("event data %v", data)
	}
}

func TestTerminalTransitionClearsLease(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	c, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "cos/s1"})
	got, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusCancelled, Holder: "cos/s1", Token: c.Claim.Token})
	if err != nil || got.Claim != nil {
		t.Fatalf("cancel %+v, %v", got, err)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "cos/s1"})
	wantErr(t, err, ticket.ErrStatusConflict)
}

func TestFullWorkflow(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "ship it", "engineer", 1)
	steps := []struct {
		holder string
		from   ticket.Status
		to     ticket.Status
		role   string
	}{
		{"sse/s1", ticket.StatusTodo, ticket.StatusInProgress, ""},
		{"sse/s1", ticket.StatusInProgress, ticket.StatusInReview, ""},
		{"cr/s1", ticket.StatusInReview, ticket.StatusInProgress, ""}, // changes requested
		{"sse/s2", ticket.StatusInProgress, ticket.StatusInReview, ""},
		{"cr/s2", ticket.StatusInReview, ticket.StatusDone, ticket.RoleReviewer},
	}
	for _, st := range steps {
		cur, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: st.holder, Statuses: []ticket.Status{st.from}})
		if err != nil {
			t.Fatalf("%s claim %s: %v", st.holder, st.from, err)
		}
		_, err = s.Transition(ctx, ticket.TransitionRequest{
			ID: cur.ID, From: st.from, To: st.to, Holder: st.holder, Token: cur.Claim.Token, ExpectVersion: cur.Version, Release: true,
			ActorRole: st.role,
		})
		if err != nil {
			t.Fatalf("%s %s->%s: %v", st.holder, st.from, st.to, err)
		}
	}
	final, _ := s.Get(ctx, tk.ID)
	if final.Status != ticket.StatusDone || final.Claim != nil || final.Tries != 1 {
		t.Fatalf("final %+v; want done, unclaimed, 1 try (one send-back)", final)
	}
}

func TestComments(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	c, err := s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "handoff", Actor: "jse"})
	if err != nil || c.ID != 1 || c.Author != "jse" {
		t.Fatalf("comment %+v, %v", c, err)
	}
	_, err = s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "  "})
	wantErr(t, err, ticket.ErrInvalid)
	_, err = s.Comment(ctx, ticket.CommentRequest{ID: 404, Body: "x"})
	wantErr(t, err, ticket.ErrNotFound)
	d, _ := s.Get(ctx, tk.ID)
	if len(d.Comments) != 1 || d.Comments[0].Body != "handoff" || d.Version != 1 {
		t.Fatalf("detail %+v", d)
	}
}

func TestIdempotency(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p := 1
	req := ticket.CreateRequest{Title: "once", Priority: &p, IdempotencyKey: "sess:create"}
	a, err := s.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, req)
	if err != nil || b.ID != a.ID {
		t.Fatalf("replay %+v, %v", b, err)
	}
	other := req
	other.Title = "different"
	_, err = s.Create(ctx, other)
	wantErr(t, err, ticket.ErrIdempotencyConflict)
	if all, _ := s.List(ctx, ticket.ListFilter{}); len(all) != 1 {
		t.Fatalf("want 1 ticket, got %d", len(all))
	}

	claim := ticket.ClaimRequest{Holder: "jse/s1", IdempotencyKey: "sess:claim"}
	c1, err := s.ClaimNext(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := s.ClaimNext(ctx, claim)
	if err != nil || c2.ID != c1.ID || c2.Version != c1.Version {
		t.Fatalf("claim replay %+v, %v", c2, err)
	}
	// Same key used for a different operation.
	_, err = s.Release(ctx, ticket.ReleaseRequest{ID: a.ID, Holder: "jse/s1", Token: c1.Claim.Token, IdempotencyKey: "sess:claim"})
	wantErr(t, err, ticket.ErrIdempotencyConflict)

	tr := ticket.TransitionRequest{ID: a.ID, To: ticket.StatusInProgress, Holder: "jse/s1", Token: c1.Claim.Token, ExpectVersion: c1.Version, IdempotencyKey: "sess:start"}
	t1, err := s.Transition(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	// Without the key, this retry would fail on version; with it, it replays.
	t2, err := s.Transition(ctx, tr)
	if err != nil || t2.Version != t1.Version {
		t.Fatalf("transition replay %+v, %v", t2, err)
	}

	cm := ticket.CommentRequest{ID: a.ID, Body: "note", Holder: "jse/s1", Token: c1.Claim.Token, IdempotencyKey: "sess:note"}
	s.Comment(ctx, cm)
	s.Comment(ctx, cm)
	if d, _ := s.Get(ctx, a.ID); len(d.Comments) != 1 {
		t.Fatalf("want 1 comment, got %d", len(d.Comments))
	}
	// One event per applied write, none for replays.
	evs, _ := s.Events(ctx, ticket.EventFilter{})
	if len(evs) != 4 { // created, claimed, transitioned, commented
		t.Fatalf("want 4 events, got %d", len(evs))
	}
}

func TestFailedWriteRollsBack(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	before, _ := s.LastSeq(ctx)
	_, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusDone, IdempotencyKey: "k"})
	wantErr(t, err, ticket.ErrInvalidTransition)
	after, _ := s.LastSeq(ctx)
	if before != after {
		t.Fatal("failed write appended an event")
	}
	// The key was not consumed by the failure.
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusBlocked, IdempotencyKey: "k"}); err != nil {
		t.Fatal(err)
	}
}

func TestEventsCursor(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		mustCreate(t, s, "x", "", 2)
	}
	all, _ := s.Events(ctx, ticket.EventFilter{})
	for i := 1; i < len(all); i++ {
		if all[i].Seq <= all[i-1].Seq {
			t.Fatal("seq not increasing")
		}
	}
	page, _ := s.Events(ctx, ticket.EventFilter{Since: all[1].Seq, Limit: 2})
	if len(page) != 2 || page[0].Seq != all[2].Seq {
		t.Fatalf("page %+v", page)
	}
	last, _ := s.LastSeq(ctx)
	if last != all[len(all)-1].Seq {
		t.Fatalf("LastSeq %d", last)
	}
	if rest, _ := s.Events(ctx, ticket.EventFilter{Since: last}); len(rest) != 0 {
		t.Fatal("nothing should follow the last seq")
	}
}

func TestEventsAppendOnly(t *testing.T) {
	s, _ := newStore(t)
	mustCreate(t, s, "x", "", 2)
	if _, err := s.w.Exec("UPDATE events SET type = 'x'"); err == nil {
		t.Fatal("update of events should fail")
	}
	if _, err := s.w.Exec("DELETE FROM events"); err == nil {
		t.Fatal("delete of events should fail")
	}
}

func TestPragmasAndDoctor(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	rep, err := s.Doctor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("doctor: %+v", rep)
	}
	// The reader pool gets the same settings.
	var mode string
	var fk, timeout int
	s.r.QueryRow("PRAGMA journal_mode").Scan(&mode)
	s.r.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	s.r.QueryRow("PRAGMA busy_timeout").Scan(&timeout)
	if mode != "wal" || fk != 1 || timeout != 5000 {
		t.Fatalf("reader pragmas: %s %d %d", mode, fk, timeout)
	}
}

func TestDoctorFlagsBadSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	db, _ := sql.Open("sqlite", path)
	db.Exec("PRAGMA journal_mode=DELETE")
	db.Close()
	s, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Open always switches to WAL, so a store opened by us is healthy.
	rep, _ := s.Doctor(context.Background())
	if !rep.OK() {
		t.Fatalf("doctor: %+v", rep)
	}
	s.w.Exec("PRAGMA user_version = 7")
	rep, _ = s.Doctor(context.Background())
	if rep.OK() {
		t.Fatal("doctor should flag a wrong schema version")
	}
}

func TestDoctorFlagsUnknownStatus(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Create(ctx, ticket.CreateRequest{Title: "stale"})
	s.w.Exec("UPDATE tickets SET status = 'in_qa' WHERE id = 1")
	rep, _ := s.Doctor(ctx)
	for _, c := range rep.Checks {
		if c.Name == "statuses" {
			if c.OK || !strings.Contains(c.Detail, "T-1 (in_qa)") {
				t.Fatalf("statuses check: %+v", c)
			}
			return
		}
	}
	t.Fatalf("no statuses check: %+v", rep)
}

func TestReopenKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	ctx := context.Background()
	s, _ := Open(ctx, path, Options{})
	s.Create(ctx, ticket.CreateRequest{Title: "persist"})
	s.Close()
	s2, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if d, err := s2.Get(ctx, 1); err != nil || d.Title != "persist" {
		t.Fatalf("%+v, %v", d, err)
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	db, _ := sql.Open("sqlite", path)
	db.Exec("PRAGMA user_version = 99")
	db.Close()
	if _, err := Open(context.Background(), path, Options{}); err == nil {
		t.Fatal("should refuse a newer schema")
	}
}

// busyErr looks like SQLITE_BUSY to isBusy.
type busyErr struct{}

func (busyErr) Error() string { return "database is locked (5) (test)" }
func (busyErr) Code() int     { return 5 }

// A busy retry after a full scan must not repeat rows.
func TestReadRetryDoesNotDuplicate(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		mustCreate(t, s, "x", "", 2)
	}
	fails := 0
	s.faultRead = func() error {
		if fails < 2 {
			fails++
			return busyErr{}
		}
		return nil
	}
	list, err := s.List(ctx, ticket.ListFilter{})
	if err != nil || len(list) != 3 || fails != 2 {
		t.Fatalf("List after busy retries: %d tickets, %v (fails %d); want 3", len(list), err, fails)
	}
	fails = 0
	evs, err := s.Events(ctx, ticket.EventFilter{})
	if err != nil || len(evs) != 3 || fails != 2 {
		t.Fatalf("Events after busy retries: %d events, %v (fails %d); want 3", len(evs), err, fails)
	}
	fails = 0
	d, err := s.Get(ctx, 1)
	if err != nil || len(d.Comments) != 0 || fails != 2 {
		t.Fatalf("Get after busy retries: %+v, %v", d, err)
	}
}

// openV1 builds a store file exactly as schema v1 left it, with a todo
// ticket, a blocked ticket and a ticket under a live claim.
func openV1(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exp := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, q := range []string{
		schemaV1,
		`INSERT INTO tickets (title, status, priority, created_by, created_at, updated_at) VALUES ('a', 'todo', 2, 'cos', 1, 1)`,
		`INSERT INTO tickets (title, status, priority, created_by, created_at, updated_at) VALUES ('b', 'blocked', 2, 'cos', 1, 1)`,
		`INSERT INTO tickets (title, status, priority, created_by, version, lease_holder, lease_expires_at, created_at, updated_at)
			VALUES ('c', 'in_progress', 1, 'cos', 3, 'jse/s1', ` + fmt.Sprint(exp) + `, 1, 1)`,
		`INSERT INTO events (ticket_id, type, actor, created_at) VALUES (1, 'created', 'cos', 1)`,
		"PRAGMA user_version = 1",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return path
}

func TestMigrateV1(t *testing.T) {
	path := openV1(t)
	ctx := context.Background()
	s, err := Open(ctx, path, Options{Now: func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	s.w.QueryRow("PRAGMA user_version").Scan(&v)
	if v != schemaVersion || schemaVersion < 2 {
		t.Fatalf("user_version %d; want %d", v, schemaVersion)
	}
	all, err := s.List(ctx, ticket.ListFilter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("list %+v, %v", all, err)
	}
	by := map[string]ticket.Ticket{}
	for _, tk := range all {
		by[tk.Title] = tk
		if tk.Type != ticket.TypeShip || tk.Tries != 0 || tk.Owner != tk.CreatedBy {
			t.Errorf("%s: type %q tries %d owner %q; want ship, 0, %q", tk.ID, tk.Type, tk.Tries, tk.Owner, tk.CreatedBy)
		}
	}
	if by["a"].BlockedFrom != "" || by["b"].BlockedFrom != ticket.StatusTodo {
		t.Errorf("blocked_from: a=%q b=%q; want \"\", todo", by["a"].BlockedFrom, by["b"].BlockedFrom)
	}
	if c := by["c"].Claim; c == nil || c.Holder != "jse/s1" || c.Token != 1 || c.Expired || by["c"].Version != 3 {
		t.Errorf("live v1 claim after migration: %+v v%d", c, by["c"].Version)
	}
	if evs, _ := s.Events(ctx, ticket.EventFilter{}); len(evs) != 1 {
		t.Errorf("events lost: %d", len(evs))
	}
	rep, err := s.Doctor(ctx)
	if err != nil || !rep.OK() {
		t.Fatalf("doctor after migration: %+v, %v", rep, err)
	}
	// Reopening a migrated store is a no-op.
	s2, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
	if _, err := s.w.Exec("UPDATE tickets SET type = 'epic' WHERE id = 1"); err == nil {
		t.Error("type column should reject unknown types")
	}
}

// A v4 store with tickets in removed statuses and old role names migrates
// to in_review and current roles, logs each change, and loses the outbox.
// planner and qa-lead stay as they are. A role with no mapping stays for
// doctor to report.
func TestMigrateV5(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v4.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := append([]string{}, migrations[:4]...)
	stmts = append(stmts,
		`INSERT INTO tickets (title, status, role, priority, created_by, version, created_at, updated_at) VALUES
			('qa', 'in_qa', 'qa-lead', 2, 'cos', 2, 1, 1),
			('rel', 'ready_to_release', 'operations-manager', 2, 'cos', 1, 1, 1),
			('custom', 'todo', 'designer', 2, 'cos', 1, 1, 1),
			('old', 'done', 'junior-software-engineer', 2, 'cos', 1, 1, 1)`,
		`INSERT INTO tickets (title, status, blocked_from, role, priority, created_by, created_at, updated_at)
			VALUES ('blk', 'blocked', 'in_qa', 'senior-software-engineer', 2, 'cos', 1, 1)`,
		`INSERT INTO tickets (title, status, role, priority, created_by, version, created_at, updated_at) VALUES
			('plan', 'todo', 'planner', 2, 'cos', 1, 1, 1),
			('rev', 'in_review', 'code-reviewer', 2, 'cos', 1, 1, 1)`,
		`INSERT INTO events (ticket_id, type, actor, created_at) VALUES (1, 'created', 'cos', 1)`,
		`INSERT INTO outbox (event_id, kind, payload, next_attempt_at) VALUES (1, 'created', '{}', 1)`,
		"PRAGMA user_version = 4",
	)
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	ctx := context.Background()
	s, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := []struct {
		status, blockedFrom ticket.Status
		role                string
		version             int64
	}{
		{ticket.StatusInReview, "", ticket.RoleQALead, 3},
		{ticket.StatusInReview, "", ticket.RoleReviewer, 2},
		{ticket.StatusTodo, "", "designer", 1},
		{ticket.StatusDone, "", ticket.RoleEngineer, 1},
		{ticket.StatusBlocked, ticket.StatusInReview, ticket.RoleEngineer, 1},
		{ticket.StatusTodo, "", ticket.RolePlanner, 1},
		{ticket.StatusInReview, "", ticket.RoleReviewer, 1},
	}
	for i, w := range want {
		d, err := s.Get(ctx, ticket.ID(i+1))
		if err != nil || d.Status != w.status || d.BlockedFrom != w.blockedFrom || d.Role != w.role || d.Version != w.version {
			t.Errorf("T-%d: %s from %q role %q v%d, %v; want %+v", i+1, d.Status, d.BlockedFrom, d.Role, d.Version, err, w)
		}
	}
	evs, _ := s.Events(ctx, ticket.EventFilter{})
	var moves, updates int
	for _, e := range evs {
		if e.Actor != ticket.SystemActor {
			continue
		}
		var d map[string]any
		json.Unmarshal(e.Data, &d)
		switch {
		case e.Type == ticket.EventTransitioned && d["to"] == "in_review":
			moves++
		case e.Type == ticket.EventUpdated && (d["role"] != nil || d["blocked_from"] == "in_review"):
			updates++
		default:
			t.Errorf("unexpected migration event %+v", e)
		}
	}
	if moves != 2 || updates != 5 {
		t.Errorf("migration logged %d moves, %d updates; want 2, 5 (4 roles, 1 blocked_from)", moves, updates)
	}
	var outbox int
	s.r.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'outbox'").Scan(&outbox)
	if outbox != 0 {
		t.Error("outbox table survived v5")
	}

	rep, err := s.Doctor(ctx)
	if err != nil || rep.OK() {
		t.Fatalf("doctor should flag the unknown role: %+v, %v", rep, err)
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: 3, To: ticket.StatusBacklog, Role: ticket.RoleEngineer}); err != nil {
		t.Fatal(err)
	}
	if rep, err := s.Doctor(ctx); err != nil || !rep.OK() {
		t.Fatalf("doctor after fixing the role: %+v, %v", rep, err)
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: 1, To: ticket.StatusDone, ActorRole: ticket.RoleReviewer}); err != nil {
		t.Fatalf("reviewer done after migration: %v", err)
	}
}

// move runs one unclaimed transition and fails the test on error.
func move(t *testing.T, s *Store, id ticket.ID, to ticket.Status) ticket.Ticket {
	t.Helper()
	got, err := s.Transition(context.Background(), ticket.TransitionRequest{ID: id, To: to, Actor: "test"})
	if err != nil {
		t.Fatalf("%s -> %s: %v", id, to, err)
	}
	return got
}

// Blocked was once a shortcut past review. A blocked ticket now
// returns only to the status it was blocked from.
func TestBlockedReturnsOnlyToOrigin(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "engineer", 2)
	move(t, s, tk.ID, ticket.StatusInProgress)
	b := move(t, s, tk.ID, ticket.StatusBlocked)
	if b.BlockedFrom != ticket.StatusInProgress {
		t.Fatalf("blocked_from %q; want in_progress", b.BlockedFrom)
	}
	for _, to := range []ticket.Status{ticket.StatusInReview, ticket.StatusTodo, ticket.StatusDone} {
		_, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: to, Actor: "jse"})
		wantErr(t, err, ticket.ErrInvalidTransition)
	}
	back := move(t, s, tk.ID, ticket.StatusInProgress)
	if back.BlockedFrom != "" {
		t.Fatalf("blocked_from should clear on unblock: %q", back.BlockedFrom)
	}
	// Blocked from review goes back to review, not on to done.
	move(t, s, tk.ID, ticket.StatusInReview)
	move(t, s, tk.ID, ticket.StatusBlocked)
	_, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusDone, ActorRole: ticket.RoleReviewer})
	wantErr(t, err, ticket.ErrInvalidTransition)
	if got := move(t, s, tk.ID, ticket.StatusInReview); got.Tries != 0 {
		t.Fatalf("unblocking is not a send-back: tries %d", got.Tries)
	}

	// Created blocked: returns to todo.
	p := 2
	nb, err := s.Create(ctx, ticket.CreateRequest{Title: "waits", Priority: &p, Status: ticket.StatusBlocked})
	if err != nil || nb.BlockedFrom != ticket.StatusTodo {
		t.Fatalf("create blocked %+v, %v", nb, err)
	}
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: nb.ID, To: ticket.StatusInProgress})
	wantErr(t, err, ticket.ErrInvalidTransition)
	move(t, s, nb.ID, ticket.StatusTodo)
}

func TestTriesCountReviewSendBacks(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	for _, to := range []ticket.Status{
		ticket.StatusInProgress, ticket.StatusInReview, ticket.StatusInProgress, // review send-back: 1
		ticket.StatusInReview, ticket.StatusInProgress, // review send-back: 2
		ticket.StatusBlocked, ticket.StatusInProgress, // unblock: not a try
		ticket.StatusTodo, ticket.StatusInProgress, // escalation: not a try
	} {
		move(t, s, tk.ID, to)
	}
	got, _ := s.Get(ctx, tk.ID)
	if got.Tries != 2 {
		t.Fatalf("tries %d; want 2", got.Tries)
	}
	evs, _ := s.Events(ctx, ticket.EventFilter{TicketID: tk.ID, Type: ticket.EventTransitioned})
	var n int
	for _, e := range evs {
		var d map[string]any
		json.Unmarshal(e.Data, &d)
		if _, ok := d["tries"]; ok {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d events carry tries; want 2", n)
	}
}

// An owner sending a scout report back is not a try.
func TestScoutSendBackIsNotATry(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p := 2
	sc, err := s.Create(ctx, ticket.CreateRequest{Title: "why", Type: ticket.TypeScout, Priority: &p, Actor: "cos"})
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []ticket.Status{ticket.StatusInProgress, ticket.StatusInReview, ticket.StatusInProgress} {
		move(t, s, sc.ID, to)
	}
	if got, _ := s.Get(ctx, sc.ID); got.Tries != 0 {
		t.Fatalf("scout tries %d; want 0", got.Tries)
	}
}

func TestBacklogAndScoutTickets(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p := 0
	sc, err := s.Create(ctx, ticket.CreateRequest{
		Title: "why is CI slow", Type: ticket.TypeScout, Status: ticket.StatusBacklog, Priority: &p,
		Actor: "cos", Owner: "chief-of-staff",
	})
	if err != nil || sc.Type != ticket.TypeScout || sc.Status != ticket.StatusBacklog || sc.Owner != "chief-of-staff" || sc.CreatedBy != "cos" {
		t.Fatalf("create scout %+v, %v", sc, err)
	}
	if def := mustCreate(t, s, "ship", "", 3); def.Type != ticket.TypeShip || def.Owner != "cos" {
		t.Fatalf("defaults %+v", def)
	}
	// --next never hands out backlog, even when it is the most urgent.
	got, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1"})
	if err != nil || got.Title != "ship" {
		t.Fatalf("claim --next %+v, %v", got, err)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s2", Statuses: []ticket.Status{ticket.StatusBacklog}})
	wantErr(t, err, ticket.ErrInvalid)
	// Grooming claims by id.
	g, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: sc.ID, Holder: "cos/s1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: sc.ID, To: ticket.StatusInProgress, Holder: "cos/s1", Token: g.Claim.Token})
	wantErr(t, err, ticket.ErrInvalidTransition)
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: sc.ID, To: ticket.StatusTodo, Holder: "cos/s1", Token: g.Claim.Token, Release: true}); err != nil {
		t.Fatal(err)
	}
	// A scout ends at in_review, and only its owner moves it on to done.
	move(t, s, sc.ID, ticket.StatusInProgress)
	move(t, s, sc.ID, ticket.StatusInReview)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: sc.ID, To: ticket.StatusDone, ActorRole: ticket.RoleReviewer})
	wantErr(t, err, ticket.ErrForbidden)
	evs, _ := s.Events(ctx, ticket.EventFilter{TicketID: sc.ID, Type: ticket.EventCreated})
	var d map[string]any
	json.Unmarshal(evs[0].Data, &d)
	if d["type"] != "scout" || d["owner"] != "chief-of-staff" {
		t.Fatalf("created event %v", d)
	}
}

// A session whose lease expired could comment while another held
// the lease, and transition once the new holder released. Both are fenced.
func TestStaleSessionIsFenced(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	a, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/a", TTL: time.Minute})
	clk.Advance(2 * time.Minute)
	b, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/b"})
	if err != nil || b.Claim.Token != a.Claim.Token+1 {
		t.Fatalf("B's claim %+v, %v", b.Claim, err)
	}

	// While B holds it: A can't comment or move it, with or without its token.
	_, err = s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "late", Holder: "jse/a", Token: a.Claim.Token})
	wantErr(t, err, ticket.ErrClaimed)
	_, err = s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "late"})
	wantErr(t, err, ticket.ErrClaimed)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress, Holder: "jse/a", Token: a.Claim.Token})
	wantErr(t, err, ticket.ErrClaimed)
	// B's token from A's lease is still a stale token.
	_, err = s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "x", Holder: "jse/b", Token: a.Claim.Token})
	wantErr(t, err, ticket.ErrLeaseLost)
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "B's note", Holder: "jse/b", Token: b.Claim.Token}); err != nil {
		t.Fatal(err)
	}

	// After B releases, nobody holds it, and A is still fenced.
	if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/b", Token: b.Claim.Token}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress, Holder: "jse/a", Token: a.Claim.Token})
	wantErr(t, err, ticket.ErrLeaseLost)
	_, err = s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "late", Holder: "jse/a", Token: a.Claim.Token})
	wantErr(t, err, ticket.ErrLeaseLost)

	d, _ := s.Get(ctx, tk.ID)
	if d.Status != ticket.StatusTodo || len(d.Comments) != 1 || d.Comments[0].Author != "jse" {
		t.Fatalf("stale writes landed: %+v", d)
	}
}

// A holder that re-claims its own lease gets a new token; a leftover process
// with the old token is fenced, including from releasing the new lease.
func TestReclaimFencesOldToken(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	old, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1"})
	cur, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s1"})
	if err != nil || cur.Claim.Token != old.Claim.Token+1 || cur.Version != old.Version+1 {
		t.Fatalf("re-claim %+v, %v", cur, err)
	}
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress, Holder: "jse/s1", Token: old.Claim.Token})
	wantErr(t, err, ticket.ErrLeaseLost)
	_, err = s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "x", Holder: "jse/s1", Token: old.Claim.Token})
	wantErr(t, err, ticket.ErrLeaseLost)
	_, err = s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s1", Token: old.Claim.Token})
	wantErr(t, err, ticket.ErrLeaseLost)
	if got, _ := s.Get(ctx, tk.ID); got.LiveClaim() == nil || got.Claim.Token != cur.Claim.Token {
		t.Fatalf("stale release dropped the new lease: %+v", got.Claim)
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress, Holder: "jse/s1", Token: cur.Claim.Token}); err != nil {
		t.Fatal(err)
	}
	// Tokens keep growing across releases.
	if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s1", Token: cur.Claim.Token}); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s2"})
	if err != nil || next.Claim.Token != cur.Claim.Token+1 {
		t.Fatalf("token after release %+v, %v", next.Claim, err)
	}
}

// Replaying a claim key after the lease was lost reported success
// with the old holder while the store said someone else held it.
func TestClaimReplayAfterLeaseLoss(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	req := ticket.ClaimRequest{Holder: "jse/a", TTL: time.Minute, IdempotencyKey: "a:claim"}
	first, err := s.ClaimNext(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	// While the lease is live, the retry replays, with the ticket as it is
	// now: a session acting on a claim-time snapshot would fail its next
	// --from or --expect-version write.
	if again, err := s.ClaimNext(ctx, req); err != nil || again.Claim.Token != first.Claim.Token {
		t.Fatalf("live replay %+v, %v", again, err)
	}
	moved, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress, Holder: "jse/a", Token: first.Claim.Token})
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(10 * time.Second)
	beat, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/a", Token: first.Claim.Token, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.ClaimNext(ctx, req)
	if err != nil || again.Status != ticket.StatusInProgress || again.Version != moved.Version ||
		!again.Claim.ExpiresAt.Equal(beat.Claim.ExpiresAt) || again.Claim.Token != first.Claim.Token {
		t.Fatalf("live replay %+v, %v; want status in_progress, version %d, expiry %s", again, err, moved.Version, beat.Claim.ExpiresAt)
	}
	clk.Advance(2 * time.Minute)
	// Expired, nobody else yet: still lost.
	_, err = s.ClaimNext(ctx, req)
	wantErr(t, err, ticket.ErrLeaseLost)
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/b"}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.LastSeq(ctx)
	_, err = s.ClaimNext(ctx, req)
	wantErr(t, err, ticket.ErrLeaseLost)
	if after, _ := s.LastSeq(ctx); after != before {
		t.Fatal("failed replay wrote an event")
	}
	if got, _ := s.Get(ctx, tk.ID); got.Claim.Holder != "jse/b" {
		t.Fatalf("holder %s; want jse/b", got.Claim.Holder)
	}

	// Same holder, superseded by its own re-claim, or released: lost too.
	tk2 := mustCreate(t, s, "y", "", 0)
	r2 := ticket.ClaimRequest{ID: tk2.ID, Holder: "jse/a", IdempotencyKey: "a:claim2"}
	if _, err := s.ClaimNext(ctx, r2); err != nil {
		t.Fatal(err)
	}
	s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk2.ID, Holder: "jse/a"})
	_, err = s.ClaimNext(ctx, r2)
	wantErr(t, err, ticket.ErrLeaseLost)
	tk3 := mustCreate(t, s, "z", "", 0)
	r3 := ticket.ClaimRequest{ID: tk3.ID, Holder: "jse/a", IdempotencyKey: "a:claim3"}
	c3, err := s.ClaimNext(ctx, r3)
	if err != nil {
		t.Fatal(err)
	}
	s.Release(ctx, ticket.ReleaseRequest{ID: tk3.ID, Holder: "jse/a", Token: c3.Claim.Token})
	_, err = s.ClaimNext(ctx, r3)
	wantErr(t, err, ticket.ErrLeaseLost)
}

// An engineer once moved an unclaimed ticket to in_progress, then straight
// to done. Ship tickets reach done only from in_review, and only by the
// reviewer.
func TestReviewerOrPlannerSetsShipDone(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "engineer", 2)
	move(t, s, tk.ID, ticket.StatusInProgress)
	_, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusDone, Actor: "eng", ActorRole: "engineer"})
	wantErr(t, err, ticket.ErrInvalidTransition)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusDone, Actor: "rev", ActorRole: ticket.RoleReviewer})
	wantErr(t, err, ticket.ErrInvalidTransition) // not even the reviewer skips review

	move(t, s, tk.ID, ticket.StatusInReview)
	before, _ := s.LastSeq(ctx)
	for _, role := range []string{"", "chief-of-staff", "engineer", "qa-lead"} {
		_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusDone, Actor: "x", ActorRole: role})
		wantErr(t, err, ticket.ErrForbidden)
	}
	if after, _ := s.LastSeq(ctx); after != before {
		t.Fatal("forbidden move wrote an event")
	}
	for _, role := range []string{"Bad Role", "code-reviewer"} {
		_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusDone, ActorRole: role})
		wantErr(t, err, ticket.ErrInvalid)
	}
	done, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, From: ticket.StatusInReview, To: ticket.StatusDone, Actor: "rev", ActorRole: ticket.RoleReviewer})
	if err != nil || done.Status != ticket.StatusDone {
		t.Fatalf("reviewer done %+v, %v", done, err)
	}
	// The log records the role the closer declared.
	evs, _ := s.Events(ctx, ticket.EventFilter{TicketID: tk.ID, Type: ticket.EventTransitioned})
	var data map[string]any
	json.Unmarshal(evs[len(evs)-1].Data, &data)
	if data["to"] != "done" || data["actor_role"] != ticket.RoleReviewer {
		t.Fatalf("done event data %v; want actor_role %s", data, ticket.RoleReviewer)
	}

	// The planner closes a ticket whose PR merged outside a review.
	tk2 := mustCreate(t, s, "y", "engineer", 2)
	move(t, s, tk2.ID, ticket.StatusInProgress)
	move(t, s, tk2.ID, ticket.StatusInReview)
	if done, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk2.ID, From: ticket.StatusInReview, To: ticket.StatusDone, Actor: "plan", ActorRole: ticket.RolePlanner}); err != nil || done.Status != ticket.StatusDone {
		t.Fatalf("planner done %+v, %v", done, err)
	}
}

// A scout ticket's owner sets it done from in_review; the reviewer can't.
func TestScoutDoneByOwner(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p := 2
	sc, err := s.Create(ctx, ticket.CreateRequest{Title: "scout", Type: ticket.TypeScout, Priority: &p, Actor: "cos", Owner: "chief-of-staff"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.ClaimNext(ctx, ticket.ClaimRequest{ID: sc.ID, Holder: "sse/s1"})
	tok := c.Claim.Token
	for _, to := range []ticket.Status{ticket.StatusInProgress, ticket.StatusInReview} {
		if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: sc.ID, To: to, Holder: "sse/s1", Token: tok}); err != nil {
			t.Fatal(err)
		}
	}
	// The engineer who wrote the report can't close it, even claiming reviewer.
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: sc.ID, To: ticket.StatusDone, Holder: "sse/s1", Token: tok, ActorRole: ticket.RoleReviewer})
	wantErr(t, err, ticket.ErrForbidden)
	s.Release(ctx, ticket.ReleaseRequest{ID: sc.ID, Holder: "sse/s1", Token: tok})
	for _, actor := range []string{"ops", "cos", ""} {
		_, err = s.Transition(ctx, ticket.TransitionRequest{ID: sc.ID, To: ticket.StatusDone, Actor: actor, ActorRole: ticket.RoleReviewer})
		wantErr(t, err, ticket.ErrForbidden)
	}
	done, err := s.Transition(ctx, ticket.TransitionRequest{ID: sc.ID, From: ticket.StatusInReview, To: ticket.StatusDone, Actor: "chief-of-staff"})
	if err != nil || done.Status != ticket.StatusDone {
		t.Fatalf("owner done %+v, %v", done, err)
	}
}

// An expired lease must log lease_expired without waiting for
// someone to re-claim. The reaper frees it, logs it and counts it.
func TestExpiredLeaseLogsEvent(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	c, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/crashed", TTL: time.Minute})
	before, _ := s.LastSeq(ctx)
	clk.Advance(time.Hour)
	res, err := s.Reap(ctx)
	if err != nil || len(res.Reaped) != 1 || res.Reaped[0] != tk.ID {
		t.Fatalf("reap %+v, %v", res, err)
	}
	evs, _ := s.Events(ctx, ticket.EventFilter{Since: before, Type: ticket.EventLeaseExpired})
	if len(evs) != 1 {
		t.Fatalf("lease_expired events = %d; want 1", len(evs))
	}
	var data map[string]any
	json.Unmarshal(evs[0].Data, &data)
	if evs[0].Actor != ticket.SystemActor || data["holder"] != "jse/crashed" || data["token"] != float64(c.Claim.Token) {
		t.Fatalf("event actor %q data %v", evs[0].Actor, data)
	}
	got, _ := s.Get(ctx, tk.ID)
	if got.Claim != nil || got.LeaseExpiries != 1 || got.Version != c.Version {
		t.Fatalf("after reap: claim %+v expiries %d version %d (want nil, 1, %d)", got.Claim, got.LeaseExpiries, got.Version, c.Version)
	}
	// Nothing left to reap, and the ticket is claimable with a newer token.
	if res, _ := s.Reap(ctx); len(res.Reaped) != 0 {
		t.Fatalf("second reap %+v", res)
	}
	next, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/fresh"})
	if err != nil || next.Claim.Token != c.Claim.Token+1 {
		t.Fatalf("claim after reap %+v, %v", next.Claim, err)
	}
	if evs, _ := s.Events(ctx, ticket.EventFilter{Since: before, Type: ticket.EventLeaseExpired}); len(evs) != 1 {
		t.Fatalf("claim after reap logged another lease_expired: %d", len(evs))
	}
}

// Without an injected clock, lease times come from the database's clock.
func TestDatabaseClock(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "tickets.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustCreate(t, s, "x", "", 2)
	before := time.Now().Add(-time.Second)
	c, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", TTL: time.Minute})
	after := time.Now().Add(time.Second)
	if err != nil || c.Claim.ExpiresAt.Before(before.Add(time.Minute)) || c.Claim.ExpiresAt.After(after.Add(time.Minute)) || c.Claim.Expired {
		t.Fatalf("claim %+v, %v; want expiry about a minute from now", c.Claim, err)
	}
	var ms int64
	if ms, err = s.clock(ctx, s.r); err != nil || ms < before.UnixMilli() || ms > after.UnixMilli() {
		t.Fatalf("database clock %d, %v", ms, err)
	}
}

func TestCheckpoint(t *testing.T) {
	s, _ := newStore(t)
	for i := 0; i < 50; i++ {
		mustCreate(t, s, fmt.Sprint(i), "", 2)
	}
	frames, done, err := s.Checkpoint(context.Background())
	if err != nil || frames == 0 || done != frames {
		t.Fatalf("checkpoint frames %d done %d, %v", frames, done, err)
	}
}
