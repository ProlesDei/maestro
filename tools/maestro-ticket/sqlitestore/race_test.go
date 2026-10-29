package sqlitestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
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

// TestRaceWritesAfterReclaim: a lease expired and another holder took it.
// The old session and the new holder write at the same time. Every stale
// write is rejected, every current one lands.
func TestRaceWritesAfterReclaim(t *testing.T) {
	const workers = 100
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tickets.db")
	clk := &clock{t: time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)}
	seed, err := Open(ctx, path, Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { seed.Close() })
	tk, _ := seed.Create(ctx, ticket.CreateRequest{Title: "re-claimed"})
	old, _ := seed.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/old", TTL: time.Minute})
	clk.Advance(2 * time.Minute)
	cur, err := seed.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/new"})
	if err != nil {
		t.Fatal(err)
	}
	stores := make([]*Store, workers)
	for i := range stores {
		if stores[i], err = Open(ctx, path, Options{Now: clk.Now}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stores[i].Close() })
	}

	var mu sync.Mutex
	var landed, fenced int
	var other []error
	runAll(workers, func(i int) {
		var err error
		stale := i%2 == 0
		holder, token := "jse/new", cur.Claim.Token
		if stale {
			holder, token = "jse/old", old.Claim.Token
		}
		if stale && i%4 == 2 {
			// A stale cancel would destroy the new holder's work.
			_, err = stores[i].Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusCancelled, Holder: holder, Token: token})
		} else {
			_, err = stores[i].Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: fmt.Sprint("c", i), Holder: holder, Token: token})
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case stale && errors.Is(err, ticket.ErrClaimed):
			fenced++
		case !stale && err == nil:
			landed++
		default:
			other = append(other, fmt.Errorf("worker %d stale=%v: %v", i, stale, err))
		}
	})
	if len(other) > 0 {
		t.Fatalf("%d unexpected results, first: %v", len(other), other[0])
	}
	if fenced != workers/2 || landed != workers/2 {
		t.Fatalf("fenced %d landed %d; want %d each", fenced, landed, workers/2)
	}
	d, _ := seed.Get(ctx, tk.ID)
	for _, c := range d.Comments {
		var n int
		fmt.Sscanf(c.Body, "c%d", &n)
		if n%2 == 0 {
			t.Errorf("stale comment landed: %q", c.Body)
		}
	}
	if len(d.Comments) != workers/2 || d.Status != ticket.StatusTodo || d.Claim.Holder != "jse/new" {
		t.Fatalf("comments %d status %s claim %+v; want %d, todo, jse/new", len(d.Comments), d.Status, d.Claim, workers/2)
	}
}

// TestRaceExpiryVsHeartbeat: leases expire while their holders' heartbeats,
// reapers and other claimers all hit the store at once, on a clock that keeps
// moving. A heartbeat either lands before the expiry is logged, or fails;
// the logged expiry is always the last extension a heartbeat reported.
func TestRaceExpiryVsHeartbeat(t *testing.T) {
	// A writer that hits SQLITE_BUSY can sleep a full second in SQLite's
	// busy handler, so the TTL spans a few seconds of real time: 120 steps
	// of 20ms. Odd sessions crash early, so their leases must expire.
	const tickets, reapers, claimers, steps, ttl = 20, 4, 4, 160, 2 * time.Minute
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tickets.db")
	clk := &clock{t: time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)}
	open := func() *Store {
		s, err := Open(ctx, path, Options{Now: clk.Now})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	seed := open()
	claims := make([]ticket.Ticket, tickets)
	for i := range claims {
		tk, _ := seed.Create(ctx, ticket.CreateRequest{Title: fmt.Sprint("t", i)})
		c, err := seed.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: fmt.Sprintf("jse/hb%d", i), TTL: ttl})
		if err != nil {
			t.Fatal(err)
		}
		claims[i] = c
	}
	stores := make([]*Store, tickets+reapers+claimers)
	for i := range stores {
		stores[i] = open()
	}

	var mu sync.Mutex
	lastBeat := map[ticket.ID]time.Time{} // newest expiry a heartbeat reported
	for _, c := range claims {
		lastBeat[c.ID] = c.Claim.ExpiresAt
	}
	var other []error
	beats := 0
	stop := make(chan struct{})
	go func() {
		for i := 0; i < steps; i++ {
			clk.Advance(time.Second)
			time.Sleep(20 * time.Millisecond)
		}
		close(stop)
	}()
	runAll(len(stores), func(i int) {
		s := stores[i]
		for {
			select {
			case <-stop:
				return
			default:
			}
			var err error
			switch {
			case i < tickets:
				c := claims[i]
				if i%2 == 1 && clk.Now().Sub(claims[0].CreatedAt) > time.Duration(i)*time.Second {
					return // this session crashes; its lease must expire
				}
				var got ticket.Ticket
				got, err = s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: c.ID, Holder: c.Claim.Holder, Token: c.Claim.Token, TTL: ttl})
				if err == nil {
					mu.Lock()
					lastBeat[c.ID] = got.Claim.ExpiresAt
					beats++
					mu.Unlock()
				} else if errors.Is(err, ticket.ErrLeaseLost) {
					return // the session stops
				}
				time.Sleep(time.Duration(i%3) * time.Millisecond)
			case i < tickets+reapers:
				_, err = s.Reap(ctx)
				time.Sleep(time.Millisecond)
			default:
				time.Sleep(time.Millisecond)
				_, err = s.ClaimNext(ctx, ticket.ClaimRequest{Holder: fmt.Sprintf("sse/c%d", i), TTL: time.Hour})
				if errors.Is(err, ticket.ErrNoWork) {
					err = nil
				}
			}
			if err != nil {
				mu.Lock()
				other = append(other, err)
				mu.Unlock()
				return
			}
		}
	})
	if len(other) > 0 {
		t.Fatalf("%d unexpected errors, first: %v", len(other), other[0])
	}
	expired := 0
	for _, c := range claims {
		evs, _ := seed.Events(ctx, ticket.EventFilter{TicketID: c.ID, Type: ticket.EventLeaseExpired})
		got, _ := seed.Get(ctx, c.ID)
		switch len(evs) {
		case 0:
			if got.Claim == nil || got.Claim.Token != c.Claim.Token {
				t.Errorf("%s: lease gone without a lease_expired event: %+v", c.ID, got.Claim)
			}
		case 1:
			expired++
			var data struct {
				Token     int64     `json:"token"`
				ExpiredAt time.Time `json:"expired_at"`
			}
			json.Unmarshal(evs[0].Data, &data)
			if data.Token != c.Claim.Token || !data.ExpiredAt.Equal(lastBeat[c.ID]) {
				t.Errorf("%s: expired token %d at %s; last heartbeat said %s", c.ID, data.Token, data.ExpiredAt, lastBeat[c.ID])
			}
			if got.LeaseExpiries != 1 || (got.Claim != nil && got.Claim.Token <= c.Claim.Token) {
				t.Errorf("%s: after expiry %+v, expiries %d", c.ID, got.Claim, got.LeaseExpiries)
			}
		default:
			t.Errorf("%s: %d lease_expired events", c.ID, len(evs))
		}
	}
	if expired == 0 || expired == tickets || beats == 0 {
		t.Logf("weak run: want some leases kept alive and some expired")
	}
	t.Logf("%d leases, %d heartbeats, %d expired under load", tickets, beats, expired)
}

// F14 (M10): many processes opening a new store at once all migrate it
// once, to the current version.
func TestRaceFirstOpenMigrates(t *testing.T) {
	const n = 30
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fresh", "tickets.db")
	stores := make([]*Store, n)
	errs := make([]error, n)
	runAll(n, func(i int) { stores[i], errs[i] = Open(ctx, path, Options{}) })
	for i, err := range errs {
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		defer stores[i].Close()
	}
	var v int
	stores[0].w.QueryRow("PRAGMA user_version").Scan(&v)
	rep, err := stores[0].Doctor(ctx)
	if v != schemaVersion || err != nil || !rep.OK() {
		t.Fatalf("user_version %d, doctor %+v, %v", v, rep, err)
	}
	if _, err := stores[n-1].Create(ctx, ticket.CreateRequest{Title: "works"}); err != nil {
		t.Fatal(err)
	}
}
