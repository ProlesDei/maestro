package mongostore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// maxTxnLifetime is the most transactionLifetimeLimitSeconds doctor
// passes: a run killed mid-transaction holds the writer until the server
// ends its transaction.
const maxTxnLifetime = 10

// slowWrite is the write latency past which doctor warns.
const slowWrite = time.Second

// Doctor implements ticket.Store. It reports a moved board, or one it
// can't reach, as a failed check rather than an error.
func (s *Store) Doctor(ctx context.Context) (ticket.DoctorReport, error) {
	var rep ticket.DoctorReport
	add := func(name string, ok bool, format string, args ...any) *ticket.Check {
		rep.Checks = append(rep.Checks, ticket.Check{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...)})
		return &rep.Checks[len(rep.Checks)-1]
	}
	pctx, cancel := context.WithTimeout(ctx, selectTimeout+time.Second)
	err := s.client.c.Ping(pctx, readpref.Primary())
	cancel()
	if err != nil {
		add("connection", false, "can't reach the replica set's primary: %v", Redact(err, s.client.uri))
		return rep, nil
	}
	var build struct {
		Version string `bson:"version"`
	}
	s.client.c.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&build)
	add("connection", true, "MongoDB %s, database %s", build.Version, s.name)

	var w writerDoc
	if err := s.db.Collection(cMeta).FindOne(ctx, bson.D{{Key: "_id", Value: writerID}}).Decode(&w); err != nil {
		add("schema", false, "can't read the board's meta: %v", err)
		return rep, nil
	}
	if err := checkWriter(w); err != nil {
		add("schema", false, "%v", err)
		return rep, nil
	}
	add("schema", w.Schema == schemaVersion, "schema %d (want %d)", w.Schema, schemaVersion)

	var limit struct {
		Seconds int64 `bson:"transactionLifetimeLimitSeconds"`
	}
	err = s.client.c.Database("admin").RunCommand(ctx, bson.D{
		{Key: "getParameter", Value: 1}, {Key: "transactionLifetimeLimitSeconds", Value: 1},
	}).Decode(&limit)
	if err != nil {
		c := add("transaction_limit", true, "warning: can't read transactionLifetimeLimitSeconds (%v); set it to %d or less", err, maxTxnLifetime)
		c.Warn = true
	} else {
		add("transaction_limit", limit.Seconds <= maxTxnLifetime, "transactionLifetimeLimitSeconds = %d (want %d or less, or a killed run holds every writer that long)",
			limit.Seconds, maxTxnLifetime)
	}

	// A write that changes nothing but the writer document: transactions
	// work, and how long one takes from here.
	start := time.Now()
	err = s.write(ctx, func(tx *txn) error { return nil })
	took := time.Since(start)
	if err != nil {
		add("transactions", false, "a write transaction failed: %v", err)
		return rep, nil
	}
	c := add("transactions", true, "a write transaction took %s", took.Round(time.Millisecond))
	if took > slowWrite {
		c.Detail = fmt.Sprintf("warning: a write transaction took %s; writes line up one at a time, so the board handles about %.1f a second",
			took.Round(time.Millisecond), float64(time.Second)/float64(took))
		c.Warn = true
	}

	var tickets []ticket.Ticket
	type maxes struct{ tickets, comments, events, launches, nEvents int64 }
	var m maxes
	var now int64
	err = s.read(ctx, func(tx *txn) error {
		now = tx.now
		var docs []ticketDoc
		err := tx.coll(cTickets).find(bson.D{}, &docs, options.Find().SetProjection(bson.D{
			{Key: "status", Value: 1}, {Key: "role", Value: 1}, {Key: "blocked_from", Value: 1}, {Key: "parked", Value: 1},
			{Key: "lease", Value: 1}, {Key: "updated_at", Value: 1},
		}))
		if err != nil {
			return err
		}
		tickets = tickets[:0]
		for _, d := range docs {
			t := ticket.Ticket{ID: ticket.ID(d.ID), Status: ticket.Status(d.Status), Role: d.Role,
				BlockedFrom: ticket.Status(d.BlockedFrom), Parked: ticket.Parked(d.Parked), UpdatedAt: msTime(int64(d.UpdatedAt))}
			if d.Lease != nil {
				t.Claim = &ticket.Claim{ExpiresAt: msTime(int64(d.Lease.Expires))}
			}
			tickets = append(tickets, t)
		}
		for _, x := range []struct {
			coll string
			to   *int64
		}{{cTickets, &m.tickets}, {cComments, &m.comments}, {cEvents, &m.events}, {cLaunches, &m.launches}} {
			if *x.to, err = tx.maxID(x.coll); err != nil {
				return err
			}
		}
		m.nEvents, err = tx.coll(cEvents).count(bson.D{})
		if err != nil {
			return err
		}
		return tx.coll(cMeta).findOne(bson.D{{Key: "_id", Value: writerID}}, &w)
	})
	if err != nil {
		return rep, err
	}
	rep.Checks = append(rep.Checks, ticket.DoctorChecks(tickets, msTime(now))...)

	behind := ""
	for _, x := range []struct {
		name       string
		counter, n int64
	}{{"tickets", w.Tickets, m.tickets}, {"comments", w.Comments, m.comments}, {"events", w.Events, m.events}, {"launches", w.Launches, m.launches}} {
		if x.counter < x.n {
			behind += fmt.Sprintf(" %s counter %d < max id %d;", x.name, x.counter, x.n)
		}
	}
	if behind == "" {
		add("counters", true, "every counter is at or past its collection's largest id")
	} else {
		add("counters", false, "a new row would reuse an id:%s", behind)
	}
	add("events", m.nEvents == m.events, "last seq %d; %d events (want one per seq, no gaps)", m.events, m.nEvents)
	return rep, nil
}

// maxID returns the largest _id in a collection of numbered documents, 0
// for none.
func (tx *txn) maxID(coll string) (int64, error) {
	var d struct {
		ID int64 `bson:"_id"`
	}
	err := tx.coll(coll).findOne(bson.D{}, &d, options.FindOne().SetSort(bson.D{{Key: "_id", Value: -1}}).SetProjection(bson.D{{Key: "_id", Value: 1}}))
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, nil
	}
	return d.ID, err
}

// backupFormat names a backup file's layout.
const backupFormat = "maestro-ticket mongodb backup"

// backupHeader opens a backup file. Each collection follows as an array
// of documents in canonical Extended JSON, so types survive a restore.
type backupHeader struct {
	Format        string           `json:"format"`
	SchemaVersion int              `json:"schema_version"`
	Database      string           `json:"database"`
	TakenAt       time.Time        `json:"taken_at"`
	Counts        map[string]int64 `json:"counts"`
}

type backupFile struct {
	backupHeader
	Collections map[string][]json.RawMessage `json:"collections"`
}

// Backup implements ticket.Store: every collection, idempotency keys
// included, read in one snapshot and written as JSON to path.partial,
// then read back and checked, then linked to path.
func (s *Store) Backup(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	exists := fmt.Errorf("%w: backup %s already exists", ticket.ErrInvalid, abs)
	if _, err := os.Lstat(abs); err == nil {
		return exists
	}
	partial := abs + ".partial"
	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: backup %s is being written, or a crashed one left %s", ticket.ErrInvalid, abs, partial)
	}
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	defer os.Remove(partial)
	var out backupFile
	err = s.read(ctx, func(tx *txn) error {
		out = backupFile{
			backupHeader: backupHeader{Format: backupFormat, SchemaVersion: schemaVersion, Database: s.name,
				TakenAt: msTime(tx.now), Counts: map[string]int64{}},
			Collections: map[string][]json.RawMessage{},
		}
		for _, c := range collections {
			cur, err := tx.coll(c).cursor(bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
			if err != nil {
				return err
			}
			docs := []json.RawMessage{}
			for cur.Next(tx.ctx) {
				b, err := bson.MarshalExtJSON(cur.Current, true, false)
				if err != nil {
					cur.Close(tx.ctx)
					return err
				}
				docs = append(docs, b)
			}
			if err := cur.Err(); err != nil {
				cur.Close(tx.ctx)
				return err
			}
			cur.Close(tx.ctx)
			out.Collections[c] = docs
			out.Counts[c] = int64(len(docs))
		}
		return nil
	})
	if err != nil {
		f.Close()
		return fmt.Errorf("backup to %s: %w", abs, err)
	}
	bw := bufio.NewWriter(f)
	err = json.NewEncoder(bw).Encode(out)
	if err == nil {
		err = bw.Flush()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("backup to %s: %w", abs, err)
	}
	if err := checkBackup(partial, out.Counts); err != nil {
		return err
	}
	// Link, unlike rename, never replaces a file at path.
	err = os.Link(partial, abs)
	if errors.Is(err, fs.ErrExist) {
		return exists
	}
	if err != nil {
		if _, err := os.Lstat(abs); err == nil {
			return exists
		}
		if err := os.Rename(partial, abs); err != nil {
			return fmt.Errorf("backup to %s: %w", abs, err)
		}
	}
	return nil
}

// checkBackup reads a backup back and checks every document parses and
// every collection holds the count it was written with.
func checkBackup(path string, counts map[string]int64) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("check backup %s: %w", path, err)
	}
	var got backupFile
	if err := json.Unmarshal(b, &got); err != nil {
		return fmt.Errorf("check backup %s: %w", path, err)
	}
	if got.Format != backupFormat || got.SchemaVersion != schemaVersion {
		return fmt.Errorf("backup %s failed its check: format %q, schema %d", path, got.Format, got.SchemaVersion)
	}
	for _, c := range collections {
		docs := got.Collections[c]
		if int64(len(docs)) != counts[c] {
			return fmt.Errorf("backup %s failed its check: %s holds %d documents, not %d", path, c, len(docs), counts[c])
		}
		for _, d := range docs {
			var doc bson.D
			if err := bson.UnmarshalExtJSON(d, true, &doc); err != nil {
				return fmt.Errorf("backup %s failed its check: %s: %w", path, c, err)
			}
		}
	}
	return nil
}

// Export implements ticket.Store.
func (s *Store) Export(ctx context.Context) (ticket.Export, error) {
	var out ticket.Export
	err := s.read(ctx, func(tx *txn) error {
		out = ticket.Export{SchemaVersion: schemaVersion, Tickets: []ticket.Detail{}, Events: []ticket.Event{}}
		byID := map[ticket.ID]int{}
		var docs []ticketDoc
		if err := tx.coll(cTickets).find(bson.D{}, &docs, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})); err != nil {
			return err
		}
		ts, err := tx.toTickets(docs)
		if err != nil {
			return err
		}
		for _, t := range ts {
			byID[t.ID] = len(out.Tickets)
			out.Tickets = append(out.Tickets, ticket.Detail{Ticket: t, Comments: []ticket.Comment{}})
		}
		var cs []commentDoc
		if err := tx.coll(cComments).find(bson.D{}, &cs, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})); err != nil {
			return err
		}
		for _, c := range cs {
			if i, ok := byID[ticket.ID(c.TicketID)]; ok {
				out.Tickets[i].Comments = append(out.Tickets[i].Comments, toComment(c))
			}
		}
		var evs []eventDoc
		if err := tx.coll(cEvents).find(bson.D{}, &evs, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})); err != nil {
			return err
		}
		for _, e := range evs {
			out.Events = append(out.Events, toEvent(e))
			out.LastSeq = e.Seq
		}
		var runs []runDoc
		if err := tx.coll(cRuns).find(bson.D{}, &runs, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}})); err != nil {
			return err
		}
		out.Runs = []ticket.Run{}
		for _, r := range runs {
			out.Runs = append(out.Runs, toRun(r))
		}
		var ls []launchDoc
		if err := tx.coll(cLaunches).find(bson.D{}, &ls, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})); err != nil {
			return err
		}
		out.Launches = []ticket.Launch{}
		for _, l := range ls {
			out.Launches = append(out.Launches, toLaunch(l))
		}
		out.ETAs, err = tx.etas()
		return err
	})
	return out, err
}
