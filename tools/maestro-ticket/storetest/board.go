package storetest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// Rich is what FillBoard wrote that a move must carry over as it is.
type Rich struct {
	// Key made Keyed with a create; a retry with it replays Keyed.
	Key   string
	Keyed ticket.Ticket
	// Create is the request Key was sent with.
	Create ticket.CreateRequest
	// Launches is the last launch id given out.
	Launches int64
}

// FillBoard writes a board with every kind of row a move copies: deps,
// areas, a split, parked and blocked tickets, short and long comments,
// an idempotent create, claims released and run out (so tokens stay
// with no lease), a revived lease, runs, ended launches and a met ETA. No
// claim is left live. clk is the store's clock.
func FillBoard(t *testing.T, s ticket.Store, clk *Clock) Rich {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	prio := func(n int) *int { return &n }
	mk := func(req ticket.CreateRequest) ticket.Ticket {
		t.Helper()
		if req.Actor == "" {
			req.Actor = "planner"
		}
		tk, err := s.Create(ctx, req)
		must(err)
		return tk
	}
	a := mk(ticket.CreateRequest{Title: "base", Body: "the <first> & only", Role: "engineer", Priority: prio(1), Areas: []string{"tools/b", "tools/a"}, Goal: "move", Tier: "junior"})
	b := mk(ticket.CreateRequest{Title: "needs base", Role: "engineer", DependsOn: []ticket.ID{a.ID}, Goal: "move"})
	mk(ticket.CreateRequest{Title: "later", Type: ticket.TypeScout, Status: ticket.StatusBacklog, Parked: ticket.ParkedUnready})
	split := mk(ticket.CreateRequest{Title: "split of needs base", SplitFrom: b.ID, Role: "engineer"})
	r := Rich{Key: "fill-create-1", Create: ticket.CreateRequest{Title: "keyed", Role: "reviewer", Actor: "planner", IdempotencyKey: "fill-create-1"}}
	r.Keyed = mk(r.Create)
	r.Keyed.Claim = nil

	_, err := s.Comment(ctx, ticket.CommentRequest{ID: a.ID, Body: "Handoff: short", Actor: "jse"})
	must(err)
	_, err = s.Comment(ctx, ticket.CommentRequest{ID: a.ID, Body: "Review: GO " + strings.Repeat("é long note ", 12), Actor: "rev"})
	must(err)
	_, err = s.Comment(ctx, ticket.CommentRequest{ID: split.ID, Body: "Kickback: x", Actor: "rev", IdempotencyKey: "fill-comment"})
	must(err)

	// A claim released: the token stays, the lease goes.
	c, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: a.ID, Holder: "jse/s1", TTL: 10 * time.Minute, Actor: "jse", IdempotencyKey: "fill-claim"})
	must(err)
	_, err = s.Release(ctx, ticket.ReleaseRequest{ID: a.ID, Holder: "jse/s1", Token: c.Claim.Token, Actor: "jse"})
	must(err)
	// A lease that ran out and came back, then was released.
	c, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: a.ID, Holder: "jse/s2", TTL: time.Minute, Actor: "jse"})
	must(err)
	clk.Advance(2 * time.Minute)
	_, err = s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: a.ID, Holder: "jse/s2", Token: c.Claim.Token, TTL: time.Minute})
	must(err)
	_, err = s.Release(ctx, ticket.ReleaseRequest{ID: a.ID, Holder: "jse/s2", Token: c.Claim.Token, Actor: "jse"})
	must(err)
	// A lease that ran out and was reaped.
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: split.ID, Holder: "jse/s3", TTL: time.Minute, Actor: "jse"})
	must(err)
	clk.Advance(2 * time.Minute)
	_, err = s.Reap(ctx)
	must(err)
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: b.ID, To: ticket.StatusBlocked, Note: "waits", Actor: "planner"})
	must(err)

	_, err = s.LogRun(ctx, ticket.LogRunRequest{Session: "sess-b", Ticket: a.ID, Actor: "jse", CostUSD: 1.25, InputTokens: 10, OutputTokens: 20, CacheWriteTokens: 3, CacheReadTokens: 4})
	must(err)
	_, err = s.LogRun(ctx, ticket.LogRunRequest{Session: "sess-a", Actor: "planner", CostUSD: 0.1})
	must(err)
	for i, tk := range []ticket.ID{a.ID, 0} {
		l, err := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "jse", Ticket: tk, PID: 100 + i, Stamp: "boot|ns|1"})
		must(err)
		_, err = s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: l.ID, Outcome: ticket.LaunchResult, Session: "sess-b"})
		must(err)
		r.Launches = l.ID
	}
	due := clk.Now().Add(48 * time.Hour)
	_, err = s.SetETA(ctx, ticket.SetETARequest{Goal: "move", Due: due, Tickets: []ticket.ID{b.ID, a.ID}, Actor: "cos"})
	must(err)
	etas, err := s.ETAs(ctx)
	must(err)
	must(s.MarkETAMet(ctx, "move", due, etas[0].SetAt))
	clk.Advance(time.Second)
	return r
}
