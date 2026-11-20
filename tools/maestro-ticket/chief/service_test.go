package chief

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
)

func TestConcurrentChiefMemory(t *testing.T) {
	uri := os.Getenv("MAESTRO_TICKET_TEST_MONGO")
	if uri == "" {
		t.Skip("set MAESTRO_TICKET_TEST_MONGO for replica-set trials")
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal("invalid test URI")
	}
	var random [8]byte
	rand.Read(random[:])
	board := hex.EncodeToString(random[:])
	u.Path = "/chief_trial_" + board
	ctx := context.Background()
	s, err := mongostore.Open(ctx, u.String(), mongostore.Options{Create: true, BoardID: board})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c, err := Open(ctx, Config{URI: u.String(), Board: board})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.db.Drop(ctx); err != nil {
			t.Error(err)
		}
		c.Close(ctx)
	})
	const a, b = "astra", "opus"
	apply := func(thread, verb string, r Request) any {
		t.Helper()
		r.Goal = "billing"
		out, err := c.Apply(ctx, thread, verb, r)
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		return out
	}
	for _, thread := range []string{a, b} {
		apply(thread, "register", Request{Profile: thread, Harness: "codex", Model: "scratch", Effort: "medium"})
		apply(thread, "draft", Request{Text: thread + " private draft"})
	}
	race := func(verb string, r Request) string {
		t.Helper()
		r.Goal = "billing"
		results := make(chan string, 2)
		for _, thread := range []string{a, b} {
			go func() {
				_, err := c.Apply(ctx, thread, verb, r)
				if err == nil {
					results <- thread
				} else {
					results <- ""
				}
			}()
		}
		x, y := <-results, <-results
		if (x == "") == (y == "") {
			t.Fatalf("%s: two callers returned %q and %q", verb, x, y)
		}
		return x + y
	}
	race("publish", Request{Source: "scratch owner acceptance"})
	if n, err := c.db.Collection("chief_drafts").CountDocuments(ctx, map[string]any{}); err != nil || n != 2 {
		t.Fatalf("lost a private draft: %d, %v", n, err)
	}
	req := Request{ID: "scope", Revision: 1, Text: "Which report format?"}
	apply(a, "propose", req)
	holder := race("claim", req)
	next := b
	if holder == b {
		next = a
	}
	req.Generation = 1
	apply(holder, "question", req)
	req.Transfer = true
	apply(next, "claim", req)
	req.Goal = "billing"
	if _, err := c.Apply(ctx, holder, "question", req); err == nil {
		t.Fatal("old holder kept its claim")
	}
	apply(a, "direction", Request{ID: "export", Kind: "delegation", Text: "Choose a text format", Source: "owner turn 1"})
	apply(b, "direction", Request{ID: "export", Kind: "revoked", Text: "Ask me first", Source: "owner turn 2", Revision: 1})
	item := Request{ID: "format", Revision: 1, Text: "CSV or TSV?"}
	apply(b, "propose", item)
	apply(b, "claim", item)
	item.Goal, item.Generation, item.Rule, item.RuleRevision, item.Rationale = "billing", 1, "export", 1, "CSV fits the old permission"
	if _, err := c.Apply(ctx, b, "rule", item); err == nil {
		t.Fatal("revoked delegation authorized a ruling")
	}
	if n, err := c.db.Collection("planner_deliveries").CountDocuments(ctx, map[string]any{}); err != nil || n != 1 {
		t.Fatalf("want one plan, got %d %v", n, err)
	}
	// A goal-specific delegation can settle one card without an owner question.
	apply(a, "direction", Request{ID: "export", Kind: "delegation", Text: "Choose CSV or TSV", Source: "owner turn 3", Revision: 2})
	if err := s.PutGoalItem(ctx, item.ID, item.Goal, "CSV or TSV?"); err != nil {
		t.Fatal(err)
	}
	item.RuleRevision, item.Rationale, item.Text = 3, "CSV fits the current permission", "Use CSV"
	apply(b, "rule", item)
	ruling := item.Goal + "/" + item.ID + "/ruling"
	for _, verb := range []string{"plan", "ack"} {
		if _, err := s.AcceptDelivery(ctx, ruling, verb); err == nil {
			t.Fatalf("%s accepted an unserved ruling", verb)
		}
	}
	first, err := s.AcceptDelivery(ctx, ruling, "rule")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.AcceptDelivery(ctx, ruling, "rule")
	if err != nil || first.Receipt == "" || replay.Receipt != first.Receipt {
		t.Fatalf("replay changed receipt: %v", err)
	}
	item.Source = ruling
	apply(b, "ack", item)
	if cards, err := s.GoalItems(ctx, item.Goal); err != nil || len(cards) != 0 {
		t.Fatalf("acknowledged card in pickup: %v", err)
	}
	if n, err := c.db.Collection("goal_decisions").CountDocuments(ctx, map[string]any{"_id": "billing/format", "question": map[string]any{"$exists": true}}); err != nil || n != 0 {
		t.Fatalf("unexpected owner question: %v", err)
	}
	// Another goal can publish its own brief, but cannot reuse billing's grant.
	other := func(verb string, req Request) error {
		req.Goal = "onboarding"
		_, err := c.Apply(ctx, "other", verb, req)
		return err
	}
	for _, step := range []struct {
		verb string
		req  Request
	}{
		{"register", Request{Profile: "max-b", Harness: "claude", Model: "scratch", Effort: "medium"}},
		{"draft", Request{Text: "Shorten signup"}}, {"publish", Request{Source: "scratch owner acceptance"}},
		{"propose", Request{ID: item.ID, Revision: 1, Text: "Choose a format"}}, {"claim", Request{ID: item.ID, Revision: 1}},
	} {
		if err := other(step.verb, step.req); err != nil {
			t.Fatal(err)
		}
	}
	if err := other("rule", item); err == nil {
		t.Fatal("another goal used billing's delegation")
	}
	if _, err := s.AcceptDelivery(ctx, "onboarding/brief/1", "plan"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptDelivery(ctx, "billing/brief/1", "plan"); err != nil {
		t.Fatal(err)
	}

}

func TestReplyRequiresOriginalQuestion(t *testing.T) {
	m := []Message{{Role: "assistant", Text: "Q-1 Choose a label"}, {Role: "assistant", Text: "Which team?"}, {Role: "user", RunID: "R-2", Text: "green"}}
	if _, err := exactReply(m, "Choose a label", "Q-1", "R-2"); err == nil {
		t.Fatal("accepted an ambiguous answer")
	}
	m[2].Text = "Owner reply to Q-1 (verbatim on the next line):\n blue "
	if got, err := exactReply(m, "Choose a label", "Q-1", "R-2"); err != nil || got != " blue " {
		t.Fatalf("changed the exact reply %q %v", got, err)
	}
}
