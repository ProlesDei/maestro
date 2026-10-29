package sqlitestore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

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
	for _, to := range []ticket.Status{ticket.StatusInProgress, ticket.StatusInReview, ticket.StatusInQA, ticket.StatusReadyToRelease} {
		move(t, s, id, to)
	}
	if _, err := s.Transition(context.Background(), ticket.TransitionRequest{ID: id, To: ticket.StatusDone, ActorRole: ticket.RoleOperationsManager}); err != nil {
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
	move(t, s, other.ID, ticket.StatusCancelled) // cancelled is not done
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
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: bl.ID, Holder: "planner/s1"}); err != nil {
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
	docs := create(t, s, ticket.CreateRequest{Title: "docs work", Areas: []string{"docs/decisions"}})
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: groom.ID, Holder: "planner/s1"}); err != nil {
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
// work past them (0243).
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
// claim's (0243).
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
	other := []string{"docs", "evals"}
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
// areas, or holds areas. Review, QA, release and answering an escalation
// neither wait for nor block an engineer on the same code (0245).
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
