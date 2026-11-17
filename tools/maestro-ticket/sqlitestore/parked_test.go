package sqlitestore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

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
