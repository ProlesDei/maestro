package mongostore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// testDB is s's database, for a test to reach behind the store's back
// at once: the client may disconnect idleClose after.
func testDB(t *testing.T, s *Store) *mongo.Database {
	t.Helper()
	db, done, err := s.conn()
	if err != nil {
		t.Fatal(err)
	}
	done()
	return db
}

// connected reports whether s's client is connected.
func connected(s *Store) bool {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	return s.client.c != nil
}

// A client no call has used for idleClose disconnects, and the next call
// connects it again: a process that lives as long as a run holds no
// connection between its calls.
func TestIdleClientDisconnects(t *testing.T) {
	defer func(d time.Duration) { idleClose = d }(idleClose)
	idleClose = 50 * time.Millisecond
	s, _ := newStore(t, Options{})
	ctx := context.Background()
	tk, err := s.Create(ctx, ticket.CreateRequest{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for connected(s) {
		if time.Now().After(deadline) {
			t.Fatal("the client stayed connected with no call using it")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := s.Get(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Halted(ctx); err != nil {
		t.Fatal(err)
	}
}

// Calls at once on stores that share a client, while it keeps
// disconnecting between them, all go through.
func TestIdleCloseUnderCalls(t *testing.T) {
	defer func(d time.Duration) { idleClose = d }(idleClose)
	idleClose = time.Millisecond
	s, uri := newStore(t, Options{})
	ctx := context.Background()
	tk, err := s.Create(ctx, ticket.CreateRequest{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := Open(ctx, uri, Options{Database: s.Database()})
			if err != nil {
				errs <- err
				return
			}
			defer o.Close()
			for i := range 6 {
				if _, err := o.Get(ctx, tk.ID); err != nil {
					errs <- err
				}
				if err := o.WriteMark(ctx, "test", "m", "x"); err != nil {
					errs <- err
				}
				time.Sleep(time.Duration(g+i) * time.Millisecond)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// ownDB is database name on a client of the test's own, apart from the
// stores'.
func ownDB(t *testing.T, uri, name string) *mongo.Database {
	t.Helper()
	c, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(selectTimeout))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Disconnect(context.Background()) })
	return c.Database(name)
}

func hasIndex(t *testing.T, db *mongo.Database, coll, name string) bool {
	t.Helper()
	specs, err := db.Collection(coll).Indexes().ListSpecifications(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

// A board this build set up opens with one read: no index build, which a
// role with no createIndex grant on events would refuse. A board an older
// build made gets its indexes on its next open.
func TestOpenBuildsOnlyABoardNotSetUp(t *testing.T) {
	uri := testURI(t)
	ctx := context.Background()
	name := freshDB(t, uri)
	s, err := Open(ctx, uri, Options{Database: name})
	if err != nil {
		t.Fatal(err)
	}
	s.Close() // and the process's client with it
	db := ownDB(t, uri, name)
	if err := db.Collection(cEvents).Indexes().DropOne(ctx, "events_type"); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, uri, Options{Database: name})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if hasIndex(t, db, cEvents, "events_type") {
		t.Fatal("open built the indexes of a board already set up")
	}

	if _, err := db.Collection(cMeta).UpdateOne(ctx, bson.D{{Key: "_id", Value: writerID}},
		bson.D{{Key: "$unset", Value: bson.D{{Key: "setup", Value: ""}}}}); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, uri, Options{Database: name})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !hasIndex(t, db, cEvents, "events_type") {
		t.Fatal("open left a board an older build made without its indexes")
	}
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "x"}); err != nil {
		t.Fatal(err)
	}
}

// A transaction that keeps losing to other writers waits longer each try,
// past 200ms, so a burst of writers doesn't flood the server, and still
// gives up with ErrBusy once the budget is spent.
func TestRetryBacksOffTowardTheBudget(t *testing.T) {
	defer func(b time.Duration) { txnBudget = b }(txnBudget)
	txnBudget = 4 * time.Second
	s := &Store{client: &sharedClient{}}
	lost := mongo.CommandError{Code: 112, Name: "WriteConflict", Labels: []string{"TransientTransactionError"}}
	var tries []time.Time
	start := time.Now()
	err := s.retryRun(context.Background(), func(context.Context) error {
		tries = append(tries, time.Now())
		return lost
	})
	took := time.Since(start)
	if !errors.Is(err, ticket.ErrBusy) {
		t.Fatalf("err = %v; want ErrBusy", err)
	}
	var gap time.Duration
	for i := 1; i < len(tries); i++ {
		gap = max(gap, tries[i].Sub(tries[i-1]))
	}
	if gap < 400*time.Millisecond {
		t.Errorf("%d tries in %s, the longest wait %s: backoff stopped growing", len(tries), took.Round(time.Millisecond), gap.Round(time.Millisecond))
	}
	if took > txnBudget+500*time.Millisecond {
		t.Errorf("gave up after %s; budget %s", took.Round(time.Millisecond), txnBudget)
	}
}

// Calls at once in one process, as many as ringOrphans runs, each get a
// connection: one waiting for a connection would spend its transaction's
// deadline in the wait. Seven calls stall on the server, holding theirs,
// and an eighth still writes well inside txnLimit.
func TestEightCallsAtOnceEachGetAConnection(t *testing.T) {
	defer func(d time.Duration) { txnLimit = d }(txnLimit)
	s, uri := newStore(t, Options{})
	ctx := context.Background()
	own := ownDB(t, uri, s.Database())
	hold := own.Collection("hold")
	if _, err := hold.InsertOne(ctx, bson.D{{Key: "_id", Value: "lock"}}); err != nil {
		t.Fatal(err)
	}
	// A transaction of the test's own takes the document and keeps it, so
	// every write to it from outside waits on the server.
	sess, err := own.Client().StartSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.EndSession(ctx)
	if err := sess.StartTransaction(); err != nil {
		t.Fatal(err)
	}
	sctx := mongo.NewSessionContext(ctx, sess)
	if _, err := hold.UpdateOne(sctx, bson.D{{Key: "_id", Value: "lock"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "by", Value: "test"}}}}); err != nil {
		t.Fatal(err)
	}
	ended := false
	end := func() {
		if !ended {
			ended = true
			sess.AbortTransaction(ctx)
		}
	}
	defer end()

	const stalled = 7
	db := testDB(t, s)
	var wg sync.WaitGroup
	for range stalled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db.Collection("hold").UpdateOne(ctx, bson.D{{Key: "_id", Value: "lock"}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "n", Value: 1}}}})
		}()
	}
	defer wg.Wait()
	// Wait until the server holds all seven, each on a connection of
	// the store's, or as many as the pool lets through.
	var reached int
	for deadline := time.Now().Add(3 * time.Second); reached < stalled && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var out struct {
			Inprog []bson.Raw `bson:"inprog"`
		}
		cmd := bson.D{{Key: "currentOp", Value: 1}, {Key: "ns", Value: s.Database() + ".hold"}, {Key: "op", Value: "update"}}
		if err := own.Client().Database("admin").RunCommand(ctx, cmd).Decode(&out); err != nil {
			t.Fatal(err)
		}
		reached = len(out.Inprog)
	}

	txnLimit = 2 * time.Second
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "x"}); err != nil {
		t.Fatalf("a write beside %d stalled calls (%d on the server): %v", stalled, reached, err)
	}
	end()
}
