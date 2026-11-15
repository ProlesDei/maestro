package cli

import (
	"strings"
	"testing"
)

// The planner tags each ticket with a tier, and bots route builds on it
// without reading the ticket.
func TestTierFlags(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "docs", "--tier", "junior")
	h.ok(nil, "create", "--title", "auth")
	h.ok(nil, "create", "--title", "docs 1", "--split-from", "T-1")
	if msg := h.fails(2, "create", "--title", "x", "--tier", "lead"); msg != "invalid" {
		t.Fatalf("bad tier: %q", msg)
	}
	var l struct {
		Tickets []struct{ ID, Tier string }
	}
	h.ok(&l, "list", "--tier", "junior")
	if len(l.Tickets) != 2 || l.Tickets[0].ID != "T-1" || l.Tickets[1].ID != "T-3" || l.Tickets[1].Tier != "junior" {
		t.Fatalf("list --tier junior: %+v", l.Tickets)
	}
	h.ok(&l, "list", "--tier", "senior")
	if len(l.Tickets) != 1 || l.Tickets[0].ID != "T-2" || l.Tickets[0].Tier != "" {
		t.Fatalf("list --tier senior: %+v; want T-2, with none", l.Tickets)
	}
	h.ok(nil, "update", "T-2", "--tier", "senior")
	if code, out, _ := h.run("", "list"); code != 0 || !strings.Contains(out, "TIER") || !strings.Contains(out, "senior") || !strings.Contains(out, "junior") {
		t.Fatalf("list text: %d\n%s", code, out)
	}
	if code, out, _ := h.run("", "get", "T-1"); code != 0 || !strings.Contains(out, "tier:     junior") {
		t.Fatalf("get text: %d\n%s", code, out)
	}
	h.ok(nil, "update", "T-2", "--tier", "")
	h.ok(&l, "list", "--tier", "senior")
	if len(l.Tickets) != 1 || l.Tickets[0].Tier != "" {
		t.Fatalf("cleared tier: %+v", l.Tickets)
	}
	if msg := h.fails(2, "list", "--tier", "mid"); msg != "invalid" {
		t.Fatalf("list --tier mid: %q", msg)
	}
}
