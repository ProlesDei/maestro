package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func eventTypes(t *testing.T, s *Store, since int64) []string {
	t.Helper()
	evs, err := s.Events(context.Background(), ticket.EventFilter{Since: since})
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	return types
}

// A write by someone who holds no lease used to clear an expired
// lease silently (a terminal move logged it as "released"), or leave it
// unreported. Every write now logs lease_expired first.
func TestUnclaimedWritesLogExpiredLease(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	a := mustCreate(t, s, "cancel me", "", 2)
	b := mustCreate(t, s, "comment on me", "", 2)
	ca, _ := s.ClaimNext(ctx, ticket.ClaimRequest{ID: a.ID, Holder: "jse/a", TTL: time.Minute})
	cb, _ := s.ClaimNext(ctx, ticket.ClaimRequest{ID: b.ID, Holder: "jse/b", TTL: time.Minute})
	clk.Advance(2 * time.Minute)

	before, _ := s.LastSeq(ctx)
	// The chief of staff read version N while the lease showed as expired; its
	// --expect-version N still holds after the store logs the expiry.
	got, err := s.Transition(ctx, ticket.TransitionRequest{ID: a.ID, To: ticket.StatusCancelled, ExpectVersion: ca.Version, Actor: "cos"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Claim != nil || got.LeaseExpiries != 1 {
		t.Fatalf("after cancel: claim %+v expiries %d", got.Claim, got.LeaseExpiries)
	}
	if types := eventTypes(t, s, before); fmt.Sprint(types) != "[lease_expired transitioned]" {
		t.Fatalf("events %v; want [lease_expired transitioned]", types)
	}
	evs, _ := s.Events(ctx, ticket.EventFilter{Since: before, Type: ticket.EventTransitioned})
	var data map[string]any
	json.Unmarshal(evs[0].Data, &data)
	if _, ok := data["released"]; ok {
		t.Fatalf("transition claims to release a lease that had expired: %v", data)
	}

	before, _ = s.LastSeq(ctx)
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: b.ID, Body: "status?", Actor: "cos"}); err != nil {
		t.Fatal(err)
	}
	if types := eventTypes(t, s, before); fmt.Sprint(types) != "[lease_expired commented]" {
		t.Fatalf("events %v; want [lease_expired commented]", types)
	}
	if got, _ := s.Get(ctx, b.ID); got.Claim != nil || got.Version != cb.Version {
		t.Fatalf("after comment: claim %+v version %d", got.Claim, got.Version)
	}

	// A failed write logs nothing, expiry included: the reaper catches it.
	c := mustCreate(t, s, "fails", "", 2)
	s.ClaimNext(ctx, ticket.ClaimRequest{ID: c.ID, Holder: "jse/c", TTL: time.Minute})
	clk.Advance(2 * time.Minute)
	before, _ = s.LastSeq(ctx)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: c.ID, To: ticket.StatusDone})
	wantErr(t, err, ticket.ErrInvalidTransition)
	if after, _ := s.LastSeq(ctx); after != before {
		t.Fatal("failed write logged an event")
	}
}

// Expiries are counted per ticket, across sessions.
func TestLeaseExpiriesCount(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "flaky", "", 2)
	for i := 1; i <= 3; i++ {
		if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: fmt.Sprintf("jse/s%d", i), TTL: time.Minute}); err != nil {
			t.Fatal(err)
		}
		clk.Advance(2 * time.Minute)
	}
	if _, err := s.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, tk.ID)
	if got.LeaseExpiries != 3 {
		t.Fatalf("lease_expiries %d; want 3", got.LeaseExpiries)
	}
	evs, _ := s.Events(ctx, ticket.EventFilter{Type: ticket.EventLeaseExpired})
	var data map[string]any
	json.Unmarshal(evs[2].Data, &data)
	if len(evs) != 3 || data["expiries"] != float64(3) || data["holder"] != "jse/s3" {
		t.Fatalf("%d lease_expired events, last %v", len(evs), data)
	}
}

// One reap call frees every expired lease, in batches of reapBatch, and
// leaves live leases alone.
func TestReapBatches(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	n := reapBatch*2 + 7
	for i := 0; i < n; i++ {
		mustCreate(t, s, fmt.Sprint("t", i), "", 2)
		if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: fmt.Sprintf("jse/s%d", i), TTL: time.Minute}); err != nil {
			t.Fatal(err)
		}
	}
	live := mustCreate(t, s, "live", "", 2)
	s.ClaimNext(ctx, ticket.ClaimRequest{ID: live.ID, Holder: "jse/live", TTL: time.Hour})
	clk.Advance(2 * time.Minute)
	res, err := s.Reap(ctx)
	if err != nil || len(res.Reaped) != n {
		t.Fatalf("reaped %d, %v; want %d", len(res.Reaped), err, n)
	}
	if got, _ := s.Get(ctx, live.ID); got.LiveClaim() == nil {
		t.Fatal("reap freed a live lease")
	}
	if evs, _ := s.Events(ctx, ticket.EventFilter{Type: ticket.EventLeaseExpired}); len(evs) != n {
		t.Fatalf("lease_expired events %d; want %d", len(evs), n)
	}
}

// A lease is expired at its exact expiry millisecond, not after.
func TestLeaseExpiresAtExactMillisecond(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	c, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", TTL: time.Minute})
	clk.Advance(time.Minute - time.Millisecond)
	if got, _ := s.Get(ctx, tk.ID); got.LiveClaim() == nil {
		t.Fatal("lease expired a millisecond early")
	}
	if res, _ := s.Reap(ctx); len(res.Reaped) != 0 {
		t.Fatal("reaped a live lease")
	}
	clk.Advance(time.Millisecond)
	if got, _ := s.Get(ctx, tk.ID); got.LiveClaim() != nil || !got.Claim.ExpiresAt.Equal(clk.Now()) {
		t.Fatalf("lease still live at its expiry: %+v", got.Claim)
	}
	_, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/s1", Token: c.Claim.Token})
	wantErr(t, err, ticket.ErrLeaseLost)
	if res, _ := s.Reap(ctx); len(res.Reaped) != 1 {
		t.Fatal("lease not reaped at its expiry")
	}
}
