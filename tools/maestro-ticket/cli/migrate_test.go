package cli

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/sqlitestore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/storetest"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func stubPing(t *testing.T, err error) {
	old := pingMongo
	t.Cleanup(func() { pingMongo = old })
	pingMongo = func(ctx context.Context, uri string) error {
		if err != nil {
			return fmt.Errorf("%w: dial %s", err, uri)
		}
		return nil
	}
}

// store --uri-file writes store.uri 0600 after the URI connects and
// prints only its host; --clear removes it; a bad URI, one that doesn't
// connect, or a SQLite board that never moved is refused.
func TestStoreURICommand(t *testing.T) {
	stubPing(t, nil)
	h := newHarness(t)
	dir := filepath.Join(h.dir, "fresh")
	h.env["MAESTRO_TICKET_STORE"] = filepath.Join(dir, "tickets.db")
	code, out, errs := h.run(testURI+"\n", "store", "--uri-file", "-", "--json")
	var res struct{ Kind, Host, Path string }
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || res.Kind != kindMongoDB || res.Host != "db.example.net:27017" {
		t.Fatalf("store: exit %d %s %s", code, out, errs)
	}
	noSecret(t, "store", out, errs)
	fi, err := os.Stat(filepath.Join(dir, uriFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("store.uri: %v %v", fi, err)
	}
	if di, err := os.Stat(dir); err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("its folder: %v %v", di, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, uriFile)); string(b) != testURI+"\n" {
		t.Fatalf("store.uri holds %q", b)
	}
	code, out, _ = h.run("", "store")
	if code != 0 || !strings.Contains(out, "mongodb at db.example.net:27017") {
		t.Fatalf("store: exit %d %s", code, out)
	}
	noSecret(t, "store", out)
	h.ok(&res, "store", "--clear")
	if res.Kind != kindSQLite {
		t.Fatalf("clear: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, uriFile)); !os.IsNotExist(err) {
		t.Fatalf("store.uri left: %v", err)
	}

	for name, body := range map[string]string{"empty": "\n", "no scheme": "db.example.net:27017", "two": testURI + " " + testURI} {
		if code, out, errs := h.run(body, "store", "--uri-file", "-", "--json"); code != 2 {
			t.Fatalf("%s: exit %d %s %s", name, code, out, errs)
		}
	}
	stubPing(t, ticket.ErrInvalid)
	code, out, errs = h.run(testURI, "store", "--uri-file", "-", "--json")
	if code != 2 || !strings.Contains(out, "dial db.example.net:27017") {
		t.Fatalf("unreachable: exit %d %s %s", code, out, errs)
	}
	noSecret(t, "store", out, errs)
	if _, err := os.Stat(filepath.Join(dir, uriFile)); !os.IsNotExist(err) {
		t.Fatalf("store.uri written for a URI that doesn't connect: %v", err)
	}

	stubPing(t, nil)
	h.ok(nil, "create", "--title", "a")
	code, out, _ = h.run(testURI, "store", "--uri-file", "-", "--json")
	if code != 2 || !strings.Contains(out, "never moved") || !strings.Contains(out, "migrate --to mongodb") {
		t.Fatalf("unmoved board: exit %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, uriFile)); !os.IsNotExist(err) {
		t.Fatalf("store.uri written beside an unmoved board: %v", err)
	}
}

// migrateBoard is a SQLite board to move and a MongoDB database of its
// own to move it to.
type migrateBoard struct {
	h    *harness
	clk  *storetest.Clock
	uri  string // names db
	db   string
	rich storetest.Rich
}

func newMigrateBoard(t *testing.T) *migrateBoard {
	t.Helper()
	base := os.Getenv("MAESTRO_TICKET_TEST_MONGO")
	if base == "" {
		t.Skip("set MAESTRO_TICKET_TEST_MONGO to a replica set URI to run the migrate tests")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 8)
	rand.Read(b)
	db := "p4mig_" + hex.EncodeToString(b)
	u.Path = "/" + db
	t.Cleanup(func() {
		c, err := mongo.Connect(options.Client().ApplyURI(base))
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Disconnect(context.Background())
		c.Database(db).Drop(context.Background())
	})
	m := &migrateBoard{h: newHarness(t), clk: storetest.NewClock(), uri: u.String(), db: db}
	m.h.now = m.clk.Now
	s := m.sqlite(t)
	m.rich = storetest.FillBoard(t, s, m.clk)
	s.Close()
	return m
}

func (m *migrateBoard) path() string { return filepath.Join(m.h.dir, "tickets.db") }

func (m *migrateBoard) sqlite(t *testing.T) *sqlitestore.Store {
	t.Helper()
	s, err := sqlitestore.Open(context.Background(), m.path(), sqlitestore.Options{Now: m.clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// mongo opens the MongoDB board; with mover, as this machine's move.
func (m *migrateBoard) mongo(t *testing.T, mover bool) *mongostore.Store {
	t.Helper()
	opts := mongostore.Options{Now: m.clk.Now}
	if mover {
		host, err := HostID(m.h.dir)
		if err != nil {
			t.Fatal(err)
		}
		opts.Mover = host
	}
	s, err := mongostore.Open(context.Background(), m.uri, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func (m *migrateBoard) stop(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(m.h.dir, "STOP"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// board reads the SQLite file's rows.
func (m *migrateBoard) board(t *testing.T) ticket.Board {
	t.Helper()
	s := m.sqlite(t)
	defer s.Close()
	var b ticket.Board
	err := s.Move(context.Background(), func(mv *sqlitestore.Mover) error {
		var err error
		b, err = mv.Board()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (m *migrateBoard) migrate(stdin string, args ...string) (int, moveReport, string) {
	code, out, errs := m.h.run(stdin, append([]string{"migrate"}, append(args, "--json")...)...)
	var rep moveReport
	json.Unmarshal([]byte(out), &rep)
	return code, rep, out + errs
}

// A rich board goes to MongoDB and back with every row as it was: lease
// tokens, idempotency keys, counters. Old handles get "board moved", a
// retried key replays, and no launch id is given twice.
func TestMigrateRoundTrip(t *testing.T) {
	m := newMigrateBoard(t)
	ctx := context.Background()
	// A launch id handed out whose row is gone, as a run record keeps one.
	raw, err := sql.Open("sqlite", m.path())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("UPDATE sqlite_sequence SET seq = seq + 5 WHERE name = 'launches'"); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	before := m.board(t)
	old := m.sqlite(t)
	defer old.Close()
	m.h.ok(nil, "stop", "--all") // on SQLite, this machine's STOP

	code, rep, out := m.migrate(m.uri, "--to", "mongodb", "--uri-file", "-")
	if code != 0 || rep.Already || rep.Tables["tickets"].Rows != 5 || rep.LastSeq != before.LastSeq() || rep.Database != m.db {
		t.Fatalf("migrate: exit %d %+v\n%s", code, rep, out)
	}
	if strings.Contains(out, m.uri) {
		t.Fatalf("migrate printed the uri: %s", out)
	}
	if fi, err := os.Stat(filepath.Join(m.h.dir, uriFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("store.uri: %v %v", fi, err)
	}
	_, err = old.Create(ctx, ticket.CreateRequest{Title: "late", Actor: "planner"})
	if !errors.Is(err, ticket.ErrInvalid) || !strings.Contains(err.Error(), "board moved to mongodb") {
		t.Fatalf("old handle: %v", err)
	}
	// Every launch carries this machine's id now, and stop --all's STOP
	// became the board's halt.
	host, _ := HostID(m.h.dir)
	mv := m.mongo(t, true)
	if on, err := mv.Halted(ctx); !on || err != nil {
		t.Fatalf("halt after the move: %v %v", on, err)
	}
	in, err := mv.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range before.Launches {
		before.Launches[i].Host = host
	}
	if d := before.Diff(in); d != "" {
		t.Fatalf("MongoDB's board: %s", d)
	}
	// The CLI reads MongoDB now; a rerun checks the copy and changes nothing.
	var list struct{ Tickets []ticket.Ticket }
	m.h.ok(&list, "list")
	if len(list.Tickets) != 5 {
		t.Fatalf("list on MongoDB: %d tickets", len(list.Tickets))
	}
	if code, rep, out := m.migrate("", "--to", "mongodb"); code != 0 || !rep.Already {
		t.Fatalf("rerun: exit %d %+v %s", code, rep, out)
	}

	s := m.mongo(t, false)
	again, err := s.Create(ctx, m.rich.Create)
	if err != nil || again.ID != m.rich.Keyed.ID {
		t.Fatalf("replay on MongoDB: %+v %v", again, err)
	}
	l, err := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "jse", PID: 7})
	if err != nil || l.ID != m.rich.Launches+6 {
		t.Fatalf("launch id %d, want %d: %v", l.ID, m.rich.Launches+6, err)
	}

	// Back: the halt first, then no open runs.
	m.h.ok(nil, "stop", "--all", "--off")
	if on, err := mv.Halted(ctx); on || err != nil {
		t.Fatalf("halt after stop --all --off: %v %v", on, err)
	}
	if code, _, out := m.migrate("", "--to", "sqlite"); code != 4 || !strings.Contains(out, "stop --all") {
		t.Fatalf("no halt: exit %d %s", code, out)
	}
	// A handout and the pause go with the board, each way.
	m.h.ok(nil, "stop", "--all")
	stop := filepath.Join(m.h.dir, "STOP")
	if b, err := os.ReadFile(stop); err != nil || string(b) != haltBody {
		t.Fatalf("stop --all made no STOP of its own here: %q %v", b, err)
	}
	co := boardCoord(storeLoc{localDir: m.h.dir}, mv)
	if err := co.create(handoutMarks, "T-2.jse", "build T-2\n"); err != nil {
		t.Fatal(err)
	}
	if err := co.setFlag(pauseFlag, []byte(`{"until":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if code, _, out := m.migrate("", "--to", "sqlite"); code != 4 || !strings.Contains(out, fmt.Sprintf("launch %d", l.ID)) {
		t.Fatalf("open launch: exit %d %s", code, out)
	}
	if _, err := s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: l.ID, Outcome: ticket.LaunchNoResult}); err != nil {
		t.Fatal(err)
	}
	out1, err := mv.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code, rep, out = m.migrate("", "--to", "sqlite")
	if code != 0 || rep.Tables["launches"].Rows != len(before.Launches)+1 {
		t.Fatalf("migrate back: exit %d %+v\n%s", code, rep, out)
	}
	if _, err := os.Stat(stop); !os.IsNotExist(err) {
		t.Fatalf("stop --all's STOP left after the move back: %v", err)
	}
	local := localCoord(storeLoc{localDir: m.h.dir})
	if h, err := local.read(handoutMarks, "T-2.jse"); err != nil || h.body != "build T-2\n" {
		t.Fatalf("handout after the move back: %+v %v", h, err)
	}
	if b, err := local.flag(pauseFlag); err != nil || string(b) != `{"until":"x"}` {
		t.Fatalf("pause after the move back: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(m.h.dir, uriFile)); !os.IsNotExist(err) {
		t.Fatalf("store.uri left: %v", err)
	}
	_, err = s.List(ctx, ticket.ListFilter{})
	if err == nil || !strings.Contains(err.Error(), "board moved") {
		t.Fatalf("MongoDB after the move back: %v", err)
	}
	for i := range out1.Launches {
		out1.Launches[i].Host = ""
	}
	back := m.board(t)
	if d := out1.Diff(back); d != "" {
		t.Fatalf("SQLite's board: %s", d)
	}
	for i := range before.Launches {
		before.Launches[i].Host = ""
	}
	if d := before.Diff(ticket.Board{Tickets: back.Tickets, Comments: back.Comments, Events: back.Events,
		Idempotency: back.Idempotency, Runs: back.Runs, Launches: back.Launches[:len(before.Launches)],
		ETAs: back.ETAs, Counters: before.Counters}); d != "" {
		t.Fatalf("the round trip changed a row: %s", d)
	}
	// Plain SQLite again: writes go through, ids go on.
	var tk ticket.Ticket
	m.h.ok(&tk, "create", "--title", "after")
	if tk.ID != 6 {
		t.Fatalf("next ticket %s", tk.ID)
	}
	if code, _, _ := m.migrate("", "--to", "sqlite"); code != 2 {
		t.Fatalf("a second move back: exit %d", code)
	}
}

// Each thing migrate waits on refuses it and leaves the board as it was.
func TestMigrateRefusals(t *testing.T) {
	m := newMigrateBoard(t)
	ctx := context.Background()
	wantRefused := func(want int, has string) {
		t.Helper()
		code, _, out := m.migrate(m.uri, "--to", "mongodb", "--uri-file", "-")
		if code != want || !strings.Contains(out, has) {
			t.Fatalf("exit %d; want %d with %q:\n%s", code, want, has, out)
		}
		if _, err := os.Stat(filepath.Join(m.h.dir, uriFile)); !os.IsNotExist(err) {
			t.Fatalf("store.uri written: %v", err)
		}
		s := m.sqlite(t)
		defer s.Close()
		if gone, err := s.Moved(ctx); gone || err != nil {
			t.Fatalf("the file moved: %v %v", gone, err)
		}
	}
	wantRefused(4, "STOP")
	s := m.sqlite(t)
	c, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: 1, Holder: "jse/s9", TTL: time.Hour, Actor: "jse"})
	if err != nil {
		t.Fatal(err)
	}
	m.stop(t)
	wantRefused(4, "T-1 (jse/s9)")
	if _, err := s.Release(ctx, ticket.ReleaseRequest{ID: 1, Holder: "jse/s9", Token: c.Claim.Token, Actor: "jse"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	r, err := runs.Create(filepath.Join(m.h.dir, "runs"), runs.Record{Launcher: "jse", StartedAt: time.Now()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantRefused(4, r.ID)
	for _, ext := range []string{".json", ".in"} {
		os.Remove(filepath.Join(m.h.dir, "runs", r.ID+ext))
	}

	other := m.mongo(t, false)
	if _, err := other.Create(ctx, ticket.CreateRequest{Title: "elsewhere", Actor: "planner"}); err != nil {
		t.Fatal(err)
	}
	wantRefused(2, "holds a board already")
	if code, _, out := m.migrate("", "--to", "sqlite"); code != 2 || !strings.Contains(out, "on SQLite already") {
		t.Fatalf("to sqlite from sqlite: exit %d %s", code, out)
	}
	if code, _, _ := m.migrate("", "--to", "postgres"); code != 2 {
		t.Fatalf("--to postgres: exit %d", code)
	}
}

// A move cut short leaves SQLite as it was and usable; a rerun finishes
// it, from before the SQLite commit or after it.
func TestMigrateInterrupted(t *testing.T) {
	m := newMigrateBoard(t)
	ctx := context.Background()
	m.stop(t)
	// Killed mid-copy: the board marked moving, half of it written, the
	// SQLite transaction gone with the process.
	half := m.board(t)
	half.Tickets, half.Events, half.Comments = half.Tickets[:2], half.Events[:3], nil
	plain := m.mongo(t, false)
	mv := m.mongo(t, true)
	if err := mv.StartMove(ctx, mongostore.MoveIn); err != nil {
		t.Fatal(err)
	}
	if err := mv.Load(ctx, half); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Get(ctx, 1); err == nil || !strings.Contains(err.Error(), "board moving") {
		t.Fatalf("MongoDB mid-move: %v", err)
	}
	var tk ticket.Ticket
	m.h.ok(&tk, "create", "--title", "written after the cut")
	if tk.ID != 6 {
		t.Fatalf("SQLite after the cut: %s", tk.ID)
	}

	code, rep, out := m.migrate(m.uri, "--to", "mongodb", "--uri-file", "-")
	if code != 0 || rep.Tables["tickets"].Rows != 6 {
		t.Fatalf("rerun: exit %d %+v\n%s", code, rep, out)
	}
	if got, err := plain.Get(ctx, 6); err != nil || got.Title != "written after the cut" {
		t.Fatalf("T-6 on MongoDB: %+v %v", got, err)
	}

	// Killed after the SQLite commit, before the moving mark came off.
	host, _ := HostID(m.h.dir)
	c, err := mongo.Connect(options.Client().ApplyURI(m.uri))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect(ctx)
	if _, err := c.Database(m.db).Collection("meta").UpdateOne(ctx, bson.D{{Key: "_id", Value: "writer"}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "moving", Value: mongostore.MoveIn}, {Key: "moving_host", Value: host}}}}); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := m.h.run("", "list", "--json"); code != 2 || !strings.Contains(out, "board moving") {
		t.Fatalf("list mid-move: exit %d %s", code, out)
	}
	if code, rep, out := m.migrate("", "--to", "mongodb"); code != 0 || rep.Tables["tickets"].Rows != 6 {
		t.Fatalf("rerun after the commit: exit %d %+v\n%s", code, rep, out)
	}
	m.h.ok(nil, "list")
}

// A move to SQLite cut short, before its copy or after it, is called off
// with --abort: the MongoDB board takes writes again from every machine,
// the file takes none, and the move can run again later.
func TestMigrateAbort(t *testing.T) {
	m := newMigrateBoard(t)
	ctx := context.Background()
	m.stop(t)
	if code, _, out := m.migrate(m.uri, "--to", "mongodb", "--uri-file", "-"); code != 0 {
		t.Fatalf("migrate: exit %d %s", code, out)
	}
	m.h.ok(nil, "stop", "--all")
	plain := m.mongo(t, false)
	mv := m.mongo(t, true)
	if code, _, out := m.migrate("", "--to", "sqlite", "--abort"); code != 2 || !strings.Contains(out, "no move to sqlite") {
		t.Fatalf("nothing to call off: exit %d %s", code, out)
	}
	if code, _, _ := m.migrate("", "--to", "mongodb", "--abort"); code != 2 {
		t.Fatalf("--abort with --to mongodb: exit %d", code)
	}
	for _, copied := range []bool{false, true} {
		if err := mv.StartMove(ctx, mongostore.MoveOut); err != nil {
			t.Fatal(err)
		}
		if copied {
			b, err := mv.Board(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for i := range b.Launches {
				b.Launches[i].Host = ""
			}
			s := m.sqlite(t)
			if err := s.Move(ctx, func(mo *sqlitestore.Mover) error { return mo.Replace(b) }); err != nil {
				t.Fatal(err)
			}
			s.Close()
		}
		if _, err := plain.List(ctx, ticket.ListFilter{}); err == nil || !strings.Contains(err.Error(), "board moving") {
			t.Fatalf("mid-move: %v", err)
		}
		if !copied {
			if code, rep, out := m.migrate("", "--to", "sqlite", "--abort"); code != 0 || !rep.Aborted {
				t.Fatalf("abort: exit %d %s", code, out)
			}
		} else if code, out, errs := m.h.run("", "migrate", "--to", "sqlite", "--abort"); code != 0 || !strings.Contains(out, "stop --all --off") {
			t.Fatalf("abort after the copy: exit %d %s %s", code, out, errs)
		}
		if _, err := plain.List(ctx, ticket.ListFilter{}); err != nil {
			t.Fatalf("MongoDB after the abort: %v", err)
		}
		s := m.sqlite(t)
		gone, err := s.Moved(ctx)
		s.Close()
		if !gone || err != nil {
			t.Fatalf("the file takes writes after the abort (copied %v): %v", copied, err)
		}
	}
	if _, err := os.Stat(filepath.Join(m.h.dir, uriFile)); err != nil {
		t.Fatalf("store.uri after the abort: %v", err)
	}
	m.h.ok(nil, "list")
	if code, _, out := m.migrate("", "--to", "sqlite"); code != 0 {
		t.Fatalf("migrate after the abort: exit %d %s", code, out)
	}
}

// A file that moved already holds no board of its own: copying it into a
// second, empty database would fork the board, so migrate refuses.
func TestMigrateMovedFileRefusesANewDatabase(t *testing.T) {
	m := newMigrateBoard(t)
	m.stop(t)
	if code, _, out := m.migrate(m.uri, "--to", "mongodb", "--uri-file", "-"); code != 0 {
		t.Fatalf("migrate: exit %d %s", code, out)
	}
	m.h.ok(nil, "store", "--clear")
	u, _ := url.Parse(m.uri)
	other := m.db + "_b"
	u.Path = "/" + other
	t.Cleanup(func() {
		c, err := mongo.Connect(options.Client().ApplyURI(m.uri))
		if err != nil {
			return
		}
		defer c.Disconnect(context.Background())
		c.Database(other).Drop(context.Background())
	})
	code, _, out := m.migrate(u.String(), "--to", "mongodb", "--uri-file", "-")
	if code != 2 || !strings.Contains(out, "moved to MongoDB already") {
		t.Fatalf("a second database: exit %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(m.h.dir, uriFile)); !os.IsNotExist(err) {
		t.Fatalf("store.uri written: %v", err)
	}
}

// Once one machine moved the board back to SQLite, a rerun there finishes
// it, but no other machine may copy the tombstoned board into a SQLite
// file of its own: that would fork the board.
func TestMigrateBackOnlyOnTheMover(t *testing.T) {
	m := newMigrateBoard(t)
	m.stop(t)
	if code, _, out := m.migrate(m.uri, "--to", "mongodb", "--uri-file", "-"); code != 0 {
		t.Fatalf("migrate: exit %d %s", code, out)
	}
	b := newHarness(t)
	b.now = m.clk.Now
	if code, out, errs := b.run(m.uri, "store", "--uri-file", "-", "--json"); code != 0 {
		t.Fatalf("store on B: exit %d %s %s", code, out, errs)
	}
	b.ok(nil, "list")
	m.h.ok(nil, "stop", "--all")
	if code, _, out := m.migrate("", "--to", "sqlite"); code != 0 {
		t.Fatalf("migrate back: exit %d %s", code, out)
	}
	// Cut short before store.uri went: a rerun here finishes it.
	if err := writeStoreURI(m.h.dir, m.uri); err != nil {
		t.Fatal(err)
	}
	if code, rep, out := m.migrate("", "--to", "sqlite"); code != 0 || !rep.Already {
		t.Fatalf("rerun on the mover: exit %d %+v %s", code, rep, out)
	}
	code, out, errs := b.run("", "migrate", "--to", "sqlite", "--json")
	if code != 2 || !strings.Contains(out, "another machine") || !strings.Contains(out, "store --clear") {
		t.Fatalf("migrate back on B: exit %d %s %s", code, out, errs)
	}
	if got := (&migrateBoard{h: b, clk: m.clk}).board(t); !got.Empty() {
		t.Fatalf("B has a SQLite board: %d tickets", len(got.Tickets))
	}
}
