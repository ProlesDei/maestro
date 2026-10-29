package sqlitestore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ProlesDei/maestro/tools/at-ticket/ticket"
)

// openMany opens n independent stores on one file. Each has its own
// connections, so they contend through SQLite's file locks exactly as
// separate processes would.
func openMany(t *testing.T, path string, n int) []*Store {
	t.Helper()
	stores := make([]*Store, n)
	for i := range stores {
		s, err := Open(context.Background(), path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = s
		t.Cleanup(func() { s.Close() })
	}
	return stores
}

// runAll runs fn(i) on n goroutines released together.
func runAll(n int, fn func(i int)) {
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer done.Done()
			start.Wait()
			fn(i)
		}()
	}
	start.Done()
	done.Wait()
}

func TestRaceClaimNext(t *testing.T) {
	const tickets, workers = 50, 200
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tickets.db")
	seed := openMany(t, path, 1)[0]
	for i := 0; i < tickets; i++ {
		p := i % 4
		if _, err := seed.Create(ctx, ticket.CreateRequest{Title: fmt.Sprint("t", i), Priority: &p}); err != nil {
			t.Fatal(err)
		}
	}
	stores := openMany(t, path, workers)

	var mu sync.Mutex
	won := map[ticket.ID]string{}
	var noWork, doubles int
	var other []error
	runAll(workers, func(i int) {
		holder := fmt.Sprintf("bot-%d/session-%d", i%7, i)
		got, err := stores[i].ClaimNext(ctx, ticket.ClaimRequest{Holder: holder})
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			if prev, dup := won[got.ID]; dup {
				doubles++
				t.Errorf("double claim of %s by %s and %s", got.ID, prev, holder)
			}
			won[got.ID] = holder
		case errors.Is(err, ticket.ErrNoWork):
			noWork++
		default:
			other = append(other, err)
		}
	})

	if len(other) > 0 {
		t.Fatalf("%d unexpected errors, first: %v", len(other), other[0])
	}
	if doubles != 0 || len(won) != tickets || noWork != workers-tickets {
		t.Fatalf("claims=%d doubles=%d noWork=%d; want %d, 0, %d", len(won), doubles, noWork, tickets, workers-tickets)
	}

	// The store agrees with what the workers saw: no lost or phantom writes.
	all, err := seed.List(ctx, ticket.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range all {
		if tk.Claim == nil || tk.Claim.Holder != won[tk.ID] || tk.Version != 2 {
			t.Errorf("%s: claim %+v version %d; worker saw %q", tk.ID, tk.Claim, tk.Version, won[tk.ID])
		}
	}
	claims, _ := seed.Events(ctx, ticket.EventFilter{Type: ticket.EventClaimed})
	if len(claims) != tickets {
		t.Fatalf("claimed events = %d; want %d", len(claims), tickets)
	}
	t.Logf("%d workers, %d tickets: %d claimed, %d no-work, 0 double claims", workers, tickets, len(won), noWork)
}

func TestRaceNoLostComments(t *testing.T) {
	const workers = 200
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tickets.db")
	seed := openMany(t, path, 1)[0]
	tk, _ := seed.Create(ctx, ticket.CreateRequest{Title: "busy"})
	stores := openMany(t, path, workers)

	var mu sync.Mutex
	var errs []error
	runAll(workers, func(i int) {
		_, err := stores[i].Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: fmt.Sprint("c", i), Actor: fmt.Sprint("w", i)})
		if err != nil {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}
	})
	if len(errs) > 0 {
		t.Fatalf("%d errors, first: %v", len(errs), errs[0])
	}
	d, _ := seed.Get(ctx, tk.ID)
	if len(d.Comments) != workers {
		t.Fatalf("comments = %d; want %d", len(d.Comments), workers)
	}
	seen := map[string]bool{}
	for _, c := range d.Comments {
		seen[c.Body] = true
	}
	if len(seen) != workers {
		t.Fatalf("distinct comments = %d", len(seen))
	}
	evs, _ := seed.Events(ctx, ticket.EventFilter{})
	if len(evs) != workers+1 {
		t.Fatalf("events = %d; want %d", len(evs), workers+1)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq != evs[i-1].Seq+1 {
			t.Fatalf("event seq gap or reorder at %d: %d after %d", i, evs[i].Seq, evs[i-1].Seq)
		}
	}
}

func TestRaceOptimisticTransition(t *testing.T) {
	const workers = 50
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tickets.db")
	seed := openMany(t, path, 1)[0]
	tk, _ := seed.Create(ctx, ticket.CreateRequest{Title: "contended"})
	stores := openMany(t, path, workers)

	var mu sync.Mutex
	var wins, conflicts int
	var other []error
	runAll(workers, func(i int) {
		_, err := stores[i].Transition(ctx, ticket.TransitionRequest{
			ID: tk.ID, From: ticket.StatusTodo, To: ticket.StatusBlocked, ExpectVersion: tk.Version,
			Note: fmt.Sprint("w", i),
		})
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ticket.ErrVersionConflict), errors.Is(err, ticket.ErrStatusConflict):
			conflicts++
		default:
			other = append(other, err)
		}
	})
	if len(other) > 0 || wins != 1 || conflicts != workers-1 {
		t.Fatalf("wins=%d conflicts=%d other=%v", wins, conflicts, other)
	}
	got, _ := seed.Get(ctx, tk.ID)
	if got.Version != tk.Version+1 {
		t.Fatalf("version %d; want %d", got.Version, tk.Version+1)
	}
}

func TestRaceIdempotentCreate(t *testing.T) {
	const workers = 50
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tickets.db")
	stores := openMany(t, path, workers)

	var mu sync.Mutex
	ids := map[ticket.ID]int{}
	var errs []error
	runAll(workers, func(i int) {
		tk, err := stores[i].Create(ctx, ticket.CreateRequest{Title: "retried", Actor: "cos", IdempotencyKey: "cos/s1:create"})
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			errs = append(errs, err)
			return
		}
		ids[tk.ID]++
	})
	if len(errs) > 0 || len(ids) != 1 {
		t.Fatalf("ids=%v errs=%v", ids, errs)
	}
	all, _ := stores[0].List(ctx, ticket.ListFilter{})
	if len(all) != 1 {
		t.Fatalf("tickets = %d; want 1", len(all))
	}
}
