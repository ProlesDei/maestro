// Package mongostore implements ticket.Store on a MongoDB replica set, so
// machines that can't share a disk share one board.
//
// Concurrency model: every write runs in a transaction whose first step
// updates one meta document, "writer". Two write transactions can't both
// change it, so MongoDB lets one through and aborts the other with a
// transient write conflict, which this package retries: writers line up one
// at a time, as SQLite's BEGIN IMMEDIATE makes them. The same document
// holds the server's clock and the id and seq counters, so ids stay dense
// and events commit in seq order. Reads run in snapshot transactions and
// never block writers.
//
// Collections: tickets (with deps, areas, lease and the latest comment's
// start on each), comments, events, idempotency, runs, launches, etas and
// meta. Events are only ever inserted, never updated or deleted, so once
// the board is set up, a server role with insert and find alone on events
// is enough.
package mongostore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/connstring"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// schemaVersion is the layout this build reads and writes. A store on a
// newer one is refused with ErrStoreTooNew.
const schemaVersion = 1

// setupVersion is the collections and indexes this build makes. Raise it
// when build makes something new, so each board builds it on its next
// open.
const setupVersion = 1

// DefaultDatabase holds the board when the URI names no database.
const DefaultDatabase = "maestro"

// txnBudget is how long a call keeps retrying transactions that lost to
// another writer before it gives up with ErrBusy. Tests shorten it.
var txnBudget = 30 * time.Second

// maxBackoff is the longest a lost transaction waits before its next try.
// Writers take turns, so a burst of them queues for seconds; waits that
// grow this far keep their failed tries from flooding the server, and no
// further, so a waiter isn't still asleep long after the board frees.
const maxBackoff = 2 * time.Second

const (
	// commitLimit bounds the commit retries of one transaction.
	commitLimit = 10 * time.Second
	// selectTimeout is how long a call waits to find the server.
	selectTimeout = 5 * time.Second
	// maxPool caps a client's connections to each server. A call waiting
	// for one spends its transaction's deadline, so it fits the most
	// calls one process makes at once: ringOrphans' 8. The pool opens a
	// connection only when calls overlap, so a process making one call at
	// a time holds one.
	maxPool = 8
)

// idleClose is how long a client no call uses stays connected. Tests
// shorten it.
var idleClose = time.Minute

// txnLimit is each transaction's own deadline: under the server's
// transactionLifetimeLimitSeconds, which doctor wants at 10 or less, so a
// call that stalls gives up before the server kills it. Tests shorten it.
var txnLimit = 8 * time.Second

// Collection names.
const (
	cMeta        = "meta"
	cTickets     = "tickets"
	cComments    = "comments"
	cEvents      = "events"
	cIdempotency = "idempotency"
	cRuns        = "runs"
	cLaunches    = "launches"
	cETAs        = "etas"
)

var collections = []string{cMeta, cTickets, cComments, cEvents, cIdempotency, cRuns, cLaunches, cETAs}

// writerID is the meta document every write transaction updates first.
const writerID = "writer"

// writerDoc is the meta "writer" document: the schema, the moved
// tombstone, a move under way, the halt, the server clock as of the last
// write, the last id each counter gave out, and the code each host's
// binary last wrote with.
type writerDoc struct {
	ID       string        `bson:"_id"`
	Schema   int           `bson:"schema"`
	Moved    bool          `bson:"moved"`
	Moving   string        `bson:"moving,omitempty"`      // the store a migrate is moving the board to
	MoveHost string        `bson:"moving_host,omitempty"` // the machine that migrate runs on, or that moved the board back
	Halt     bool          `bson:"halt"`
	Writes   int64         `bson:"writes"`
	Now      bson.DateTime `bson:"now"`
	Tickets  int64         `bson:"tickets"`
	Comments int64         `bson:"comments"`
	Events   int64         `bson:"events"`
	Launches int64         `bson:"launches"`
	// Code is the newest code any binary wrote with; Hosts, each host
	// id's last.
	Code  int            `bson:"code"`
	Hosts map[string]int `bson:"hosts"`
	// Setup is the setupVersion the board was last set up at.
	Setup int `bson:"setup"`
}

// Code is this build's code: it goes up when what this package writes
// changes so that every machine's binary should be rebuilt to match.
// doctor warns when a host writes with an older one. 1 was the first
// MongoDB store; 2 added shared marks, the halt and launch hosts.
const Code = 2

// Options tune a Store. The zero value is fine for production.
type Options struct {
	// Now returns the current time. Nil (production) uses the server's
	// clock. Tests inject a fake one.
	Now func() time.Time
	// Database names the database; empty takes the URI's, else
	// DefaultDatabase.
	Database string
	// StopFile is the kill switch's path: while a file is there, every
	// claim and lease revival is refused. Empty checks none.
	StopFile string
	// Host is this machine's id: the writer document keeps the code each
	// host last wrote with, for doctor. Empty records none.
	Host string
	// Mover, set to this machine's host id, marks migrate's own store: it
	// alone may open and use a board that is moving, or has moved, to the
	// other store, and only its machine's move goes on.
	Mover string
	// Create makes a board in a database that holds none: migrate's move
	// in does, and tests. Without it, Open refuses such a database, so a
	// URI naming the wrong one can't start a second, empty board.
	Create bool
}

// Store is a ticket.Store on one MongoDB database.
type Store struct {
	client *sharedClient
	name   string
	now    func() time.Time
	stop   string
	host   string
	mover  string
	closed sync.Once
}

var _ ticket.Store = (*Store)(nil)

// sharedClient is one process's client for one URI: stores opened on the
// same URI share it, and the last Close disconnects it. A client no call
// has used for idleClose disconnects too, and the next call connects it
// again, so a process that lives as long as a run holds no connection
// between its calls.
type sharedClient struct {
	uri  string
	refs int // guarded by clients

	mu     sync.Mutex
	c      *mongo.Client // nil until a call connects it
	busy   int           // calls using c
	idle   *time.Timer   // disconnects c once no call uses it
	closed bool          // the last store closed
}

var clients = struct {
	sync.Mutex
	m map[string]*sharedClient
}{m: map[string]*sharedClient{}}

// Open connects to the replica set at uri (reusing this process's client
// for it), sets up the board if an older build set it up, or if it is new
// and opts.Create is set, and checks its schema. No error it returns holds
// the URI.
func Open(ctx context.Context, uri string, opts Options) (*Store, error) {
	s, err := open(ctx, uri, opts)
	if err != nil {
		return nil, Redact(err, uri)
	}
	return s, nil
}

func open(ctx context.Context, uri string, opts Options) (*Store, error) {
	cs, err := connstring.ParseAndValidate(uri)
	if err != nil {
		return nil, fmt.Errorf("%w: bad MongoDB URI: %v", ticket.ErrInvalid, err)
	}
	name := opts.Database
	if name == "" {
		name = cs.Database
	}
	if name == "" {
		name = DefaultDatabase
	}
	if strings.ContainsAny(opts.Host, ".$ ") {
		return nil, fmt.Errorf("%w: bad host id %q", ticket.ErrInvalid, opts.Host)
	}
	s := &Store{client: acquire(uri), name: name, now: opts.Now, stop: opts.StopFile, host: opts.Host, mover: opts.Mover}
	if err := s.setup(ctx, opts.Create); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func acquire(uri string) *sharedClient {
	clients.Lock()
	defer clients.Unlock()
	sc := clients.m[uri]
	if sc == nil {
		sc = &sharedClient{uri: uri}
		clients.m[uri] = sc
	}
	sc.refs++
	return sc
}

// use returns the client, connected, for a call, which ends with release.
func (sc *sharedClient) use() (*mongo.Client, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.closed {
		return nil, mongo.ErrClientDisconnected
	}
	if sc.c == nil {
		// Defaults first, so the URI's own settings win. Polling watches
		// each server on one connection, not two.
		c, err := mongo.Connect(options.Client().SetServerSelectionTimeout(selectTimeout).SetMaxPoolSize(maxPool).
			SetServerMonitoringMode(options.ServerMonitoringModePoll).ApplyURI(sc.uri))
		if err != nil {
			return nil, fmt.Errorf("connect to MongoDB: %w", err)
		}
		sc.c = c
	}
	if sc.idle != nil {
		sc.idle.Stop()
		sc.idle = nil
	}
	sc.busy++
	return sc.c, nil
}

// release ends a call use began, and disconnects the client once no call
// has used it for idleClose.
func (sc *sharedClient) release() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.busy--; sc.busy == 0 && sc.c != nil && !sc.closed {
		sc.idle = time.AfterFunc(idleClose, sc.closeIdle)
	}
}

// closeIdle disconnects the client if no call took it up since release.
func (sc *sharedClient) closeIdle() {
	sc.mu.Lock()
	c := sc.c
	if sc.busy > 0 || c == nil {
		sc.mu.Unlock()
		return
	}
	sc.c, sc.idle = nil, nil
	sc.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), selectTimeout)
	defer cancel()
	c.Disconnect(ctx)
}

// conn returns the store's database on the connected client for a call,
// and done, which ends the call.
func (s *Store) conn() (*mongo.Database, func(), error) {
	c, err := s.client.use()
	if err != nil {
		return nil, nil, Redact(err, s.client.uri)
	}
	return c.Database(s.name), s.client.release, nil
}

// Close releases the store's share of the client. It is safe to call
// twice.
func (s *Store) Close() error {
	var err error
	s.closed.Do(func() {
		clients.Lock()
		defer clients.Unlock()
		sc := s.client
		if sc.refs--; sc.refs > 0 {
			return
		}
		delete(clients.m, sc.uri)
		sc.mu.Lock()
		c := sc.c
		sc.c, sc.closed = nil, true
		if sc.idle != nil {
			sc.idle.Stop()
		}
		sc.mu.Unlock()
		if c == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), selectTimeout)
		defer cancel()
		err = c.Disconnect(ctx)
	})
	return err
}

// ErrNoBoard is Open's refusal of a database that holds no board.
var ErrNoBoard = errors.New("holds no board")

// Database names the store's database.
func (s *Store) Database() string { return s.name }

// setup checks the schema, first making the collections, indexes and
// writer document of a board not set up at setupVersion: one an older
// build set up, or, with create, a new one. A database with no writer
// document holds no board, and without create it is refused, untouched.
// A board set up already costs one read, and needs no grant to build
// indexes. Each step is safe to run from many processes at once. It is the one place that touches collections outside
// a transaction: MongoDB builds indexes there.
func (s *Store) setup(ctx context.Context, create bool) error {
	db, done, err := s.conn()
	if err != nil {
		return err
	}
	defer done()
	var w writerDoc
	err = db.Collection(cMeta).FindOne(ctx, bson.D{{Key: "_id", Value: writerID}}).Decode(&w)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("read the board's meta: %w", err)
	}
	if err != nil && !create {
		return fmt.Errorf("%w: database %s %w: point store.uri at the database the board is in (maestro-ticket store --uri-file -), or start the board there by moving this machine's SQLite board with maestro-ticket migrate --to mongodb",
			ticket.ErrInvalid, s.name, ErrNoBoard)
	}
	if err != nil || w.Setup < setupVersion {
		if err := s.build(ctx, db); err != nil {
			return err
		}
		if err := db.Collection(cMeta).FindOne(ctx, bson.D{{Key: "_id", Value: writerID}}).Decode(&w); err != nil {
			return fmt.Errorf("read the board's meta: %w", err)
		}
	}
	return s.checkWriter(w)
}

func (s *Store) build(ctx context.Context, db *mongo.Database) error {
	have, err := db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}
	for _, c := range collections {
		if contains(have, c) {
			continue
		}
		var ce mongo.CommandError
		if err := db.CreateCollection(ctx, c); err != nil && !(errors.As(err, &ce) && ce.Code == 48) { // NamespaceExists
			return fmt.Errorf("create %s: %w", c, err)
		}
	}
	held := bson.D{{Key: "lease.expires", Value: bson.D{{Key: "$exists", Value: true}}}}
	idx := map[string][]mongo.IndexModel{
		cTickets: {
			{Keys: bson.D{{Key: "status", Value: 1}, {Key: "priority", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("tickets_queue")},
			{Keys: bson.D{{Key: "lease.holder", Value: 1}}, Options: options.Index().SetName("tickets_holder").SetPartialFilterExpression(held)},
			{Keys: bson.D{{Key: "lease.expires", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("tickets_lease_expiry").SetPartialFilterExpression(held)},
			{Keys: bson.D{{Key: "deps", Value: 1}}, Options: options.Index().SetName("deps_reverse")},
			{Keys: bson.D{{Key: "goal", Value: 1}}, Options: options.Index().SetName("tickets_goal")},
		},
		cComments: {{Keys: bson.D{{Key: "ticket_id", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("comments_ticket")}},
		cEvents: {
			{Keys: bson.D{{Key: "ticket_id", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("events_ticket")},
			{Keys: bson.D{{Key: "type", Value: 1}, {Key: "ticket_id", Value: 1}}, Options: options.Index().SetName("events_type")},
		},
		cRuns: {
			{Keys: bson.D{{Key: "ticket_id", Value: 1}}, Options: options.Index().SetName("runs_ticket")},
			{Keys: bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("runs_created")},
		},
		cLaunches: {
			{Keys: bson.D{{Key: "ended_at", Value: 1}, {Key: "started_at", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("launches_open")},
			{Keys: bson.D{{Key: "lifts", Value: 1}}, Options: options.Index().SetName("launches_lifts").SetSparse(true)},
		},
		cETAs: {{Keys: bson.D{{Key: "due_at", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("etas_due")}},
		// Made by its index: marks are no part of the board, so backups
		// and exports leave them out.
		cMarks: {{Keys: bson.D{{Key: "space", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("marks_space")}},
	}
	for c, models := range idx {
		if _, err := db.Collection(c).Indexes().CreateMany(ctx, models); err != nil {
			return fmt.Errorf("index %s: %w", c, err)
		}
	}
	// The writer document: made once, never replaced. It notes the setup
	// last, so a build cut short runs again.
	_, err = db.Collection(cMeta).UpdateOne(ctx, bson.D{{Key: "_id", Value: writerID}},
		bson.D{{Key: "$setOnInsert", Value: bson.D{
			{Key: "schema", Value: schemaVersion}, {Key: "moved", Value: false}, {Key: "writes", Value: int64(0)},
			{Key: "now", Value: bson.NewDateTimeFromTime(time.Now())},
			{Key: "tickets", Value: int64(0)}, {Key: "comments", Value: int64(0)},
			{Key: "events", Value: int64(0)}, {Key: "launches", Value: int64(0)},
		}}, {Key: "$max", Value: bson.D{{Key: "setup", Value: setupVersion}}}}, options.UpdateOne().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("make the writer document: %w", err)
	}
	return nil
}

// movedMsg is what every call fails with once the board was handed back
// to SQLite: the meta document's moved tombstone is set.
const movedMsg = "board moved: this MongoDB board was handed back to SQLite; use the store there"

func (s *Store) checkWriter(w writerDoc) error {
	switch {
	case s.mover != "" && w.Schema == schemaVersion:
		return nil
	case w.Moved:
		return fmt.Errorf("%w: %s", ticket.ErrInvalid, movedMsg)
	case w.Moving == MoveOut:
		return fmt.Errorf("%w: board moving: maestro-ticket migrate --to %s is copying it, or was cut short; wait for it, or run it again on the machine that started it; if that machine is gone, call it off with migrate --to sqlite --abort --force",
			ticket.ErrInvalid, w.Moving)
	case w.Moving != "":
		return fmt.Errorf("%w: board moving: maestro-ticket migrate --to %s is copying it, or was cut short; wait for it, or run it again on the machine that started it",
			ticket.ErrInvalid, w.Moving)
	case w.Schema > schemaVersion:
		return fmt.Errorf("%w: schema %d, this binary knows %d; rebuild maestro-ticket from the newest Maestro this machine uses",
			ticket.ErrStoreTooNew, w.Schema, schemaVersion)
	case w.Schema < 1:
		return fmt.Errorf("%w: the board's meta has no schema; it was not made by maestro-ticket", ticket.ErrInvalid)
	}
	return nil
}

func (s *Store) checkStop() error {
	if s.stop == "" {
		return nil
	}
	if _, err := os.Lstat(s.stop); err == nil {
		return fmt.Errorf("%w: %s exists; stop, and claim nothing until it is gone", ticket.ErrStopped, s.stop)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: can't tell if %s exists: %v", ticket.ErrStopped, s.stop, err)
	}
	return nil
}

// txn is one transaction. Every collection access goes through its coll,
// which runs the operation in the transaction's session.
type txn struct {
	ctx context.Context // the session's
	db  *mongo.Database
	now int64 // ms
	w   *writerDoc
	ids bool // a counter moved: write the writer document back
	// host is the host id whose code the write records, if any.
	host string
}

// coll returns collection name, bound to the transaction.
func (tx *txn) coll(name string) coll { return coll{ctx: tx.ctx, c: tx.db.Collection(name)} }

// next takes the next id of a counter on the writer document.
func (tx *txn) next(counter *int64) int64 {
	*counter++
	tx.ids = true
	return *counter
}

// coll is a collection bound to a transaction's session.
type coll struct {
	ctx context.Context
	c   *mongo.Collection
}

func (c coll) findOne(filter, out any, opts ...options.Lister[options.FindOneOptions]) error {
	return c.c.FindOne(c.ctx, filter, opts...).Decode(out)
}

// find decodes every match into out, a pointer to a slice.
func (c coll) find(filter, out any, opts ...options.Lister[options.FindOptions]) error {
	cur, err := c.c.Find(c.ctx, filter, opts...)
	if err != nil {
		return err
	}
	return cur.All(c.ctx, out)
}

func (c coll) cursor(filter any, opts ...options.Lister[options.FindOptions]) (*mongo.Cursor, error) {
	return c.c.Find(c.ctx, filter, opts...)
}

func (c coll) insert(doc any) error {
	_, err := c.c.InsertOne(c.ctx, doc)
	return err
}

func (c coll) insertMany(docs any) error {
	_, err := c.c.InsertMany(c.ctx, docs)
	return err
}

func (c coll) update(filter, update any, opts ...options.Lister[options.UpdateOneOptions]) (*mongo.UpdateResult, error) {
	return c.c.UpdateOne(c.ctx, filter, update, opts...)
}

// findAndUpdate updates one match and decodes it as it is after.
func (c coll) findAndUpdate(filter, update, out any) error {
	return c.c.FindOneAndUpdate(c.ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(out)
}

func (c coll) bulk(models []mongo.WriteModel) (*mongo.BulkWriteResult, error) {
	return c.c.BulkWrite(c.ctx, models)
}

func (c coll) deleteOne(filter any) (int64, error) {
	res, err := c.c.DeleteOne(c.ctx, filter)
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}

func (c coll) count(filter any) (int64, error) { return c.c.CountDocuments(c.ctx, filter) }

func txnOptions() *options.TransactionOptionsBuilder {
	return options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority())
}

// write runs fn in a write transaction. Its first step bumps the writer
// document and reads the server clock and counters from it, so writers
// line up one at a time and see time move forward in commit order.
func (s *Store) write(ctx context.Context, fn func(tx *txn) error) error {
	return s.retry(ctx, func(ctx context.Context) error { return s.once(ctx, true, fn) })
}

// read runs fn in a snapshot transaction. Its first step reads the
// server clock, once, and checks the board.
func (s *Store) read(ctx context.Context, fn func(tx *txn) error) error {
	return s.retry(ctx, func(ctx context.Context) error { return s.once(ctx, false, fn) })
}

// snapshot runs fn in a snapshot session: like read, every read in it
// sees the board as of one moment, but in no transaction, so a board too
// big to read within txnLimit still reads whole. The server keeps that
// moment for minSnapshotHistoryWindowInSeconds, 5 minutes by default.
func (s *Store) snapshot(ctx context.Context, fn func(tx *txn) error) error {
	db, done, err := s.conn()
	if err != nil {
		return err
	}
	defer done()
	sess, err := db.Client().StartSession(options.Session().SetSnapshot(true))
	if err != nil {
		return Redact(err, s.client.uri)
	}
	defer sess.EndSession(context.WithoutCancel(ctx))
	tx := &txn{ctx: mongo.NewSessionContext(ctx, sess), db: db}
	err = s.begin(tx, false)
	if err == nil {
		err = fn(tx)
	}
	return Redact(err, s.client.uri)
}

// retry reruns a transaction that lost to another (TransientTransaction
// Error), with exponential backoff and jitter, for up to txnBudget; then it
// gives up with ErrBusy.
func (s *Store) retry(ctx context.Context, run func(ctx context.Context) error) error {
	return Redact(s.retryRun(ctx, run), s.client.uri)
}

func (s *Store) retryRun(ctx context.Context, run func(ctx context.Context) error) error {
	start := time.Now()
	backoff := 2 * time.Millisecond
	for attempt := 1; ; attempt++ {
		err := run(ctx)
		if err == nil || !hasLabel(err, "TransientTransactionError") || ctx.Err() != nil {
			return err
		}
		if time.Since(start) > txnBudget {
			return fmt.Errorf("%w after %d tries in %s; try again later: %v", ticket.ErrBusy, attempt, time.Since(start).Round(time.Millisecond), err)
		}
		// The last wait ends as the budget does, so one more try lands.
		d := min(backoff/2+rand.N(backoff/2+1), txnBudget-time.Since(start))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func hasLabel(err error, label string) bool {
	var le mongo.LabeledError
	return errors.As(err, &le) && le.HasErrorLabel(label)
}

// once runs one attempt of a transaction.
func (s *Store) once(ctx context.Context, write bool, fn func(tx *txn) error) error {
	db, done, err := s.conn()
	if err != nil {
		return err
	}
	defer done()
	sess, err := db.Client().StartSession()
	if err != nil {
		return err
	}
	defer sess.EndSession(context.WithoutCancel(ctx))
	tctx, cancel := context.WithTimeout(ctx, txnLimit)
	defer cancel()
	if err := sess.StartTransaction(txnOptions()); err != nil {
		return err
	}
	tx := &txn{ctx: mongo.NewSessionContext(tctx, sess), db: db}
	err = s.begin(tx, write)
	if err == nil {
		err = fn(tx)
	}
	if err == nil && tx.ids {
		err = s.saveCounters(tx)
	}
	if err == nil && !write {
		// A read changed nothing: end it without a commit.
		actx, acancel := context.WithTimeout(context.WithoutCancel(ctx), selectTimeout)
		sess.AbortTransaction(actx)
		acancel()
		return nil
	}
	if err != nil {
		actx, acancel := context.WithTimeout(context.WithoutCancel(ctx), selectTimeout)
		sess.AbortTransaction(actx)
		acancel()
		if tctx.Err() != nil && ctx.Err() == nil && !hasLabel(err, "TransientTransactionError") {
			return fmt.Errorf("%w: a transaction ran past %s; try again later: %v", ticket.ErrBusy, txnLimit, err)
		}
		return err
	}
	return s.commit(ctx, sess)
}

// commit commits, retrying only the commit while its result is unknown:
// the closure already ran, and running it again could apply it twice.
func (s *Store) commit(ctx context.Context, sess *mongo.Session) error {
	cctx, cancel := context.WithTimeout(ctx, commitLimit)
	defer cancel()
	sctx := mongo.NewSessionContext(cctx, sess)
	for {
		err := sess.CommitTransaction(sctx)
		if err == nil || !hasLabel(err, "UnknownTransactionCommitResult") || hasLabel(err, "TransientTransactionError") {
			return err
		}
		select {
		case <-cctx.Done():
			return fmt.Errorf("commit result unknown after %s; check the ticket before you try again: %w", commitLimit, err)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// begin is a transaction's first step. A write bumps the writer document,
// which takes the board's one write slot and returns the server time and
// counters; a read projects the server time from it. Either refuses a
// moved board or a newer schema.
func (s *Store) begin(tx *txn, write bool) error {
	var w writerDoc
	var err error
	meta := tx.coll(cMeta)
	id := bson.D{{Key: "_id", Value: writerID}}
	if write {
		err = meta.findAndUpdate(id, bson.D{
			{Key: "$inc", Value: bson.D{{Key: "writes", Value: int64(1)}}},
			{Key: "$currentDate", Value: bson.D{{Key: "now", Value: true}}},
		}, &w)
	} else {
		err = meta.findOne(id, &w, options.FindOne().SetProjection(bson.D{
			{Key: "schema", Value: 1}, {Key: "moved", Value: 1}, {Key: "moving", Value: 1}, {Key: "now", Value: "$$NOW"},
		}))
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("%w: database %s holds no maestro board", ticket.ErrInvalid, s.name)
	}
	if err != nil {
		return err
	}
	if err := s.checkWriter(w); err != nil {
		return err
	}
	tx.now = int64(w.Now)
	if s.now != nil {
		tx.now = s.now().UnixMilli()
	}
	if write {
		tx.w = &w
		// A host whose code the board doesn't know yet records it with
		// this write: one more field, no more round trips.
		if s.host != "" && w.Hosts[s.host] != Code {
			tx.host = s.host
			tx.ids = true
		}
	}
	return nil
}

func (s *Store) saveCounters(tx *txn) error {
	set := bson.D{
		{Key: "tickets", Value: tx.w.Tickets}, {Key: "comments", Value: tx.w.Comments},
		{Key: "events", Value: tx.w.Events}, {Key: "launches", Value: tx.w.Launches},
	}
	up := bson.D{{Key: "$set", Value: set}}
	if tx.host != "" {
		up[0].Value = append(set, bson.E{Key: "hosts." + tx.host, Value: Code})
		up = append(up, bson.E{Key: "$max", Value: bson.D{{Key: "code", Value: Code}}})
	}
	_, err := tx.coll(cMeta).update(bson.D{{Key: "_id", Value: writerID}}, up)
	return err
}

// halted refuses a claim or a lease revival while the halt stands. The
// writer document is the transaction's first read, and stop --all sets
// the halt in a write of its own, so a claim that began before the halt
// either commits before it or loses to it and sees it.
func (tx *txn) halted() error {
	if tx.w != nil && tx.w.Halt {
		return fmt.Errorf("%w: %s", ticket.ErrStopped, haltMsg)
	}
	return nil
}

// haltMsg is what a claim under the halt fails with.
const haltMsg = "the board is halted (maestro-ticket stop --all); claim nothing until stop --all --off"

func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func dt(ms int64) bson.DateTime { return bson.DateTime(ms) }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// encodeData is an event's data as SQLite's store writes it: JSON, with
// < > & kept as written, since a note quotes code.
func encodeData(data any) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(data); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// Redact takes uri, and its password alone, out of err's text, keeping
// what err wraps for errors.Is.
func Redact(err error, uri string) error {
	if err == nil || uri == "" {
		return err
	}
	msg := err.Error()
	out := strings.ReplaceAll(msg, uri, hostOf(uri))
	if u, perr := url.Parse(uri); perr == nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			out = strings.ReplaceAll(out, pw, "***")
			out = strings.ReplaceAll(out, url.QueryEscape(pw), "***")
		}
	}
	if out == msg {
		return err
	}
	return redacted{msg: out, err: err}
}

func hostOf(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" {
		return "an unreadable URI"
	}
	return u.Host
}

type redacted struct {
	msg string
	err error
}

func (e redacted) Error() string { return e.msg }
func (e redacted) Unwrap() error { return e.err }
