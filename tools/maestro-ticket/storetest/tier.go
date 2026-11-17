package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// A tier says which engineer builds a ticket, so a bot routes a build
// without reading the ticket. A split keeps it; senior includes none.
func testTier(t *testing.T, f Factory) {
	s, _ := newStore(t, f)
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
