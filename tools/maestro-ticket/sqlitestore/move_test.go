package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/storetest"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// A board copied into an empty file comes out row for row, counters and
// idempotency keys too, and the copy goes on from where the board was.
func TestMoveReplaceRoundTrip(t *testing.T) {
	ctx := context.Background()
	src, clk := newStore(t)
	rich := storetest.FillBoard(t, src, clk)
	// A launch id handed out, in a run record, whose row is gone.
	if _, err := src.w.ExecContext(ctx, "UPDATE sqlite_sequence SET seq = seq + 5 WHERE name = 'launches'"); err != nil {
		t.Fatal(err)
	}
	var b ticket.Board
	var ex ticket.Export
	err := src.Move(ctx, func(m *Mover) error {
		var err error
		if b, err = m.Board(); err != nil {
			return err
		}
		ex, err = m.Export()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Tickets) != 5 || len(b.Events) == 0 || len(b.Idempotency) != 3 || b.Counters.Launches != rich.Launches+5 {
		t.Fatalf("board: %d tickets, %d events, %d keys, counters %+v", len(b.Tickets), len(b.Events), len(b.Idempotency), b.Counters)
	}
	if tk := b.Tickets[0]; tk.Lease != nil || tk.Token != 2 || strings.Join(tk.Areas, ",") != "tools/a,tools/b" {
		t.Fatalf("T-1 row: %+v", tk)
	}

	dst, err := Open(ctx, filepath.Join(t.TempDir(), "tickets.db"), Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	var got ticket.Board
	var gotEx ticket.Export
	err = dst.Move(ctx, func(m *Mover) error {
		if err := m.Block(); err != nil {
			return err
		}
		if err := m.Replace(b); err != nil {
			return err
		}
		var err error
		if got, err = m.Board(); err != nil {
			return err
		}
		gotEx, err = m.Export()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := b.Diff(got); d != "" {
		t.Fatalf("copy differs: %s", d)
	}
	a, _ := json.Marshal(ex)
	c, _ := json.Marshal(gotEx)
	if string(a) != string(c) {
		t.Fatalf("exports differ:\n%s\n%s", a, c)
	}
	if gone, err := dst.Moved(ctx); err != nil || gone {
		t.Fatalf("Replace left the moved triggers: %v %v", gone, err)
	}
	// A retried key replays; new ids follow the counters.
	again, err := dst.Create(ctx, rich.Create)
	if err != nil || again.ID != rich.Keyed.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	l, err := dst.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "jse", PID: 9})
	if err != nil || l.ID != rich.Launches+6 {
		t.Fatalf("launch id %d, want %d: %v", l.ID, rich.Launches+6, err)
	}
	tk, err := dst.Create(ctx, ticket.CreateRequest{Title: "next", Actor: "planner"})
	if err != nil || tk.ID != ticket.ID(len(b.Tickets)+1) {
		t.Fatalf("next ticket %v: %v", tk.ID, err)
	}
	if _, err := dst.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "x", Actor: "planner"}); err != nil {
		t.Fatal(err)
	}
	// Events stay append-only after the swap.
	if _, err := dst.w.ExecContext(ctx, "DELETE FROM events WHERE seq = 1"); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("delete an event: %v", err)
	}
}

// Once blocked, every write fails with ErrInvalid that says the board
// moved, from a handle opened before too; reads still work, and doctor
// fails.
func TestMoveBlockRefusesWrites(t *testing.T) {
	ctx := context.Background()
	s, clk := newStore(t)
	storetest.FillBoard(t, s, clk)
	old, err := Open(ctx, s.Path(), Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := s.Move(ctx, func(m *Mover) error { return m.Block() }); err != nil {
		t.Fatal(err)
	}
	for _, h := range []*Store{s, old} {
		_, err := h.Create(ctx, ticket.CreateRequest{Title: "x", Actor: "planner"})
		wantErr(t, err, ticket.ErrInvalid)
		if !strings.Contains(err.Error(), "board moved to mongodb") || !strings.Contains(err.Error(), "store.uri") {
			t.Fatalf("create: %v", err)
		}
		_, err = h.Comment(ctx, ticket.CommentRequest{ID: 1, Body: "x", Actor: "planner"})
		wantErr(t, err, ticket.ErrInvalid)
		_, err = h.LogRun(ctx, ticket.LogRunRequest{Session: "s", Actor: "a"})
		wantErr(t, err, ticket.ErrInvalid)
		if _, err := h.Get(ctx, 1); err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	if _, err := s.w.ExecContext(ctx, "DELETE FROM etas"); err == nil {
		t.Fatal("a raw delete got through")
	}
	rep, err := s.Doctor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || !strings.Contains(rep.Checks[len(rep.Checks)-1].Detail, "board moved") {
		t.Fatalf("doctor: %+v", rep)
	}
	if gone, err := s.Moved(ctx); err != nil || !gone {
		t.Fatalf("Moved: %v %v", gone, err)
	}
}

// A move whose fn fails leaves the file as it was.
func TestMoveRollsBack(t *testing.T) {
	ctx := context.Background()
	s, clk := newStore(t)
	storetest.FillBoard(t, s, clk)
	err := s.Move(ctx, func(m *Mover) error {
		if err := m.Replace(ticket.Board{}); err != nil {
			return err
		}
		if err := m.Block(); err != nil {
			return err
		}
		return sql.ErrTxDone
	})
	if err != sql.ErrTxDone {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "after", Actor: "planner"}); err != nil {
		t.Fatal(err)
	}
	if tk, err := s.Get(ctx, 1); err != nil || tk.Title != "base" {
		t.Fatalf("T-1: %+v %v", tk, err)
	}
}
