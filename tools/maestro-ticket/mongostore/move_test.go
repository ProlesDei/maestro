package mongostore

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/storetest"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// testHost is the host id a test's moves run from.
const testHost = "0123456789abcdef0123456789abcdef"

func moverStore(t *testing.T, uri, db string, now func() time.Time) *Store {
	t.Helper()
	return moverOn(t, uri, db, now, testHost)
}

func moverOn(t *testing.T, uri, db string, now func() time.Time, host string) *Store {
	t.Helper()
	s, err := Open(context.Background(), uri, Options{Database: db, Now: now, Mover: host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A board loaded into an empty database reads back row for row, with the
// fields queries use copied out right: revivals, splits and the latest
// comment's start. A load cut short is finished by the next.
func TestMoveLoadRoundTrip(t *testing.T) {
	ctx := context.Background()
	clk := storetest.NewClock()
	src, uri := newStore(t, Options{Now: clk.Now})
	rich := storetest.FillBoard(t, src, clk)
	b, err := src.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range b.Events {
		types = append(types, e.Type)
	}
	for _, want := range []string{ticket.EventLeaseRevived, ticket.EventLeaseExpired} {
		if !slices.Contains(types, want) {
			t.Fatalf("the fill made no %s event: %v", want, types)
		}
	}
	srcEx, err := src.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b.Launches[0].Host = "0123456789abcdef0123456789abcdef"

	db := freshDB(t, uri)
	dst := moverStore(t, uri, db, clk.Now)
	plain, err := Open(ctx, uri, Options{Database: db, Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	wantErr(t, dst.Load(ctx, b), ticket.ErrInvalid) // not marked moving
	if err := dst.StartMove(ctx, MoveIn); err != nil {
		t.Fatal(err)
	}
	// Cut short: half the events and some tickets are in.
	half := b
	half.Events = b.Events[:len(b.Events)/2]
	half.Tickets = b.Tickets[:2]
	half.Comments = nil
	if err := dst.Load(ctx, half); err != nil {
		t.Fatal(err)
	}
	_, err = plain.Get(ctx, 1)
	wantErr(t, err, ticket.ErrInvalid)
	if !strings.Contains(err.Error(), "board moving") {
		t.Fatalf("read mid-move: %v", err)
	}
	if err := dst.Load(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := dst.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d := b.Diff(got); d != "" {
		t.Fatalf("copy differs: %s", d)
	}
	if err := dst.EndMove(ctx, false); err != nil {
		t.Fatal(err)
	}
	gotEx, err := plain.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	x, _ := json.Marshal(srcEx)
	y, _ := json.Marshal(gotEx)
	if string(x) != string(y) {
		t.Fatalf("exports differ:\n%s\n%s", x, y)
	}
	// What the copy keeps beyond Export: revivals, splits, latest.
	var host struct {
		Host string `bson:"host"`
	}
	if err := plain.db.Collection(cLaunches).FindOne(ctx, bson.D{{Key: "_id", Value: b.Launches[0].ID}}).Decode(&host); err != nil || host.Host != b.Launches[0].Host {
		t.Fatalf("host: %+v %v", host, err)
	}
	rows, err := plain.Runs(ctx, ticket.RunFilter{})
	if err != nil {
		t.Fatal(err)
	}
	srcRows, _ := src.Runs(ctx, ticket.RunFilter{})
	if x, y := mustJSON(t, srcRows), mustJSON(t, rows); x != y {
		t.Fatalf("runs differ:\n%s\n%s", x, y)
	}
	long, err := plain.List(ctx, ticket.ListFilter{LatestPrefix: "Review: GO " + strings.Repeat("é long note ", 10)})
	if err != nil || len(long) != 1 {
		t.Fatalf("latest prefix: %v %v", long, err)
	}
	again, err := plain.Create(ctx, rich.Create)
	if err != nil || again.ID != rich.Keyed.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	l, err := plain.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "jse", PID: 9})
	if err != nil || l.ID != rich.Launches+1 {
		t.Fatalf("launch %d: %v", l.ID, err)
	}
	// The board is in use now: a second move in is refused.
	wantErr(t, dst.StartMove(ctx, MoveIn), ticket.ErrInvalid)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Events already in that don't match the board refuse the load.
func TestMoveLoadRefusesOtherEvents(t *testing.T) {
	ctx := context.Background()
	clk := storetest.NewClock()
	src, uri := newStore(t, Options{Now: clk.Now})
	storetest.FillBoard(t, src, clk)
	b, err := src.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dst := moverStore(t, uri, freshDB(t, uri), clk.Now)
	if err := dst.StartMove(ctx, MoveIn); err != nil {
		t.Fatal(err)
	}
	if err := dst.Load(ctx, b); err != nil {
		t.Fatal(err)
	}
	b.Events[0].Actor = "someone else"
	wantErr(t, dst.Load(ctx, b), ticket.ErrInvalid)
	other := moverOn(t, uri, dst.Database(), clk.Now, "fedcba9876543210fedcba9876543210")
	if err := other.Load(ctx, b); err == nil || !strings.Contains(err.Error(), "another machine") {
		t.Fatalf("another machine's load: %v", err)
	}
}

// Moving out holds every other caller off; the tombstone then refuses
// them for good, while the mover still reads.
func TestMoveOutTombstone(t *testing.T) {
	ctx := context.Background()
	clk := storetest.NewClock()
	s, uri := newStore(t, Options{Now: clk.Now})
	storetest.FillBoard(t, s, clk)
	mv := moverStore(t, uri, s.Database(), clk.Now)
	wantErr(t, mv.StartMove(ctx, MoveIn), ticket.ErrInvalid) // not empty
	if err := mv.StartMove(ctx, MoveOut); err != nil {
		t.Fatal(err)
	}
	wantErr(t, mv.StartMove(ctx, MoveIn), ticket.ErrInvalid)
	// Another machine's mover can't join or end this move.
	other := moverOn(t, uri, s.Database(), clk.Now, "fedcba9876543210fedcba9876543210")
	for _, err := range []error{other.StartMove(ctx, MoveOut), other.EndMove(ctx, true)} {
		if !errors.Is(err, ticket.ErrInvalid) || !strings.Contains(err.Error(), "another machine") {
			t.Fatalf("other machine: %v", err)
		}
	}
	_, err := s.Create(ctx, ticket.CreateRequest{Title: "x", Actor: "planner"})
	wantErr(t, err, ticket.ErrInvalid)
	if err := mv.EndMove(ctx, true); err != nil {
		t.Fatal(err)
	}
	_, err = s.List(ctx, ticket.ListFilter{})
	if err == nil || !strings.Contains(err.Error(), "board moved") {
		t.Fatalf("list after the move: %v", err)
	}
	if st, err := mv.State(ctx); st.To != "" || !st.Moved || err != nil {
		t.Fatalf("state: %+v %v", st, err)
	}
	if b, err := mv.Board(ctx); err != nil || len(b.Tickets) != 5 {
		t.Fatalf("mover's read: %d %v", len(b.Tickets), err)
	}
}
