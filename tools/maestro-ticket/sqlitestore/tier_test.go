package sqlitestore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// A v8 store opens with every ticket kept and no tier, which reads as
// senior.
func TestMigrateV9(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v8.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := append([]string{}, migrations[:8]...)
	stmts = append(stmts,
		`INSERT INTO tickets (title, status, role, priority, created_by, goal, created_at, updated_at) VALUES
			('a', 'todo', 'engineer', 2, 'cos', 'demo', 1, 1),
			('b', 'in_review', 'engineer', 1, 'cos', '', 1, 1)`,
		"PRAGMA user_version = 8",
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
	for i, title := range []string{"a", "b"} {
		d, err := s.Get(ctx, ticket.ID(i+1))
		if err != nil || d.Title != title || d.Tier != "" {
			t.Errorf("T-%d: %q tier %q, %v", i+1, d.Title, d.Tier, err)
		}
	}
	if d, _ := s.Get(ctx, 1); d.Goal != "demo" {
		t.Errorf("goal lost in migration: %q", d.Goal)
	}
	list, err := s.List(ctx, ticket.ListFilter{Tier: ticket.TierSenior})
	if err != nil || len(list) != 2 {
		t.Fatalf("migrated tickets read as senior: %+v, %v", list, err)
	}
	if rep, err := s.Doctor(ctx); err != nil || !rep.OK() {
		t.Fatalf("doctor after v9: %+v, %v", rep, err)
	}
}
