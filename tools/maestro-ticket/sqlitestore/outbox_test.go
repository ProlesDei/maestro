package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

type outboxRow struct {
	eventID, attempts, nextAttempt int64
	kind, payload                  string
	delivered                      sql.NullInt64
}

func outboxRows(t *testing.T, s *Store) []outboxRow {
	t.Helper()
	rows, err := s.r.Query("SELECT event_id, kind, payload, attempts, next_attempt_at, delivered_at FROM outbox ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.eventID, &r.kind, &r.payload, &r.attempts, &r.nextAttempt, &r.delivered); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// Every event gets exactly one outbox row, queued for delivery, whose
// payload is the event as Events returns it.
func TestOutboxRowPerEvent(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	c, _ := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", TTL: time.Minute})
	s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "hi", Holder: "jse/s1", Token: c.Claim.Token})
	clk.Advance(2 * time.Minute)
	s.Reap(ctx)

	evs, _ := s.Events(ctx, ticket.EventFilter{})
	rows := outboxRows(t, s)
	if len(evs) != 4 || len(rows) != len(evs) {
		t.Fatalf("%d events, %d outbox rows; want 4 each", len(evs), len(rows))
	}
	for i, e := range evs {
		r := rows[i]
		want, _ := json.Marshal(e)
		if r.eventID != e.Seq || r.kind != e.Type || r.attempts != 0 || r.delivered.Valid ||
			r.nextAttempt != e.CreatedAt.UnixMilli() || r.payload != string(want) {
			t.Errorf("outbox row %+v for event %s", r, want)
		}
	}
	if _, err := s.w.Exec("INSERT INTO outbox (event_id, kind, payload, next_attempt_at) VALUES (1, 'x', '{}', 0)"); err == nil {
		t.Error("outbox accepted a second row for one event")
	}
	rep, err := s.Doctor(ctx)
	if err != nil || !rep.OK() {
		t.Fatalf("doctor %+v, %v", rep, err)
	}
}

// The outbox row is part of the write: if it can't be queued, the ticket
// change and its event roll back too.
func TestOutboxInSameTransaction(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	if _, err := s.w.Exec(`CREATE TRIGGER outbox_fail BEFORE INSERT ON outbox
		BEGIN SELECT RAISE(ABORT, 'outbox down'); END`); err != nil {
		t.Fatal(err)
	}
	before, _ := s.LastSeq(ctx)
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: tk.ID, To: ticket.StatusInProgress}); err == nil {
		t.Fatal("transition succeeded without its outbox row")
	}
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "y"}); err == nil {
		t.Fatal("create succeeded without its outbox row")
	}
	if after, _ := s.LastSeq(ctx); after != before {
		t.Fatal("event committed without its outbox row")
	}
	got, _ := s.Get(ctx, tk.ID)
	all, _ := s.List(ctx, ticket.ListFilter{})
	if got.Status != ticket.StatusTodo || got.Version != tk.Version || len(all) != 1 {
		t.Fatalf("ticket changed without its outbox row: %+v, %d tickets", got.Ticket, len(all))
	}
}

// Migrating an old store queues no history: events from before the outbox
// existed are backfilled as delivered.
func TestMigrateBackfillsOutboxDelivered(t *testing.T) {
	path := openV1(t)
	ctx := context.Background()
	s, err := Open(ctx, path, Options{Now: func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows := outboxRows(t, s)
	if len(rows) != 1 || rows[0].eventID != 1 || rows[0].kind != "created" || !rows[0].delivered.Valid {
		t.Fatalf("backfilled outbox %+v; want one delivered row for event 1", rows)
	}
	var e ticket.Event
	if err := json.Unmarshal([]byte(rows[0].payload), &e); err != nil || e.Seq != 1 || e.TicketID != 1 || e.Type != "created" {
		t.Fatalf("backfilled payload %s: %+v, %v", rows[0].payload, e, err)
	}
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: 1, Body: "new"}); err != nil {
		t.Fatal(err)
	}
	rows = outboxRows(t, s)
	if len(rows) != 2 || rows[1].delivered.Valid {
		t.Fatalf("new event after migration not queued: %+v", rows)
	}
}
