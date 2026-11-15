package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// A tier says which engineer builds a ticket, so a bot routes a build
// without reading the ticket. A split keeps it; senior includes none.
func TestTier(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "x", Tier: "mid"}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("bad tier: %v; want invalid", err)
	}
	jr := create(t, s, ticket.CreateRequest{Title: "docs", Tier: ticket.TierJunior})
	sr := create(t, s, ticket.CreateRequest{Title: "auth", Tier: ticket.TierSenior})
	none := create(t, s, ticket.CreateRequest{Title: "old"})
	if jr.Tier != ticket.TierJunior || sr.Tier != ticket.TierSenior || none.Tier != "" {
		t.Fatalf("tiers %q %q %q", jr.Tier, sr.Tier, none.Tier)
	}
	split := create(t, s, ticket.CreateRequest{Title: "docs 1", SplitFrom: jr.ID})
	if split.Tier != ticket.TierJunior {
		t.Fatalf("split tier %q; want the old ticket's", split.Tier)
	}
	up := create(t, s, ticket.CreateRequest{Title: "docs 2", SplitFrom: jr.ID, Tier: ticket.TierSenior})
	if up.Tier != ticket.TierSenior {
		t.Fatalf("split with its own tier: %q", up.Tier)
	}

	ids := func(f ticket.ListFilter) []ticket.ID {
		list, err := s.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := []ticket.ID{}
		for _, x := range list {
			out = append(out, x.ID)
		}
		return out
	}
	if got := ids(ticket.ListFilter{Tier: ticket.TierJunior}); len(got) != 2 || got[0] != jr.ID || got[1] != split.ID {
		t.Fatalf("list --tier junior: %v", got)
	}
	if got := ids(ticket.ListFilter{Tier: ticket.TierSenior}); len(got) != 3 {
		t.Fatalf("list --tier senior: %v; want auth, old (none) and docs 2", got)
	}

	senior := ticket.TierSenior
	got, err := s.Update(ctx, ticket.UpdateRequest{ID: jr.ID, Tier: &senior})
	if err != nil || got.Tier != ticket.TierSenior {
		t.Fatalf("update tier: %+v, %v", got, err)
	}
	empty := ""
	if got, err = s.Update(ctx, ticket.UpdateRequest{ID: jr.ID, Tier: &empty}); err != nil || got.Tier != "" {
		t.Fatalf("clear tier: %+v, %v", got, err)
	}
	bad := "lead"
	if _, err := s.Update(ctx, ticket.UpdateRequest{ID: jr.ID, Tier: &bad}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("update to a bad tier: %v; want invalid", err)
	}
	evs, err := s.Events(ctx, ticket.EventFilter{TicketID: jr.ID, Type: ticket.EventUpdated})
	if err != nil || len(evs) != 2 {
		t.Fatalf("tier updates logged: %+v, %v; want 2", evs, err)
	}
}

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
