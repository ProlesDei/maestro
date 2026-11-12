package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// Two tickets that only waited on another sat in backlog, and the planner
// read them as held on purpose. Every backlog ticket now says why it waits.
func TestBacklogNeedsAReason(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "x", Status: ticket.StatusBacklog}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("backlog with no reason: %v; want invalid", err)
	}
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "x", Parked: ticket.ParkedHold}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("todo with a reason: %v; want invalid", err)
	}
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "x", Status: ticket.StatusBacklog, Parked: "deps"}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("unknown reason: %v; want invalid", err)
	}

	held := create(t, s, ticket.CreateRequest{Title: "held", Status: ticket.StatusBacklog, Parked: ticket.ParkedHold})
	if held.Parked != ticket.ParkedHold {
		t.Fatalf("parked %q; want hold", held.Parked)
	}
	if got := move(t, s, held.ID, ticket.StatusTodo); got.Parked != "" {
		t.Fatalf("todo kept parked %q", got.Parked)
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: held.ID, To: ticket.StatusBacklog}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("into backlog with no reason: %v; want invalid", err)
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: held.ID, To: ticket.StatusBlocked, Parked: ticket.ParkedHold}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("a reason on a move to blocked: %v; want invalid", err)
	}
	got, err := s.Transition(ctx, ticket.TransitionRequest{ID: held.ID, To: ticket.StatusBacklog, Parked: ticket.ParkedDeferred})
	if err != nil || got.Parked != ticket.ParkedDeferred {
		t.Fatalf("into backlog as deferred: %+v, %v", got, err)
	}

	// Blocked from backlog keeps the reason and returns with it.
	if got := move(t, s, held.ID, ticket.StatusBlocked); got.Parked != ticket.ParkedDeferred {
		t.Fatalf("blocked from backlog: parked %q; want deferred", got.Parked)
	}
	if got := move(t, s, held.ID, ticket.StatusBacklog); got.Parked != ticket.ParkedDeferred {
		t.Fatalf("back from blocked: parked %q; want deferred", got.Parked)
	}

	// update changes the reason of a parked ticket, and of no other.
	got, err = s.Update(ctx, ticket.UpdateRequest{ID: held.ID, Parked: ticket.ParkedHold})
	if err != nil || got.Parked != ticket.ParkedHold || got.Version != 5 {
		t.Fatalf("update --parked: %+v, %v", got, err)
	}
	other := create(t, s, ticket.CreateRequest{Title: "todo"})
	if _, err := s.Update(ctx, ticket.UpdateRequest{ID: other.ID, Parked: ticket.ParkedHold}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("update --parked on todo: %v; want invalid", err)
	}

	create(t, s, ticket.CreateRequest{Title: "later", Status: ticket.StatusBacklog, Parked: ticket.ParkedUnready})
	list, err := s.List(ctx, ticket.ListFilter{Parked: []ticket.Parked{ticket.ParkedHold}})
	if err != nil || len(list) != 1 || list[0].ID != held.ID {
		t.Fatalf("list --parked hold: %+v, %v", list, err)
	}
	if rep, err := s.Doctor(ctx); err != nil || !rep.OK() {
		t.Fatalf("doctor: %+v, %v", rep, err)
	}

	// Cancelling clears it.
	if got := move(t, s, held.ID, ticket.StatusCancelled); got.Parked != "" {
		t.Fatalf("cancelled kept parked %q", got.Parked)
	}
}

func TestGoal(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "x", Goal: "Swaps Demo"}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("bad goal: %v; want invalid", err)
	}
	a := create(t, s, ticket.CreateRequest{Title: "a", Goal: "swaps-demo"})
	split := create(t, s, ticket.CreateRequest{Title: "a1", SplitFrom: a.ID})
	if split.Goal != "swaps-demo" {
		t.Fatalf("split goal %q; want the old ticket's", split.Goal)
	}
	moved := create(t, s, ticket.CreateRequest{Title: "a2", SplitFrom: a.ID, Goal: "toolbox"})
	if moved.Goal != "toolbox" {
		t.Fatalf("split with its own goal: %q", moved.Goal)
	}
	create(t, s, ticket.CreateRequest{Title: "none"})
	list, err := s.List(ctx, ticket.ListFilter{Goal: "swaps-demo"})
	if err != nil || len(list) != 2 {
		t.Fatalf("list --goal: %+v, %v", list, err)
	}
	empty := ""
	got, err := s.Update(ctx, ticket.UpdateRequest{ID: a.ID, Goal: &empty})
	if err != nil || got.Goal != "" {
		t.Fatalf("clear goal: %+v, %v", got, err)
	}
}

// Before v6 the only reason written down was a planner session's
// "Parked: planning" comment. Those become planning; every other backlog
// ticket becomes unready, which the planner reads before it goes to todo.
func TestMigrateV6(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v5.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := append([]string{}, migrations[:5]...)
	stmts = append(stmts,
		`INSERT INTO tickets (title, status, role, priority, created_by, created_at, updated_at) VALUES
			('planning', 'backlog', 'engineer', 2, 'cos', 1, 1),
			('old', 'backlog', 'engineer', 2, 'cos', 1, 1),
			('work', 'todo', 'engineer', 2, 'cos', 1, 1)`,
		`INSERT INTO tickets (title, status, blocked_from, role, priority, created_by, created_at, updated_at)
			VALUES ('blk', 'blocked', 'backlog', 'engineer', 2, 'cos', 1, 1)`,
		`INSERT INTO comments (ticket_id, author, body, created_at) VALUES
			(1, 'planner', 'Parked: planning', 1), (2, 'planner', 'Parked: planning', 1), (2, 'cos', 'later', 2)`,
		"PRAGMA user_version = 5",
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
	want := []ticket.Parked{ticket.ParkedPlanning, ticket.ParkedUnready, "", ticket.ParkedUnready}
	for i, w := range want {
		d, err := s.Get(ctx, ticket.ID(i+1))
		if err != nil || d.Parked != w || d.Goal != "" {
			t.Errorf("T-%d: parked %q goal %q, %v; want %q", i+1, d.Parked, d.Goal, err, w)
		}
	}
	evs, err := s.Events(ctx, ticket.EventFilter{Type: ticket.EventUpdated})
	if err != nil || len(evs) != 3 {
		t.Fatalf("migration events: %+v, %v; want 3", evs, err)
	}
	if rep, err := s.Doctor(ctx); err != nil || !rep.OK() {
		t.Fatalf("doctor after v6: %+v, %v", rep, err)
	}
}
