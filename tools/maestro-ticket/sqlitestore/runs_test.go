package sqlitestore

import (
	"context"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func TestLogRunKeepsTheSessionsNewestTotal(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	a := mustCreate(t, s, "a", "engineer", 1)
	log := func(req ticket.LogRunRequest) ticket.Run {
		t.Helper()
		r, err := s.LogRun(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := log(ticket.LogRunRequest{Session: "s1", Ticket: a.ID, Actor: "junior-engineer", CostUSD: 1.5, InputTokens: 10, CacheWriteTokens: 100})
	if first.Ticket != a.ID || first.Actor != "junior-engineer" || first.CostUSD != 1.5 || first.CacheWriteTokens != 100 {
		t.Fatalf("first log: %+v", first)
	}
	// A resume by another bot, naming no ticket, reports the session's
	// running total: it replaces the totals and keeps the actor and ticket.
	clk.Advance(time.Minute)
	resumed := log(ticket.LogRunRequest{Session: "s1", Actor: "senior-engineer", CostUSD: 2.25, InputTokens: 30, CacheWriteTokens: 150})
	if resumed.Ticket != a.ID || resumed.Actor != "junior-engineer" || resumed.CostUSD != 2.25 || resumed.InputTokens != 30 ||
		!resumed.UpdatedAt.After(resumed.CreatedAt) {
		t.Fatalf("resumed: %+v", resumed)
	}
	// An older reply logged late never shrinks the total.
	if late := log(ticket.LogRunRequest{Session: "s1", Actor: "junior-engineer", CostUSD: 1.5}); late.CostUSD != 2.25 || late.InputTokens != 30 {
		t.Fatalf("late reply changed the run: %+v", late)
	}
	log(ticket.LogRunRequest{Session: "s2", Actor: "planner", CostUSD: 0.5})
	runs, err := s.Runs(ctx, ticket.RunFilter{})
	if err != nil || len(runs) != 2 || runs[0].Session != "s1" || runs[1].Ticket != 0 || runs[1].Origin != "" {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	exp, err := s.Export(ctx)
	if err != nil || len(exp.Runs) != 2 || exp.Runs[0].CostUSD != 2.25 {
		t.Fatalf("export runs = %+v, %v", exp.Runs, err)
	}
}

func TestLogRunRefuses(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	_, err := s.LogRun(ctx, ticket.LogRunRequest{Session: "s1", Ticket: 9, Actor: "qa-lead", CostUSD: 1})
	wantErr(t, err, ticket.ErrNotFound)
	for _, req := range []ticket.LogRunRequest{
		{Session: "", Actor: "qa-lead"},
		{Session: "a\nb", Actor: "qa-lead"},
		{Session: "s1", Actor: " "},
		{Session: "s1", Actor: "qa-lead", CostUSD: -1},
		{Session: "s1", Actor: "qa-lead", OutputTokens: -1},
	} {
		_, err := s.LogRun(ctx, req)
		wantErr(t, err, ticket.ErrInvalid)
	}
	if runs, _ := s.Runs(ctx, ticket.RunFilter{}); len(runs) != 0 {
		t.Fatalf("a refused log wrote %+v", runs)
	}
}

func TestRunsCarryTheTicketsOrigin(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	planned := mustCreate(t, s, "planned", "engineer", 1)
	found, err := s.Create(ctx, ticket.CreateRequest{Title: "found", Role: "engineer", Actor: "reviewer", Goal: "speed"})
	if err != nil {
		t.Fatal(err)
	}
	split, err := s.Create(ctx, ticket.CreateRequest{Title: "rewrite", Role: "engineer", Actor: "planner", SplitFrom: found.ID})
	if err != nil {
		t.Fatal(err)
	}
	start := clk.Now()
	for i, id := range []ticket.ID{planned.ID, found.ID, split.ID} {
		if _, err := s.LogRun(ctx, ticket.LogRunRequest{Session: string(rune('a' + i)), Ticket: id, Actor: "senior-engineer", CostUSD: 1}); err != nil {
			t.Fatal(err)
		}
		clk.Advance(time.Hour)
	}
	runs, err := s.Runs(ctx, ticket.RunFilter{})
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	if runs[0].Origin != "cos" || runs[1].Origin != "reviewer" || runs[1].Goal != "speed" || runs[2].Origin != "split" || runs[2].Goal != "speed" {
		t.Fatalf("origins and goals: %+v", runs)
	}
	if one, _ := s.Runs(ctx, ticket.RunFilter{Ticket: found.ID}); len(one) != 1 || one[0].Session != "b" {
		t.Fatalf("by ticket: %+v", one)
	}
	if late, _ := s.Runs(ctx, ticket.RunFilter{Since: start.Add(90 * time.Minute)}); len(late) != 1 || late[0].Session != "c" {
		t.Fatalf("since: %+v", late)
	}
}

// A cancelled ticket rewritten as one other carries its runs to it, down
// the chain; one split into several, or still open, keeps them.
func TestRunsCountTowardTheRewrite(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	splitOf := func(from ticket.ID, title string) ticket.Ticket {
		t.Helper()
		tk, err := s.Create(ctx, ticket.CreateRequest{Title: title, Role: "engineer", Actor: "planner", SplitFrom: from})
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	sessions := map[string]ticket.ID{}
	logOn := func(sess string, id ticket.ID) {
		t.Helper()
		if _, err := s.LogRun(ctx, ticket.LogRunRequest{Session: sess, Ticket: id, Actor: "senior-engineer", CostUSD: 1}); err != nil {
			t.Fatal(err)
		}
		sessions[sess] = id
		clk.Advance(time.Minute)
	}
	// a -> b -> c, a and b cancelled: their runs count toward c.
	a := mustCreate(t, s, "a", "engineer", 1)
	b := splitOf(a.ID, "b")
	c := splitOf(b.ID, "c")
	// wide was split in two, and open has one rewrite but is still open:
	// both keep their runs.
	wide := mustCreate(t, s, "wide", "engineer", 1)
	splitOf(wide.ID, "w1")
	splitOf(wide.ID, "w2")
	open := mustCreate(t, s, "open", "engineer", 1)
	splitOf(open.ID, "o1")
	for _, id := range []ticket.ID{a.ID, b.ID, wide.ID} {
		move(t, s, id, ticket.StatusCancelled)
	}
	logOn("on-a", a.ID)
	logOn("on-b", b.ID)
	logOn("on-c", c.ID)
	logOn("on-wide", wide.ID)
	logOn("on-open", open.ID)

	runs, err := s.Runs(ctx, ticket.RunFilter{})
	if err != nil || len(runs) != 5 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	want := map[string][2]ticket.ID{ // session: counts toward, carried from
		"on-a": {c.ID, a.ID}, "on-b": {c.ID, b.ID}, "on-c": {c.ID, 0},
		"on-wide": {wide.ID, 0}, "on-open": {open.ID, 0},
	}
	for _, r := range runs {
		if w := want[r.Session]; r.Ticket != w[0] || r.CarriedFrom != w[1] {
			t.Errorf("%s: ticket %s carried from %s; want %s from %s", r.Session, r.Ticket, r.CarriedFrom, w[0], w[1])
		}
		if r.Ticket == c.ID && r.Origin != "split" {
			t.Errorf("%s: origin %q; want c's, split", r.Session, r.Origin)
		}
	}
	// --ticket keeps runs counted toward it and runs logged on it.
	for id, n := range map[ticket.ID]int{c.ID: 3, a.ID: 1, b.ID: 1, wide.ID: 1} {
		if got, _ := s.Runs(ctx, ticket.RunFilter{Ticket: id}); len(got) != n {
			t.Errorf("--ticket %s: %d runs; want %d", id, len(got), n)
		}
	}
}

// A ticket re-filed by hand links to the cancelled one it replaces: it
// adds that ticket's tries and gets its runs. Each wrong link is refused.
func TestUpdateSplitFromLinksARefile(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	old := mustCreate(t, s, "old", "engineer", 1)
	for _, to := range []ticket.Status{ticket.StatusInProgress, ticket.StatusInReview, ticket.StatusInProgress,
		ticket.StatusInReview, ticket.StatusInProgress, ticket.StatusCancelled} {
		move(t, s, old.ID, to)
	}
	refile := mustCreate(t, s, "refile", "engineer", 1)
	move(t, s, refile.ID, ticket.StatusInProgress)
	move(t, s, refile.ID, ticket.StatusInReview)
	move(t, s, refile.ID, ticket.StatusInProgress) // its own send-back: 1
	if _, err := s.LogRun(ctx, ticket.LogRunRequest{Session: "s1", Ticket: old.ID, Actor: "senior-engineer", CostUSD: 14.91}); err != nil {
		t.Fatal(err)
	}
	upd := func(id, from ticket.ID) (ticket.Ticket, error) {
		return s.Update(ctx, ticket.UpdateRequest{ID: id, SplitFrom: from, Actor: "planner"})
	}
	open := mustCreate(t, s, "open", "engineer", 1)
	_, err := upd(refile.ID, open.ID)
	wantErr(t, err, ticket.ErrInvalid) // not cancelled
	_, err = upd(refile.ID, 999)
	wantErr(t, err, ticket.ErrNotFound)
	_, err = upd(refile.ID, refile.ID)
	wantErr(t, err, ticket.ErrInvalid)

	got, err := upd(refile.ID, old.ID)
	if err != nil || got.Tries != 3 {
		t.Fatalf("link: tries %d, %v; want 2 + 1", got.Tries, err)
	}
	runs, _ := s.Runs(ctx, ticket.RunFilter{Ticket: refile.ID})
	if len(runs) != 1 || runs[0].CarriedFrom != old.ID || runs[0].Origin != "split" || runs[0].Tries != 3 {
		t.Fatalf("runs toward the refile: %+v", runs)
	}
	// A retry of the link changes nothing: no second count of the tries.
	evs, _ := s.Events(ctx, ticket.EventFilter{TicketID: refile.ID, Type: ticket.EventUpdated})
	if again, err := upd(refile.ID, old.ID); err != nil || again.Tries != 3 {
		t.Fatalf("retry: tries %d, %v; want 3", again.Tries, err)
	}
	if evs2, _ := s.Events(ctx, ticket.EventFilter{TicketID: refile.ID, Type: ticket.EventUpdated}); len(evs2) != len(evs) {
		t.Fatalf("retry wrote %d events", len(evs2)-len(evs))
	}
	// A second replacement of old would take refile's runs away.
	other := mustCreate(t, s, "other", "engineer", 1)
	_, err = upd(other.ID, old.ID)
	wantErr(t, err, ticket.ErrInvalid)
	// A link back up the chain would loop.
	move(t, s, refile.ID, ticket.StatusCancelled)
	_, err = upd(old.ID, refile.ID)
	wantErr(t, err, ticket.ErrInvalid)
	if o, _ := s.Get(ctx, old.ID); o.Tries != 2 {
		t.Fatalf("refused links changed old's tries: %d", o.Tries)
	}
}

// A link carries the old ticket's goal, as create --split-from does,
// unless the new ticket has one or the request sets one; and retrying a
// link made at create is a no-op, even while the old ticket is open.
func TestUpdateSplitFromCarriesTheGoal(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	mk := func(title, goal string) ticket.Ticket {
		t.Helper()
		tk, err := s.Create(ctx, ticket.CreateRequest{Title: title, Role: "engineer", Actor: "planner", Goal: goal})
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	link := func(req ticket.UpdateRequest) ticket.Ticket {
		t.Helper()
		req.Actor = "planner"
		got, err := s.Update(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	old := mk("old", "auth")
	move(t, s, old.ID, ticket.StatusCancelled)
	if got := link(ticket.UpdateRequest{ID: mk("bare", "").ID, SplitFrom: old.ID}); got.Goal != "auth" {
		t.Fatalf("bare re-file goal %q; want auth", got.Goal)
	}
	old2 := mk("old2", "auth")
	move(t, s, old2.ID, ticket.StatusCancelled)
	if got := link(ticket.UpdateRequest{ID: mk("own", "billing").ID, SplitFrom: old2.ID}); got.Goal != "billing" {
		t.Fatalf("a re-file's own goal changed to %q", got.Goal)
	}
	old3 := mk("old3", "auth")
	move(t, s, old3.ID, ticket.StatusCancelled)
	other := "speed"
	if got := link(ticket.UpdateRequest{ID: mk("asked", "").ID, SplitFrom: old3.ID, Goal: &other}); got.Goal != "speed" {
		t.Fatalf("--goal lost to the link: %q", got.Goal)
	}
	open := mk("open", "")
	kid, err := s.Create(ctx, ticket.CreateRequest{Title: "kid", Role: "engineer", Actor: "planner", SplitFrom: open.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := link(ticket.UpdateRequest{ID: kid.ID, SplitFrom: open.ID}); got.Tries != kid.Tries {
		t.Fatalf("retry of a create-time link: %+v", got)
	}
}
