package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/at-ticket/ticket"
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
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "agent-team", "tickets.db"), Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, clk
}

func mustCreate(t *testing.T, s *Store, title, role string, prio int) ticket.Ticket {
	t.Helper()
	tk, err := s.Create(context.Background(), ticket.CreateRequest{Title: title, Role: role, Priority: &prio, Actor: "planner"})
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
	a := mustCreate(t, s, "a", "junior-software-engineer", 2)
	b := mustCreate(t, s, "b", "senior-software-engineer", 0)
	if a.ID != 1 || b.ID != 2 || a.Version != 1 || a.Status != ticket.StatusTodo || a.CreatedBy != "planner" {
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
	jse, _ := s.List(ctx, ticket.ListFilter{Role: "junior-software-engineer"})
	if len(jse) != 1 || jse[0].ID != a.ID {
		t.Fatalf("role filter: %+v", jse)
	}
	none, _ := s.List(ctx, ticket.ListFilter{Statuses: []ticket.Status{ticket.StatusDone}})
	if len(none) != 0 {
		t.Fatalf("status filter: %+v", none)
	}
}

func TestClaimNextOrderAndFilters(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	mustCreate(t, s, "low", "junior-software-engineer", 3)
	urgent := mustCreate(t, s, "urgent", "junior-software-engineer", 0)
	mustCreate(t, s, "senior", "senior-software-engineer", 0)

	got, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", Role: "junior-software-engineer"})
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
	got2, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s2", Role: "junior-software-engineer"})
	if err != nil || got2.Title != "low" {
		t.Fatalf("second claim %+v, %v", got2, err)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s3", Role: "junior-software-engineer"})
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
	tk := mustCreate(t, s, "x", "junior-software-engineer", 2)

	got, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s1"})
	if err != nil || got.Claim.Holder != "jse/s1" {
		t.Fatalf("claim by id %+v, %v", got, err)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "sse/s1"})
	wantErr(t, err, ticket.ErrClaimed)

	// Re-claiming your own live lease refreshes it without a new version.
	again, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s1", TTL: time.Hour})
	if err != nil || again.Version != got.Version || !again.Claim.ExpiresAt.After(got.Claim.ExpiresAt) {
		t.Fatalf("refresh %+v, %v", again, err)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s1", Statuses: []ticket.Status{ticket.StatusInQA}})
	wantErr(t, err, ticket.ErrStatusConflict)
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s1", Role: "qa-lead"})
	wantErr(t, err, ticket.ErrStatusConflict)
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: 404, Holder: "jse/s1"})
	wantErr(t, err, ticket.ErrNotFound)
}

func TestLeaseExpiryAndHeartbeat(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/crashed", TTL: 10 * time.Minute}); err != nil {
		t.Fatal(err)
	}

	clk.Advance(9 * time.Minute)
	hb, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/crashed", TTL: 10 * time.Minute})
	if err != nil || !hb.Claim.ExpiresAt.Equal(clk.Now().Add(10*time.Minute)) || hb.Version != 2 {
		t.Fatalf("heartbeat %+v, %v", hb, err)
	}
	_, err = s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/other"})
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
	if err != nil || taken.Claim.Holder != "jse/fresh" || taken.Claim.Expired {
		t.Fatalf("expired lease should be claimable: %+v, %v", taken, err)
	}
	_, err = s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/crashed"})
	wantErr(t, err, ticket.ErrLeaseLost)

	evs, _ := s.Events(ctx, ticket.EventFilter{TicketID: tk.ID})
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	want := []string{"created", "claimed", "lease_expired", "claimed"}
	if len(types) != len(want) {
		t.Fatalf("events %v; want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("events %v; want %v", types, want)
		}
	}
}

func TestHeartbeatAfterExpiryFails(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", TTL: time.Minute})
	clk.Advance(2 * time.Minute)
	_, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/s1"})
	wantErr(t, err, ticket.ErrLeaseLost)
}

func TestRelease(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1"})

	_, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s2"})
	wantErr(t, err, ticket.ErrClaimed)

	r, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s1"})
	if err != nil || r.Claim != nil || r.Version != 3 {
		t.Fatalf("release %+v, %v", r, err)
	}
	// Twice is a no-op.
	r2, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s1"})
	if err != nil || r2.Version != 3 {
		t.Fatalf("second release %+v, %v", r2, err)
	}
	// Someone else's expired lease is not ours to release, and not an error.
	s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s3", TTL: time.Minute})
	clk.Advance(2 * time.Minute)
	if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s1"}); err != nil {
		t.Fatal(err)
	}
	// Our own expired lease is cleared.
	r3, err := s.Release(ctx, ticket.ReleaseRequest{ID: tk.ID, Holder: "jse/s3"})
	if err != nil || r3.Claim != nil {
		t.Fatalf("own expired release %+v, %v", r3, err)
	}
}

func TestTransitionChecks(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "junior-software-engineer", 2)
	c, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1"})

	// Claimed by someone else, or no holder given.
	_, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress, Holder: "jse/s2"})
	wantErr(t, err, ticket.ErrClaimed)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress})
	wantErr(t, err, ticket.ErrClaimed)
	// Wrong from / version / disallowed transition.
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, From: ticket.StatusInQA, To: ticket.StatusInProgress, Holder: "jse/s1"})
	wantErr(t, err, ticket.ErrStatusConflict)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress, Holder: "jse/s1", ExpectVersion: 1})
	wantErr(t, err, ticket.ErrVersionConflict)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusDone, Holder: "jse/s1"})
	wantErr(t, err, ticket.ErrInvalidTransition)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: 404, To: ticket.StatusDone})
	wantErr(t, err, ticket.ErrNotFound)

	got, err := s.Transition(ctx, ticket.TransitionRequest{
		ID: tk.ID, From: ticket.StatusTodo, To: ticket.StatusInProgress, Holder: "jse/s1", ExpectVersion: c.Version,
	})
	if err != nil || got.Status != ticket.StatusInProgress || got.Version != c.Version+1 || got.Claim == nil {
		t.Fatalf("transition %+v, %v", got, err)
	}
	// Escalation: back to todo, reassigned, claim dropped in the same write.
	esc, err := s.Transition(ctx, ticket.TransitionRequest{
		ID: tk.ID, To: ticket.StatusTodo, Holder: "jse/s1", Role: "senior-software-engineer",
		Note: "2 failed tries", Release: true,
	})
	if err != nil || esc.Role != "senior-software-engineer" || esc.Claim != nil {
		t.Fatalf("escalate %+v, %v", esc, err)
	}
	evs, _ := s.Events(ctx, ticket.EventFilter{TicketID: tk.ID, Type: ticket.EventTransitioned})
	var data map[string]any
	json.Unmarshal(evs[len(evs)-1].Data, &data)
	if data["note"] != "2 failed tries" || data["role"] != "senior-software-engineer" || data["released"] != "jse/s1" {
		t.Fatalf("event data %v", data)
	}
}

func TestTerminalTransitionClearsLease(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "cos/s1"})
	got, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusCancelled, Holder: "cos/s1"})
	if err != nil || got.Claim != nil {
		t.Fatalf("cancel %+v, %v", got, err)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "cos/s1"})
	wantErr(t, err, ticket.ErrStatusConflict)
}

func TestFullWorkflow(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "ship it", "senior-software-engineer", 1)
	steps := []struct {
		holder string
		from   ticket.Status
		to     ticket.Status
	}{
		{"sse/s1", ticket.StatusTodo, ticket.StatusInProgress},
		{"sse/s1", ticket.StatusInProgress, ticket.StatusInReview},
		{"cr/s1", ticket.StatusInReview, ticket.StatusInProgress}, // changes requested
		{"sse/s2", ticket.StatusInProgress, ticket.StatusInReview},
		{"cr/s2", ticket.StatusInReview, ticket.StatusInQA},
		{"qa/s1", ticket.StatusInQA, ticket.StatusReadyToRelease},
		{"ops/s1", ticket.StatusReadyToRelease, ticket.StatusDone},
	}
	for _, st := range steps {
		cur, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: st.holder, Statuses: []ticket.Status{st.from}})
		if err != nil {
			t.Fatalf("%s claim %s: %v", st.holder, st.from, err)
		}
		_, err = s.Transition(ctx, ticket.TransitionRequest{
			ID: cur.ID, From: st.from, To: st.to, Holder: st.holder, ExpectVersion: cur.Version, Release: true,
		})
		if err != nil {
			t.Fatalf("%s %s->%s: %v", st.holder, st.from, st.to, err)
		}
	}
	final, _ := s.Get(ctx, tk.ID)
	if final.Status != ticket.StatusDone || final.Claim != nil {
		t.Fatalf("final %+v", final)
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
	_, err = s.Release(ctx, ticket.ReleaseRequest{ID: a.ID, Holder: "jse/s1", IdempotencyKey: "sess:claim"})
	wantErr(t, err, ticket.ErrIdempotencyConflict)

	tr := ticket.TransitionRequest{ID: a.ID, To: ticket.StatusInProgress, Holder: "jse/s1", ExpectVersion: c1.Version, IdempotencyKey: "sess:start"}
	t1, err := s.Transition(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	// Without the key, this retry would fail on version; with it, it replays.
	t2, err := s.Transition(ctx, tr)
	if err != nil || t2.Version != t1.Version {
		t.Fatalf("transition replay %+v, %v", t2, err)
	}

	cm := ticket.CommentRequest{ID: a.ID, Body: "note", IdempotencyKey: "sess:note"}
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
