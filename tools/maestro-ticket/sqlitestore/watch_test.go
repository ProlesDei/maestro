package sqlitestore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// T-181's review lease ran out twice and came back with no word: a hung
// session looked healthy. The claim now counts its revivals.
func TestClaimCountsRevivals(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	a := mustCreate(t, s, "review me", "", 2)
	c, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: a.ID, Holder: "reviewer/x", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	hb := ticket.HeartbeatRequest{ID: a.ID, Holder: "reviewer/x", Token: c.Claim.Token, TTL: time.Minute}
	if got, _ := s.Heartbeat(ctx, hb); got.Claim.Revivals != 0 {
		t.Fatalf("a heartbeat in time counted as a revival: %+v", got.Claim)
	}
	for want := 1; want <= 2; want++ {
		clk.Advance(2 * time.Minute)
		got, err := s.Heartbeat(ctx, hb)
		if err != nil {
			t.Fatal(err)
		}
		if got.Claim.Revivals != want {
			t.Fatalf("revivals %d; want %d", got.Claim.Revivals, want)
		}
	}
	list, _ := s.List(ctx, ticket.ListFilter{})
	if list[0].Claim.Revivals != 2 {
		t.Fatalf("list shows %+v", list[0].Claim)
	}
	// A new claim is a new lease: its count starts again.
	c, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: a.ID, Holder: "reviewer/x", TTL: time.Minute})
	if err != nil || c.Claim.Revivals != 0 {
		t.Fatalf("re-claim: %v %+v", err, c.Claim)
	}
}

func TestLaunchesEndOnce(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	a := mustCreate(t, s, "build me", "", 2)
	if _, err := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "x", Ticket: 99, PID: 5}); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("unknown ticket: %v", err)
	}
	l, err := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "senior-engineer", Ticket: a.ID, PID: 42, Stamp: "b|n|7"})
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Minute)
	m, _ := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "planner", PID: 43})
	open, _ := s.OpenLaunches(ctx)
	if len(open) != 2 || open[0].ID != l.ID || open[0].Ticket != a.ID || open[0].Stamp != "b|n|7" || open[1].Ticket != 0 {
		t.Fatalf("open = %+v", open)
	}
	if ok, err := s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: l.ID, Outcome: ticket.LaunchDied}); !ok || err != nil {
		t.Fatalf("end: %v %v", ok, err)
	}
	// A second look that found the same dead run counts nothing.
	if ok, err := s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: l.ID, Outcome: ticket.LaunchResult, Session: "s"}); ok || err != nil {
		t.Fatalf("second end: %v %v", ok, err)
	}
	if _, err := s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: m.ID, Outcome: "fine"}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("bad outcome: %v", err)
	}
	open, _ = s.OpenLaunches(ctx)
	if len(open) != 1 || open[0].ID != m.ID {
		t.Fatalf("open = %+v", open)
	}
	exp, _ := s.Export(ctx)
	if len(exp.Launches) != 2 || exp.Launches[0].Outcome != ticket.LaunchDied || exp.Launches[0].EndedAt.IsZero() {
		t.Fatalf("export = %+v", exp.Launches)
	}
}

func TestETAs(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	a := mustCreate(t, s, "demo", "", 2)
	g := "demo"
	if _, err := s.Update(ctx, ticket.UpdateRequest{ID: a.ID, Goal: &g}); err != nil {
		t.Fatal(err)
	}
	due := clk.Now().Add(time.Hour)
	if _, err := s.SetETA(ctx, ticket.SetETARequest{Goal: "dmeo", Due: due}); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("a goal no ticket has: %v", err)
	}
	if _, err := s.SetETA(ctx, ticket.SetETARequest{Goal: "demo", Due: due, Tickets: []ticket.ID{a.ID, 9}}); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("unknown ticket: %v", err)
	}
	if _, err := s.SetETA(ctx, ticket.SetETARequest{Goal: "Bad Goal", Due: due}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("bad goal: %v", err)
	}
	if _, err := s.SetETA(ctx, ticket.SetETARequest{Goal: "demo", Due: due, Tickets: []ticket.ID{a.ID}, Actor: "planner"}); err != nil {
		t.Fatal(err)
	}
	// A slip replaces the ETA; it never adds a second.
	later := due.Add(time.Hour)
	e, err := s.SetETA(ctx, ticket.SetETARequest{Goal: "demo", Due: later, Actor: "planner"})
	if err != nil || !e.Due.Equal(later) || len(e.Tickets) != 0 || e.SetBy != "planner" {
		t.Fatalf("replace: %v %+v", err, e)
	}
	b := mustCreate(t, s, "alpha", "", 2)
	g = "alpha"
	if _, err := s.Update(ctx, ticket.UpdateRequest{ID: b.ID, Goal: &g}); err != nil {
		t.Fatal(err)
	}
	mustSetETA(t, s, "alpha", due)
	list, _ := s.ETAs(ctx)
	if len(list) != 2 || list[0].Goal != "alpha" || list[1].Goal != "demo" {
		t.Fatalf("etas = %+v", list)
	}
	// A look marks met only the ETA it judged: not a replacement set
	// meanwhile, even one due at the same time.
	judged := list[0]
	clk.Advance(time.Second)
	mustSetETA(t, s, "alpha", due)
	if err := s.MarkETAMet(ctx, judged.Goal, judged.Due, judged.SetAt); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.ETAs(ctx); !list[0].MetAt.IsZero() {
		t.Fatalf("a replaced ETA was marked met: %+v", list[0])
	}
	if err := s.MarkETAMet(ctx, list[0].Goal, list[0].Due, list[0].SetAt); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.ETAs(ctx); list[0].MetAt.IsZero() {
		t.Fatalf("the judged ETA wasn't marked met: %+v", list[0])
	}
	if err := s.ClearETA(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearETA(ctx, "alpha"); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("clear twice: %v", err)
	}
}

func mustSetETA(t *testing.T, s *Store, goal string, due time.Time) {
	t.Helper()
	if _, err := s.SetETA(context.Background(), ticket.SetETARequest{Goal: goal, Due: due}); err != nil {
		t.Fatal(err)
	}
}

// T-123 and T-167 sat in review for 3 hours with no one on them. Doctor
// names a handoff that waits past 20 minutes, and still passes.
func TestDoctorFlagsStalls(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	a := mustCreate(t, s, "handed off", "", 2)
	b := mustCreate(t, s, "under review", "", 2)
	for _, id := range []ticket.ID{a.ID, b.ID} {
		for _, to := range []ticket.Status{ticket.StatusInProgress, ticket.StatusInReview} {
			if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: id, To: to, Actor: "x"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: b.ID, Statuses: []ticket.Status{ticket.StatusInReview}, Holder: "reviewer/r", TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	stalls := func() ticket.Check {
		rep, err := s.Doctor(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range rep.Checks {
			if c.Name == "stalls" {
				return c
			}
		}
		t.Fatal("no stalls check")
		return ticket.Check{}
	}
	if c := stalls(); !c.OK || strings.Contains(c.Detail, "T-") {
		t.Fatalf("fresh handoff: %+v", c)
	}
	clk.Advance(21 * time.Minute)
	if c := stalls(); !c.OK || !strings.Contains(c.Detail, "T-1") || strings.Contains(c.Detail, "T-2") {
		t.Fatalf("after 21m: %+v", c)
	}
}
