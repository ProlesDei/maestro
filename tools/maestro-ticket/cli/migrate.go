package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/sqlitestore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// maxURI caps what --uri-file reads.
const maxURI = 4096

// pingMongo checks a URI reaches a replica set before store writes it.
// Tests swap it for a fake.
var pingMongo = mongostore.Ping

// readURI reads a MongoDB URI from file, or stdin for "-", and checks its
// form. No error it returns holds the URI.
func (c *call) readURI(file string) (string, error) {
	var b []byte
	var err error
	if file == "-" {
		b, err = io.ReadAll(io.LimitReader(c.env.Stdin, maxURI+1))
	} else {
		var f *os.File
		if f, err = os.Open(file); err == nil {
			b, err = io.ReadAll(io.LimitReader(f, maxURI+1))
			f.Close()
		}
	}
	if err != nil {
		return "", usagef("read --uri-file: %v", err)
	}
	uri := strings.TrimSpace(string(b))
	switch {
	case uri == "":
		return "", usagef("the URI file is empty; store --clear removes store.uri")
	case len(b) > maxURI:
		return "", usagef("the URI file is over %d bytes", maxURI)
	case !strings.HasPrefix(uri, "mongodb://") && !strings.HasPrefix(uri, "mongodb+srv://"):
		return "", usagef("the URI file holds no mongodb:// or mongodb+srv:// URI")
	case strings.ContainsFunc(uri, func(r rune) bool { return r <= ' ' || r == 0x7f }):
		return "", usagef("the URI file holds more than one URI on one line")
	}
	return uri, nil
}

// writeStoreURI writes store.uri in dir, readable by its owner alone,
// whole, through a rename, as runs' secrets are.
func writeStoreURI(dir, uri string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-store-uri-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(uri + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, uriFile))
}

// runStoreURI writes, removes or shows store.uri, which makes the store
// MongoDB. The URI goes in from a file or stdin, never argv, and only its
// host prints.
func runStoreURI(ctx context.Context, c *call) error {
	file := c.fs.String("uri-file", "", "read the MongoDB URI from this file, or - for stdin")
	clr := c.fs.Bool("clear", false, "remove store.uri: the store is SQLite again")
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	if *file != "" && *clr {
		return usagef("store: use --uri-file or --clear, not both")
	}
	// The folder, not location: a store.uri that is there but broken is
	// what --uri-file and --clear fix.
	path, _, err := c.storePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	switch {
	case *file != "":
		uri, err := c.readURI(*file)
		if err != nil {
			return err
		}
		if err := c.unmovedBoard(ctx, path); err != nil {
			return err
		}
		if err := pingMongo(ctx, uri); err != nil {
			return redactURI(err, uri)
		}
		if err := writeStoreURI(dir, uri); err != nil {
			return err
		}
	case *clr:
		if err := os.Remove(filepath.Join(dir, uriFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	loc, err := c.resolveLoc()
	if err != nil {
		return err
	}
	res := map[string]any{"kind": loc.kind, "path": loc.sqlitePath}
	if loc.kind == kindMongoDB {
		res["path"] = filepath.Join(loc.localDir, uriFile)
		res["host"] = uriHost(loc.uri)
	}
	return c.out(res, func(w io.Writer) {
		if loc.kind == kindMongoDB {
			fmt.Fprintf(w, "store: mongodb at %s (%s)\n", uriHost(loc.uri), res["path"])
			return
		}
		fmt.Fprintf(w, "store: sqlite at %s\n", loc.sqlitePath)
	})
}

// unmovedBoard refuses a store.uri beside a SQLite board with tickets that
// never moved: every session here would turn to an empty MongoDB board,
// a second one, and the move would then find it written.
func (c *call) unmovedBoard(ctx context.Context, path string) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	s, err := sqlitestore.Open(ctx, path, sqlitestore.Options{Now: c.env.Now})
	if err != nil {
		return err
	}
	defer s.Close()
	seq, err := s.LastSeq(ctx)
	if err != nil {
		return err
	}
	gone, err := s.Moved(ctx)
	if err != nil || seq == 0 || gone {
		return err
	}
	return fmt.Errorf("%w: %s holds a SQLite board that never moved; move it with maestro-ticket migrate --to mongodb --uri-file -, which writes store.uri once the copy checks out",
		ticket.ErrInvalid, path)
}

// sqliteDrops clears the fields of a launch only MongoDB keeps, which a
// move to SQLite drops, so the two stores' Exports compare.
func sqliteDrops(l *ticket.Launch) {
	l.Host, l.Job, l.Tickets, l.Limit, l.Lifts = "", "", nil, "", time.Time{}
}

// moveReport is what a move checked: each table's rows and hash, the
// last seq and the counters, the same on both stores.
type moveReport struct {
	To       string                `json:"to"`
	Host     string                `json:"host"`
	Database string                `json:"database"`
	Tables   map[string]tableCheck `json:"tables"`
	LastSeq  int64                 `json:"last_seq"`
	Counters ticket.Counters       `json:"counters"`
	// Already says an earlier run had made the copy; this one checked it.
	Already bool   `json:"already,omitempty"`
	Kept    string `json:"kept,omitempty"` // the SQLite file left as the way back
}

type tableCheck struct {
	Rows int    `json:"rows"`
	Hash string `json:"hash"`
}

// checkCopy compares a board with its copy row by row and table by
// table, and the two stores' Exports, which read every ticket, comment,
// event, run, launch and ETA as callers see them.
func checkCopy(from, to ticket.Board, fromEx, toEx ticket.Export) (map[string]tableCheck, error) {
	if d := from.Diff(to); d != "" {
		return nil, fmt.Errorf("%w: the copy doesn't match the board: %s; nothing moved", ticket.ErrInvalid, d)
	}
	a, err := exportText(fromEx)
	if err != nil {
		return nil, err
	}
	b, err := exportText(toEx)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(a, b) {
		return nil, fmt.Errorf("%w: the copy's rows match, but its Export doesn't; nothing moved", ticket.ErrInvalid)
	}
	ha, hb := from.Hashes(), to.Hashes()
	out := map[string]tableCheck{}
	counts := from.Counts()
	for _, t := range ticket.BoardTables {
		if ha[t] != hb[t] {
			return nil, fmt.Errorf("%w: the copy's %s hash doesn't match; nothing moved", ticket.ErrInvalid, t)
		}
		if t != "counters" {
			out[t] = tableCheck{Rows: counts[t], Hash: ha[t][:16]}
		}
	}
	return out, nil
}

// exportText is an Export as both stores can hold it: no schema version,
// which each numbers its own way, and no launch field SQLite drops.
func exportText(e ticket.Export) ([]byte, error) {
	e.SchemaVersion = 0
	e.Launches = slices.Clone(e.Launches)
	for i := range e.Launches {
		sqliteDrops(&e.Launches[i])
	}
	return json.Marshal(e)
}

// runMigrate moves the board between SQLite and MongoDB.
func runMigrate(ctx context.Context, c *call) error {
	to := c.fs.String("to", "", "mongodb or sqlite")
	file := c.fs.String("uri-file", "", "with --to mongodb, the MongoDB URI's file, or - for stdin, when store.uri isn't written")
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	var rep moveReport
	var err error
	switch *to {
	case kindMongoDB:
		rep, err = c.migrateIn(ctx, *file)
	case kindSQLite:
		if *file != "" {
			return usagef("migrate: --uri-file goes with --to mongodb")
		}
		rep, err = c.migrateOut(ctx)
	default:
		return usagef("migrate: want --to mongodb or --to sqlite")
	}
	if err != nil {
		return err
	}
	return c.out(rep, func(w io.Writer) { printMove(w, rep) })
}

func printMove(w io.Writer, r moveReport) {
	n := func(t string) int { return r.Tables[t].Rows }
	how := "moved"
	if r.Already {
		how = "checked the earlier copy:"
	}
	fmt.Fprintf(w, "%s the board to %s", how, r.To)
	if r.To == kindMongoDB {
		fmt.Fprintf(w, " at %s, database %s", r.Host, r.Database)
	}
	fmt.Fprintf(w, ": %d tickets, %d comments, %d events (last seq %d), %d idempotency keys, %d runs, %d launches, %d ETAs; every table matches row by row\n",
		n("tickets"), n("comments"), n("events"), r.LastSeq, n("idempotency"), n("runs"), n("launches"), n("etas"))
	if r.To == kindMongoDB {
		fmt.Fprintf(w, "%s stays as the way back and takes no writes. Next: store: mongodb in install.yaml, apply, check, then delete STOP (stop --all --off, if stop --all made it)\n", r.Kept)
	} else {
		fmt.Fprintln(w, "the MongoDB board is marked moved and store.uri is gone. Next: store: sqlite in install.yaml, apply, check; on each other machine, store --clear")
	}
}

// liveHere refuses while a run on this machine is live, or STOP is
// missing: a run started after the copy would write to a board that took
// no more writes.
func (c *call) liveHere(loc storeLoc) error {
	stop := filepath.Join(loc.localDir, sqlitestore.StopFile)
	if _, err := os.Lstat(stop); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: migrate needs new work stopped: create %s, wait until maestro-ticket runs shows none running, then run it again", ticket.ErrStatusConflict, stop)
	}
	list, err := runs.List(filepath.Join(loc.localDir, "runs"), time.Now(), true)
	if err != nil {
		return err
	}
	var live []string
	for _, r := range list {
		if !r.Ended() {
			live = append(live, r.ID)
		}
	}
	if len(live) > 0 {
		return fmt.Errorf("%w: %d runs still live here (%s); wait until maestro-ticket runs shows none running", ticket.ErrStatusConflict, len(live), strings.Join(live, ", "))
	}
	return nil
}

// noClaims reaps s and refuses while a ticket holds a live claim: its
// holder would heartbeat the board left behind.
func noClaims(ctx context.Context, s ticket.Store) error {
	if _, err := s.Reap(ctx); err != nil {
		return err
	}
	yes := true
	held, err := s.List(ctx, ticket.ListFilter{Claimed: &yes})
	if err != nil {
		return err
	}
	if len(held) > 0 {
		ids := make([]string, len(held))
		for i, t := range held {
			ids[i] = t.ID.String() + " (" + t.Claim.Holder + ")"
		}
		return fmt.Errorf("%w: %d tickets hold a live claim: %s; wait until each is released or runs out", ticket.ErrStatusConflict, len(held), strings.Join(ids, ", "))
	}
	return nil
}

// openMover opens the MongoDB board as this machine's move's own store,
// and returns this machine's host id.
func (c *call) openMover(ctx context.Context, loc storeLoc, uri string) (*mongostore.Store, string, error) {
	host, err := HostID(loc.localDir)
	if err != nil {
		return nil, "", err
	}
	s, err := mongostore.Open(ctx, uri, mongostore.Options{Now: c.env.Now, Mover: host,
		StopFile: filepath.Join(loc.localDir, sqlitestore.StopFile)})
	if err != nil {
		return nil, "", redactURI(err, uri)
	}
	return s, host, nil
}

// movingElsewhere refuses a move another machine runs.
func movingElsewhere(st mongostore.Move, host string) error {
	if st.To != "" && st.Host != host {
		return fmt.Errorf("%w: maestro-ticket migrate --to %s is under way on another machine (host id %s); finish it there", ticket.ErrInvalid, st.To, st.Host)
	}
	return nil
}

// migrateIn moves the SQLite board here to MongoDB: in one SQLite write
// transaction, it reads every table, copies it into an empty MongoDB
// board marked moving, checks the copy, blocks every write to the file
// and writes store.uri; then, the transaction committed, it lifts the
// moving mark. Cut short anywhere, a rerun finishes it.
func (c *call) migrateIn(ctx context.Context, file string) (moveReport, error) {
	rep := moveReport{To: kindMongoDB}
	loc, err := c.location()
	if err != nil {
		return rep, err
	}
	uri := loc.uri
	if file != "" {
		u, err := c.readURI(file)
		if err != nil {
			return rep, err
		}
		if uri != "" && u != uri {
			return rep, fmt.Errorf("%w: %s names another MongoDB board (%s); remove it with maestro-ticket store --clear, or leave out --uri-file",
				ticket.ErrInvalid, filepath.Join(loc.localDir, uriFile), uriHost(uri))
		}
		uri = u
	}
	if uri == "" {
		return rep, usagef("migrate --to mongodb: give the MongoDB URI with --uri-file F|-")
	}
	rep.Host, rep.Kept = uriHost(uri), loc.sqlitePath
	if _, err := os.Stat(loc.sqlitePath); errors.Is(err, fs.ErrNotExist) {
		return rep, fmt.Errorf("%w: no SQLite board at %s to move", ticket.ErrNotFound, loc.sqlitePath)
	}
	if err := envcheck.Err(c.envChecks(loc.sqlitePath)); err != nil {
		return rep, err
	}
	if err := c.liveHere(loc); err != nil {
		return rep, err
	}
	sq, err := sqlitestore.Open(ctx, loc.sqlitePath, sqlitestore.Options{Now: c.env.Now})
	if err != nil {
		return rep, err
	}
	defer sq.Close()
	gone, err := sq.Moved(ctx)
	if err != nil {
		return rep, err
	}
	// A moved file takes no writes, reap's included; its claims were
	// checked before it moved.
	if !gone {
		if err := noClaims(ctx, sq); err != nil {
			return rep, err
		}
	}
	mg, host, err := c.openMover(ctx, loc, uri)
	if err != nil {
		return rep, err
	}
	defer mg.Close()
	rep.Database = mg.Database()
	err = sq.Move(ctx, func(m *sqlitestore.Mover) error {
		b, err := m.Board()
		if err != nil {
			return err
		}
		// Every launch here is this machine's; on a shared board, say so.
		for i := range b.Launches {
			if b.Launches[i].Host == "" {
				b.Launches[i].Host = host
			}
		}
		st, err := mg.State(ctx)
		if err != nil {
			return redactURI(err, uri)
		}
		if err := movingElsewhere(st, host); err != nil {
			return err
		}
		switch {
		case st.Moved:
			return fmt.Errorf("%w: database %s was moved back to SQLite already; give migrate a new database", ticket.ErrInvalid, rep.Database)
		case st.To == mongostore.MoveOut:
			return fmt.Errorf("%w: maestro-ticket migrate --to sqlite is moving database %s out; finish that first", ticket.ErrInvalid, rep.Database)
		case st.To == "":
			have, err := mg.Board(ctx)
			if err != nil {
				return redactURI(err, uri)
			}
			if !have.Empty() {
				if d := b.Diff(have); d != "" {
					return fmt.Errorf("%w: database %s holds a board already, not this one (%s); give migrate an empty database", ticket.ErrInvalid, rep.Database, d)
				}
				rep.Already = true
				break
			}
			if err := mg.StartMove(ctx, mongostore.MoveIn); err != nil {
				return redactURI(err, uri)
			}
			fallthrough
		default:
			if err := mg.Load(ctx, b); err != nil {
				return redactURI(err, uri)
			}
		}
		got, err := mg.Board(ctx)
		if err != nil {
			return redactURI(err, uri)
		}
		toEx, err := mg.Export(ctx)
		if err != nil {
			return redactURI(err, uri)
		}
		fromEx, err := m.Export()
		if err != nil {
			return err
		}
		if rep.Tables, err = checkCopy(b, got, fromEx, toEx); err != nil {
			return err
		}
		rep.LastSeq, rep.Counters = b.LastSeq(), b.Counters
		if !gone {
			if err := m.Block(); err != nil {
				return err
			}
		}
		if loc.uri != uri {
			return writeStoreURI(loc.localDir, uri)
		}
		return nil
	})
	if err != nil {
		return rep, err
	}
	// Before another machine may use the board: a STOP that stop --all
	// made halts every machine now, and the marks go with the board.
	if b, err := os.ReadFile(filepath.Join(loc.localDir, sqlitestore.StopFile)); err == nil && string(b) == haltBody {
		if _, err := mg.SetHalt(ctx, true); err != nil {
			return rep, fmt.Errorf("the board is on MongoDB, but still marked moving: %w; run migrate --to mongodb again", redactURI(err, uri))
		}
	}
	if err := carryMarks(localCoord(loc), boardCoord(loc, mg)); err != nil {
		return rep, fmt.Errorf("the board is on MongoDB, but still marked moving: %w; run migrate --to mongodb again", redactURI(err, uri))
	}
	if err := mg.EndMove(ctx, false); err != nil {
		return rep, fmt.Errorf("the board is on MongoDB, but it is still marked moving: %w; run migrate --to mongodb again", redactURI(err, uri))
	}
	return rep, nil
}

// localCoord is the coordStore of the files beside the SQLite board, and
// boardCoord that of the MongoDB board mg, as coord picks them.
func localCoord(loc storeLoc) fileCoord {
	return fileCoord{dir: loc.localDir, handouts: filepath.Join(loc.localDir, "handouts"), runs: filepath.Join(loc.localDir, "runs")}
}

func boardCoord(loc storeLoc, mg *mongostore.Store) sharedCoord {
	return sharedCoord{local: localCoord(loc), board: func() (*mongostore.Store, error) { return mg, nil }}
}

// carryMarks copies the marks sessions share, the handouts and the pause,
// from the store a move leaves to the one it moves to, so a line a look
// printed before the move isn't printed again after it. Each mark's age
// starts again; one the target holds already stays.
func carryMarks(from, to coordStore) error {
	ms, err := from.list(handoutMarks)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, m := range ms {
		if strings.Contains(m.name, ".stale") || strings.Contains(m.name, ".tmp") || strings.HasSuffix(m.name, ".takeover") {
			continue
		}
		got, err := from.read(handoutMarks, m.name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := to.create(handoutMarks, m.name, got.body); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	b, err := from.flag(pauseFlag)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return to.setFlag(pauseFlag, b)
}

// migrateOut moves the MongoDB board back to the SQLite file here: it
// marks the board moving, so no machine writes to it, copies it into the
// file in one SQLite write transaction that empties it and drops its
// moved triggers, checks the copy, then tombstones the MongoDB board,
// removes store.uri and lifts the halt. Cut short anywhere, a rerun
// finishes it.
func (c *call) migrateOut(ctx context.Context) (moveReport, error) {
	rep := moveReport{To: kindSQLite}
	loc, err := c.location()
	if err != nil {
		return rep, err
	}
	if loc.kind != kindMongoDB {
		return rep, fmt.Errorf("%w: the board is on SQLite already: no %s", ticket.ErrInvalid, filepath.Join(loc.localDir, uriFile))
	}
	rep.Host, rep.Kept = uriHost(loc.uri), loc.sqlitePath
	if err := envcheck.Err(c.envChecks(loc.sqlitePath)); err != nil {
		return rep, err
	}
	mg, host, err := c.openMover(ctx, loc, loc.uri)
	if err != nil {
		return rep, err
	}
	defer mg.Close()
	rep.Database = mg.Database()
	st, err := mg.State(ctx)
	if err != nil {
		return rep, redactURI(err, loc.uri)
	}
	if err := movingElsewhere(st, host); err != nil {
		return rep, err
	}
	done := st.Moved
	switch {
	case st.To == mongostore.MoveIn:
		return rep, fmt.Errorf("%w: maestro-ticket migrate --to mongodb never finished; run it again first", ticket.ErrInvalid)
	case st.To == "" && !done:
		on, err := mg.Halted(ctx)
		if err != nil {
			return rep, redactURI(err, loc.uri)
		}
		if !on {
			return rep, fmt.Errorf("%w: migrate needs every machine halted: run maestro-ticket stop --all, wait until no machine has a run going, then run it again", ticket.ErrStatusConflict)
		}
		open, err := mg.OpenLaunches(ctx)
		if err != nil {
			return rep, redactURI(err, loc.uri)
		}
		if len(open) > 0 {
			ids := make([]string, len(open))
			for i, l := range open {
				ids[i] = fmt.Sprintf("launch %d (%s)", l.ID, dash(l.Actor))
			}
			return rep, fmt.Errorf("%w: %d runs are still open on the board: %s; wait until no machine has a run going", ticket.ErrStatusConflict, len(open), strings.Join(ids, ", "))
		}
		if err := noClaims(ctx, mg); err != nil {
			return rep, redactURI(err, loc.uri)
		}
		if err := mg.StartMove(ctx, mongostore.MoveOut); err != nil {
			return rep, redactURI(err, loc.uri)
		}
	}
	b, err := mg.Board(ctx)
	if err != nil {
		return rep, redactURI(err, loc.uri)
	}
	for i := range b.Launches {
		b.Launches[i].Host = ""
	}
	fromEx, err := mg.Export(ctx)
	if err != nil {
		return rep, redactURI(err, loc.uri)
	}
	sq, err := sqlitestore.Open(ctx, loc.sqlitePath, sqlitestore.Options{Now: c.env.Now})
	if err != nil {
		return rep, err
	}
	defer sq.Close()
	err = sq.Move(ctx, func(m *sqlitestore.Mover) error {
		gone, err := m.Moved()
		if err != nil {
			return err
		}
		have, err := m.Board()
		if err != nil {
			return err
		}
		switch {
		case !gone && !have.Empty() && b.Diff(have) != "":
			return fmt.Errorf("%w: %s holds a SQLite board that never moved to MongoDB; move it aside, then run migrate --to sqlite again", ticket.ErrInvalid, loc.sqlitePath)
		case !gone && !have.Empty():
			rep.Already = true // an earlier run's copy, committed
		default:
			if err := m.Replace(b); err != nil {
				return err
			}
		}
		got, err := m.Board()
		if err != nil {
			return err
		}
		toEx, err := m.Export()
		if err != nil {
			return err
		}
		rep.Tables, err = checkCopy(b, got, fromEx, toEx)
		rep.LastSeq, rep.Counters = b.LastSeq(), b.Counters
		return err
	})
	if err != nil {
		return rep, err
	}
	if err := carryMarks(boardCoord(loc, mg), localCoord(loc)); err != nil {
		return rep, fmt.Errorf("the board is copied back, but its marks aren't: %w; run migrate --to sqlite again", redactURI(err, loc.uri))
	}
	if !done {
		if err := mg.EndMove(ctx, true); err != nil {
			return rep, fmt.Errorf("the board is copied back, but MongoDB's isn't marked moved: %w; run migrate --to sqlite again", redactURI(err, loc.uri))
		}
	}
	if err := os.Remove(filepath.Join(loc.localDir, uriFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return rep, err
	}
	// The board's halt goes with its tombstone; this machine's copy of it
	// goes now, as stop --all --off would take it. A STOP made by hand
	// stays.
	mirrorHalt(loc.localDir, false)
	return rep, nil
}
