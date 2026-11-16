package cli

import (
	"strings"
	"testing"
)

// create --lint refuses an engineer's ship ticket that lacks the shape
// the create-ticket skill asks for, names every gap at once, and leaves
// scouts and other roles' tickets alone. Without --lint nothing changes.
func TestCreateLint(t *testing.T) {
	h := newHarness(t)
	good := "Goal: log in by email.\n\n1. A valid link logs the user in.\n2. A used link shows the expired page.\n\nNot handled:\n- rate limits: T-9 has them\n"
	out := h.fails(2, "create", "--title", "a", "--lint")
	if out != "invalid" {
		t.Fatalf("code %q", out)
	}
	_, stdout, _ := h.run("", "create", "--title", "a", "--lint", "--json")
	for _, want := range []string{"no numbered acceptance criterion", "Not handled:", "no --areas"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("refusal lacks %q: %s", want, stdout)
		}
	}
	if code, stdout, _ := h.run("1. it works\n", "create", "--title", "a", "--areas", "src/a", "--lint", "--body-file", "-", "--json"); code != 2 || strings.Contains(stdout, "numbered") || !strings.Contains(stdout, "Not handled:") {
		t.Fatalf("one gap: %d %s", code, stdout)
	}
	if code, stdout, errs := h.run(good, "create", "--title", "a", "--areas", "src/a", "--lint", "--body-file", "-", "--json"); code != 0 || errs != "" {
		t.Fatalf("a good ticket: %d %s %s", code, stdout, errs)
	}
	// "Not handled: none" and a heading both count; a long list only warns.
	long := "## Criteria\n1. a\n2. b\n3. c\n4. d\n5. e\n6. f\n7. g\n\n## Not handled\nnone\n"
	code, _, errs := h.run(long, "create", "--title", "b", "--areas", "src/b", "--role", "engineer", "--lint", "--body-file", "-", "--json")
	if code != 0 || !strings.Contains(errs, "lint: 7 numbered lines: past about 6 criteria, split the ticket") {
		t.Fatalf("a long ticket: %d %s", code, errs)
	}
	// Markdown around either doesn't hide it.
	md := "- [ ] **1.** it works\n\n> - Not handled: none\n"
	if code, stdout, _ := h.run(md, "create", "--title", "c", "--areas", "src/c", "--lint", "--body-file", "-", "--json"); code != 0 {
		t.Fatalf("a Markdown body: %d %s", code, stdout)
	}
	// Prose that only says the words is no list.
	if code, stdout, _ := h.run("1. it works\n\n- not handled by the caller today\n", "create", "--title", "d", "--areas", "src/d", "--lint", "--body-file", "-", "--json"); code != 2 || !strings.Contains(stdout, "Not handled:") {
		t.Fatalf("prose for a list: %d %s", code, stdout)
	}
	// A scout and another role's ticket have no such shape.
	h.ok(nil, "create", "--title", "Design: x", "--type", "scout", "--role", "designer", "--owner", "planner", "--lint")
	h.ok(nil, "create", "--title", "Live run: x", "--type", "scout", "--role", "qa-lead", "--owner", "qa-lead", "--lint")
	// Without --lint, as before.
	h.ok(nil, "create", "--title", "quick")
	var l struct{ Tickets []struct{ ID string } }
	h.ok(&l, "list")
	if len(l.Tickets) != 6 {
		t.Fatalf("%d tickets; want 6: the refused ones made none", len(l.Tickets))
	}
}
