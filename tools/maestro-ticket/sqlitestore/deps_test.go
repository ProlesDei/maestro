package sqlitestore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func create(t *testing.T, s *Store, req ticket.CreateRequest) ticket.Ticket {
	t.Helper()
	tk, err := s.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func shipToDone(t *testing.T, s *Store, id ticket.ID) {
	t.Helper()
	for _, to := range []ticket.Status{ticket.StatusInProgress, ticket.StatusInReview} {
		move(t, s, id, to)
	}
	if _, err := s.Transition(context.Background(), ticket.TransitionRequest{ID: id, To: ticket.StatusDone, ActorRole: ticket.RoleQALead}); err != nil {
		t.Fatal(err)
	}
}

// ClaimNext only hands out tickets whose dependencies are all done.
func TestClaimWaitsOnDeps(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p0 := 0
	base := mustCreate(t, s, "base", "", 2)
	other := mustCreate(t, s, "other", "", 2)
	// Most urgent, but waiting on base and other.
	top := create(t, s, ticket.CreateRequest{Title: "top", Priority: &p0, DependsOn: []ticket.ID{base.ID, other.ID}})
	if fmt.Sprint(top.DependsOn) != fmt.Sprint([]ticket.ID{base.ID, other.ID}) {
		t.Fatalf("depends_on %v", top.DependsOn)
	}
	got, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/a"})
	if err != nil || got.ID != base.ID {
		t.Fatalf("first claim %s, %v; want %s (top is waiting)", got.ID, err, base.ID)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: top.ID, Holder: "jse/b"})
	wantErr(t, err, ticket.ErrWaitingOnDeps)
	s.Release(ctx, ticket.ReleaseRequest{ID: base.ID, Holder: "jse/a", Token: got.Claim.Token})

	shipToDone(t, s, base.ID)
	move(t, s, other.ID, ticket.StatusBlocked) // only done counts
	got, err = s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/a"})
	wantErr(t, err, ticket.ErrNoWork)
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: top.ID, Holder: "jse/a"})
	wantErr(t, err, ticket.ErrWaitingOnDeps)
	if _, err := s.Update(ctx, ticket.UpdateRequest{ID: top.ID, RemoveDeps: []ticket.ID{other.ID}}); err != nil {
		t.Fatal(err)
	}
	got, err = s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/a"})
	if err != nil || got.ID != top.ID {
		t.Fatalf("claim after deps met %s, %v", got.ID, err)
	}

	// Backlog tickets are claimed by id to groom, whatever they wait on.
	bl := create(t, s, ticket.CreateRequest{Title: "later", Status: ticket.StatusBacklog, DependsOn: []ticket.ID{top.ID}})
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: bl.ID, Holder: "cos/s1"}); err != nil {
		t.Fatalf("grooming claim: %v", err)
	}
}

func TestDepsValidation(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	a := mustCreate(t, s, "a", "", 2)
	b := create(t, s, ticket.CreateRequest{Title: "b", DependsOn: []ticket.ID{a.ID}})
	c := create(t, s, ticket.CreateRequest{Title: "c", DependsOn: []ticket.ID{b.ID}})
	_, err := s.Create(ctx, ticket.CreateRequest{Title: "x", DependsOn: []ticket.ID{99}})
	wantErr(t, err, ticket.ErrNotFound)
	before, _ := s.LastSeq(ctx)
	// a -> c would close the loop a -> c -> b -> a.
	_, err = s.Update(ctx, ticket.UpdateRequest{ID: a.ID, AddDeps: []ticket.ID{c.ID}})
	wantErr(t, err, ticket.ErrInvalid)
	_, err = s.Update(ctx, ticket.UpdateRequest{ID: a.ID, AddDeps: []ticket.ID{a.ID}})
	wantErr(t, err, ticket.ErrInvalid)
	_, err = s.Update(ctx, ticket.UpdateRequest{ID: a.ID})
	wantErr(t, err, ticket.ErrInvalid)
	if after, _ := s.LastSeq(ctx); after != before {
		t.Fatal("failed update wrote an event")
	}
	// Updates are fenced like any write to a claimed ticket.
	cl, _ := s.ClaimNext(ctx, ticket.ClaimRequest{ID: a.ID, Holder: "jse/a"})
	areas := []string{"tools"}
	_, err = s.Update(ctx, ticket.UpdateRequest{ID: a.ID, Areas: &areas})
	wantErr(t, err, ticket.ErrClaimed)
	got, err := s.Update(ctx, ticket.UpdateRequest{ID: a.ID, Areas: &areas, Holder: "jse/a", Token: cl.Claim.Token})
	if err != nil || fmt.Sprint(got.Areas) != "[tools]" || got.Version != cl.Version {
		t.Fatalf("update %+v, %v", got, err)
	}
	evs, _ := s.Events(ctx, ticket.EventFilter{Type: ticket.EventUpdated})
	if len(evs) != 1 {
		t.Fatalf("updated events %d", len(evs))
	}
	_, err = s.Create(ctx, ticket.CreateRequest{Title: "x", Areas: []string{"../etc"}})
	wantErr(t, err, ticket.ErrInvalid)
}

// List with Ready shows what ClaimNext would hand out, in its order: no
// ticket waiting on a dep or a busy area, and none under a live claim.
func TestListReadyMatchesClaimNext(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	ready := func(limit int) string {
		t.Helper()
		l, err := s.List(ctx, ticket.ListFilter{Statuses: []ticket.Status{ticket.StatusTodo}, Ready: true, Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		var ids []ticket.ID
		for _, x := range l {
			ids = append(ids, x.ID)
		}
		return fmt.Sprint(ids)
	}
	p0, p1 := 0, 1
	base := create(t, s, ticket.CreateRequest{Title: "base", Priority: &p1, Areas: []string{"src"}})
	waits := create(t, s, ticket.CreateRequest{Title: "waits", Priority: &p0, DependsOn: []ticket.ID{base.ID}})
	near := create(t, s, ticket.CreateRequest{Title: "near", Priority: &p0, Areas: []string{"src/x"}})
	free := create(t, s, ticket.CreateRequest{Title: "free", Priority: &p1, Areas: []string{"docs"}})
	if got, want := ready(0), fmt.Sprint([]ticket.ID{near.ID, base.ID, free.ID}); got != want {
		t.Fatalf("ready %s; want %s (waits is waiting on base)", got, want)
	}
	cl, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: base.ID, Holder: "jse/a", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	// base is claimed, and its area holds near.
	if got, want := ready(0), fmt.Sprint([]ticket.ID{free.ID}); got != want {
		t.Fatalf("ready with base claimed %s; want %s", got, want)
	}
	cf, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/b", TTL: time.Hour})
	if err != nil || cf.ID != free.ID {
		t.Fatalf("claim next %s, %v; want %s, as ready said", cf.ID, err, free.ID)
	}
	if got := ready(0); got != "[]" {
		t.Fatalf("ready with all claimed %s", got)
	}
	// An expired claim holds nothing.
	clk.Advance(2 * time.Minute)
	if got, want := ready(1), fmt.Sprint([]ticket.ID{near.ID}); got != want {
		t.Fatalf("ready after expiry, limit 1: %s; want %s", got, want)
	}
	// The limit counts after the area check: near is skipped, free shows.
	cl, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: base.ID, Holder: "jse/a", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: free.ID, Holder: "jse/b", Token: cf.Claim.Token}); err != nil {
		t.Fatal(err)
	}
	if got, want := ready(1), fmt.Sprint([]ticket.ID{free.ID}); got != want {
		t.Fatalf("ready, limit 1, near skipped: %s; want %s", got, want)
	}
	if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: base.ID, Holder: "jse/a", Token: cl.Claim.Token}); err != nil {
		t.Fatal(err)
	}
	shipToDone(t, s, base.ID)
	if got, want := ready(0), fmt.Sprint([]ticket.ID{waits.ID, near.ID, free.ID}); got != want {
		t.Fatalf("ready with base done %s; want %s", got, want)
	}
}

// Tickets whose areas overlap never run at the same time.
func TestClaimSkipsBusyAreas(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p0, p1 := 0, 1
	a := create(t, s, ticket.CreateRequest{Title: "store", Priority: &p0, Areas: []string{"tools/maestro-ticket/sqlitestore"}})
	b := create(t, s, ticket.CreateRequest{Title: "whole tool", Priority: &p0, Areas: []string{"tools/maestro-ticket/"}})
	c := create(t, s, ticket.CreateRequest{Title: "sibling", Priority: &p1, Areas: []string{"tools/maestro-ticketing"}})
	d := create(t, s, ticket.CreateRequest{Title: "no areas", Priority: &p1})
	if fmt.Sprint(b.Areas) != "[tools/maestro-ticket]" {
		t.Fatalf("areas not normalized: %v", b.Areas)
	}
	ca, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/1"})
	if err != nil || ca.ID != a.ID {
		t.Fatalf("first %s, %v", ca.ID, err)
	}
	// b contains a's area: skipped. c only shares a name prefix: free.
	cc, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/2"})
	if err != nil || cc.ID != c.ID {
		t.Fatalf("second %s, %v; want %s", cc.ID, err, c.ID)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: b.ID, Holder: "sse/1"})
	wantErr(t, err, ticket.ErrAreaBusy)
	cd, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/3"})
	if err != nil || cd.ID != d.ID {
		t.Fatalf("third %s, %v; want %s", cd.ID, err, d.ID)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/4"})
	wantErr(t, err, ticket.ErrNoWork)
	// A grooming claim on a backlog ticket doesn't hold its areas.
	groom := create(t, s, ticket.CreateRequest{Title: "groom", Status: ticket.StatusBacklog, Areas: []string{"docs"}})
	docs := create(t, s, ticket.CreateRequest{Title: "docs work", Areas: []string{"docs/guide"}})
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: groom.ID, Holder: "cos/s1"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/6"}); err != nil || got.ID != docs.ID {
		t.Fatalf("claim beside a grooming claim %s, %v; want %s", got.ID, err, docs.ID)
	}
	// The holder can renew its own claim.
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: a.ID, Holder: "jse/1"}); err != nil {
		t.Fatalf("renew: %v", err)
	}
	cur, _ := s.Get(ctx, a.ID)
	s.Release(ctx, ticket.ReleaseRequest{ID: a.ID, Holder: "jse/1", Token: cur.Claim.Token})
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: b.ID, Holder: "sse/1"}); err != nil {
		t.Fatalf("after release: %v", err)
	}
	// Now b's area covers a's: a waits.
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/5"})
	wantErr(t, err, ticket.ErrNoWork)
}

// Many claimers at once never run two tickets with overlapping areas.
func TestRaceAreaLocks(t *testing.T) {
	const groups, perGroup, workers = 5, 10, 100
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tickets.db")
	seed := openMany(t, path, 1)[0]
	for g := 0; g < groups; g++ {
		for i := 0; i < perGroup; i++ {
			area := fmt.Sprintf("pkg%d", g)
			if i%2 == 1 {
				area += fmt.Sprintf("/sub%d", i) // inside the group's area
			}
			if _, err := seed.Create(ctx, ticket.CreateRequest{Title: fmt.Sprint(g, "-", i), Areas: []string{area}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	stores := openMany(t, path, workers)
	var mu sync.Mutex
	won := 0
	var other []error
	runAll(workers, func(i int) {
		_, err := stores[i].ClaimNext(ctx, ticket.ClaimRequest{Holder: fmt.Sprintf("jse/s%d", i)})
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			won++
		case !errors.Is(err, ticket.ErrNoWork):
			other = append(other, err)
		}
	})
	if len(other) > 0 {
		t.Fatalf("%d errors, first %v", len(other), other[0])
	}
	claimed := true
	held, _ := seed.List(ctx, ticket.ListFilter{Claimed: &claimed})
	for i, x := range held {
		for _, y := range held[i+1:] {
			if a, b, ok := ticket.AnyAreaOverlaps(x.Areas, y.Areas); ok {
				t.Errorf("%s (%s) and %s (%s) claimed together", x.ID, a, y.ID, b)
			}
		}
	}
	// Sub-areas in a group don't overlap each other, only the root, so the
	// count depends on order; what matters is no overlap and no lost claim.
	if won != len(held) || won < groups {
		t.Fatalf("won %d, held %d", won, len(held))
	}
	t.Logf("%d workers, %d tickets in %d groups: %d claimed, no overlaps", workers, groups*perGroup, groups, won)
}

// A claim on a backlog ticket skips the dependency and area checks, since
// grooming does no work. Its holder can't then move it out of backlog into
// work past them.
func TestBacklogClaimCantStartPastChecks(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	held := create(t, s, ticket.CreateRequest{Title: "held", Areas: []string{"src"}})
	hc, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: held.ID, Holder: "jse/1"})
	if err != nil {
		t.Fatal(err)
	}
	dep := mustCreate(t, s, "dep", "", 2)
	overlap := create(t, s, ticket.CreateRequest{Title: "overlap", Status: ticket.StatusBacklog, Areas: []string{"src/x"}})
	waits := create(t, s, ticket.CreateRequest{Title: "waits", Status: ticket.StatusBacklog, DependsOn: []ticket.ID{dep.ID}})
	free := create(t, s, ticket.CreateRequest{Title: "free", Status: ticket.StatusBacklog, Areas: []string{"docs"}})
	claim := func(id ticket.ID, holder string) ticket.Ticket {
		t.Helper()
		c, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: id, Holder: holder})
		if err != nil {
			t.Fatalf("grooming claim %s: %v", id, err)
		}
		return c
	}
	promote := func(c ticket.Ticket) error {
		_, err := s.Transition(ctx, ticket.TransitionRequest{ID: c.ID, To: ticket.StatusTodo, Holder: c.Claim.Holder, Token: c.Claim.Token})
		return err
	}
	oc := claim(overlap.ID, "jse/2")
	wantErr(t, promote(oc), ticket.ErrAreaBusy)
	wc := claim(waits.ID, "jse/3")
	wantErr(t, promote(wc), ticket.ErrWaitingOnDeps)
	if err := promote(claim(free.ID, "jse/4")); err != nil {
		t.Fatalf("promote with nothing in the way: %v", err)
	}
	// Unclaimed promotion is grooming: allowed, and claim then checks.
	if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: waits.ID, Holder: "jse/3", Token: wc.Claim.Token}); err != nil {
		t.Fatal(err)
	}
	move(t, s, waits.ID, ticket.StatusTodo)
	// Once the area is free, the held ticket can move on.
	if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: held.ID, Holder: "jse/1", Token: hc.Claim.Token}); err != nil {
		t.Fatal(err)
	}
	if err := promote(oc); err != nil {
		t.Fatalf("promote after the area is free: %v", err)
	}
}

// The holder of a working ticket can't widen its areas onto another live
// claim's.
func TestUpdateAreasChecksLiveClaims(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	a := create(t, s, ticket.CreateRequest{Title: "a", Areas: []string{"src"}})
	b := create(t, s, ticket.CreateRequest{Title: "b", Areas: []string{"docs"}})
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: a.ID, Holder: "jse/1"}); err != nil {
		t.Fatal(err)
	}
	bc, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: b.ID, Holder: "jse/2"})
	if err != nil {
		t.Fatal(err)
	}
	widen := []string{"docs", "src/y"}
	_, err = s.Update(ctx, ticket.UpdateRequest{ID: b.ID, Areas: &widen, Holder: "jse/2", Token: bc.Claim.Token})
	wantErr(t, err, ticket.ErrAreaBusy)
	if got, _ := s.Get(ctx, b.ID); fmt.Sprint(got.Areas) != "[docs]" {
		t.Fatalf("areas changed to %v after a refused update", got.Areas)
	}
	other := []string{"docs", "scripts"}
	if _, err := s.Update(ctx, ticket.UpdateRequest{ID: b.ID, Areas: &other, Holder: "jse/2", Token: bc.Claim.Token}); err != nil {
		t.Fatalf("free areas: %v", err)
	}
	// An unclaimed ticket's areas hold nothing yet: claim checks them.
	c := mustCreate(t, s, "c", "", 2)
	mine := []string{"src"}
	if _, err := s.Update(ctx, ticket.UpdateRequest{ID: c.ID, Areas: &mine}); err != nil {
		t.Fatalf("unclaimed update: %v", err)
	}
}

// Only work that writes code (todo, in_progress) waits on dependencies and
// areas, or holds areas. Review and answering an escalation
// neither wait for nor block an engineer on the same code.
func TestOnlyWorkClaimsUseAreasAndDeps(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	work := create(t, s, ticket.CreateRequest{Title: "work", Areas: []string{"src"}})
	esc := create(t, s, ticket.CreateRequest{Title: "escalated", Areas: []string{"src/x"}})
	rev := create(t, s, ticket.CreateRequest{Title: "in review", Areas: []string{"src/y"}})
	dep := mustCreate(t, s, "dep", "", 2)
	waits := create(t, s, ticket.CreateRequest{Title: "waits", DependsOn: []ticket.ID{dep.ID}})
	move(t, s, esc.ID, ticket.StatusBlocked)
	move(t, s, rev.ID, ticket.StatusInProgress)
	move(t, s, rev.ID, ticket.StatusInReview)
	move(t, s, waits.ID, ticket.StatusBlocked)
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: work.ID, Holder: "jse/1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: esc.ID, Holder: "cos/1"}); err != nil {
		t.Fatalf("answering an escalation beside a live claim: %v", err)
	}
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: waits.ID, Holder: "cos/2"}); err != nil {
		t.Fatalf("answering an escalation on a ticket with deps pending: %v", err)
	}
	rc, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "rev/1", Statuses: []ticket.Status{ticket.StatusInReview}})
	if err != nil || rc.ID != rev.ID {
		t.Fatalf("review queue beside a live claim: %s, %v", rc.ID, err)
	}
	// Those claims hold no areas: an engineer can still start beside them.
	if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: work.ID, Holder: "jse/1", Token: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: work.ID, Holder: "jse/2"}); err != nil {
		t.Fatalf("work beside review and escalation claims: %v", err)
	}
}

// Claimed or not, a ticket can't start from todo before its deps are done,
// even a dep added after the claim. Done never waits on deps.
func TestStartWaitsOnDeps(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	base := mustCreate(t, s, "base", "", 2)
	top := create(t, s, ticket.CreateRequest{Title: "top", DependsOn: []ticket.ID{base.ID}})
	_, err := s.Transition(ctx, ticket.TransitionRequest{ID: top.ID, To: ticket.StatusInProgress})
	wantErr(t, err, ticket.ErrWaitingOnDeps)

	held := create(t, s, ticket.CreateRequest{Title: "held"})
	cl, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: held.ID, Holder: "jse/held"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, ticket.UpdateRequest{ID: held.ID, AddDeps: []ticket.ID{base.ID}, Holder: "jse/held", Token: cl.Claim.Token}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Transition(ctx, ticket.TransitionRequest{ID: held.ID, To: ticket.StatusInProgress, Holder: "jse/held", Token: cl.Claim.Token})
	wantErr(t, err, ticket.ErrWaitingOnDeps)

	// Done never waits on deps, even one added while the ticket is in review.
	shipToDone(t, s, base.ID)
	move(t, s, top.ID, ticket.StatusInProgress)
	move(t, s, top.ID, ticket.StatusInReview)
	late := mustCreate(t, s, "late", "", 2)
	if _, err := s.Update(ctx, ticket.UpdateRequest{ID: top.ID, AddDeps: []ticket.ID{late.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: top.ID, To: ticket.StatusDone, ActorRole: ticket.RoleQALead}); err != nil {
		t.Fatalf("done with a dep not done: %v", err)
	}
}

// A cancelled dependency never finishes, so its dependents would wait
// forever: the store refuses to cancel a ticket open tickets depend on,
// and to add a dependency on a cancelled one.
func TestNoOpenTicketWaitsOnCancelled(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	base := mustCreate(t, s, "base", "", 2)
	dep := create(t, s, ticket.CreateRequest{Title: "dependent", DependsOn: []ticket.ID{base.ID}})

	move(t, s, base.ID, ticket.StatusBlocked)
	_, err := s.Transition(ctx, ticket.TransitionRequest{ID: base.ID, To: ticket.StatusCancelled})
	wantErr(t, err, ticket.ErrHasDependents)

	// Once the dependent lets go, the cancel goes through.
	if _, err := s.Update(ctx, ticket.UpdateRequest{ID: dep.ID, RemoveDeps: []ticket.ID{base.ID}}); err != nil {
		t.Fatal(err)
	}
	move(t, s, base.ID, ticket.StatusCancelled)

	_, err = s.Create(ctx, ticket.CreateRequest{Title: "late", DependsOn: []ticket.ID{base.ID}})
	wantErr(t, err, ticket.ErrInvalid)
	_, err = s.Update(ctx, ticket.UpdateRequest{ID: dep.ID, AddDeps: []ticket.ID{base.ID}})
	wantErr(t, err, ticket.ErrInvalid)

	// A finished dependent doesn't hold a cancel back.
	other := mustCreate(t, s, "other", "", 2)
	fin := mustCreate(t, s, "finished", "", 2)
	move(t, s, fin.ID, ticket.StatusInProgress)
	if _, err := s.Update(ctx, ticket.UpdateRequest{ID: fin.ID, AddDeps: []ticket.ID{other.ID}}); err != nil {
		t.Fatal(err)
	}
	move(t, s, fin.ID, ticket.StatusInReview)
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: fin.ID, To: ticket.StatusDone, ActorRole: ticket.RoleQALead}); err != nil {
		t.Fatal(err)
	}
	move(t, s, other.ID, ticket.StatusCancelled)
}
