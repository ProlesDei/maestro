package mongostore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/storetest"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// testURIEnv names a replica set the tests may make and drop databases
// on. Without it, every test here skips.
const testURIEnv = "MAESTRO_TICKET_TEST_MONGO"

func testURI(t *testing.T) string {
	t.Helper()
	uri := os.Getenv(testURIEnv)
	if uri == "" {
		t.Skipf("set %s to a replica set URI to run the MongoDB tests", testURIEnv)
	}
	return uri
}

// freshDB names a new database for t and drops it when t ends, after the
// stores t opened are closed.
func freshDB(t *testing.T, uri string) string {
	t.Helper()
	b := make([]byte, 8)
	rand.Read(b)
	name := "mt_" + hex.EncodeToString(b)
	t.Cleanup(func() {
		c, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(selectTimeout))
		if err != nil {
			t.Errorf("drop %s: %v", name, err)
			return
		}
		defer c.Disconnect(context.Background())
		if err := c.Database(name).Drop(context.Background()); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})
	return name
}

// TestConformance runs the tests every ticket.Store must pass.
func TestConformance(t *testing.T) {
	uri := testURI(t)
	if storetest.ReapTickets <= 2*reapBatch {
		t.Fatalf("reap batch %d: the suite's %d leases don't cross two batches", reapBatch, storetest.ReapTickets)
	}
	storetest.Run(t, func(t *testing.T, now func() time.Time) func() (ticket.Store, error) {
		db := freshDB(t, uri)
		return func() (ticket.Store, error) {
			s, err := Open(context.Background(), uri, Options{Database: db, Now: now})
			if err != nil {
				return nil, err
			}
			return s, nil
		}
	})
}

func newStore(t *testing.T, opts Options) (*Store, string) {
	t.Helper()
	uri := testURI(t)
	if opts.Database == "" {
		opts.Database = freshDB(t, uri)
	}
	s, err := Open(context.Background(), uri, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, uri
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v; want %v", err, want)
	}
}

// setMeta changes the writer document behind the store's back.
func setMeta(t *testing.T, s *Store, set bson.D) {
	t.Helper()
	if _, err := s.db.Collection(cMeta).UpdateOne(context.Background(), bson.D{{Key: "_id", Value: writerID}},
		bson.D{{Key: "$set", Value: set}}); err != nil {
		t.Fatal(err)
	}
}

func TestMovedBoardRefusesEveryCall(t *testing.T) {
	s, uri := newStore(t, Options{})
	ctx := context.Background()
	tk, err := s.Create(ctx, ticket.CreateRequest{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	setMeta(t, s, bson.D{{Key: "moved", Value: true}})
	_, err = s.Get(ctx, tk.ID)
	wantErr(t, err, ticket.ErrInvalid)
	if !strings.Contains(err.Error(), "board moved") {
		t.Fatalf("read: %v", err)
	}
	_, err = s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "hi"})
	wantErr(t, err, ticket.ErrInvalid)
	_, err = Open(ctx, uri, Options{Database: s.Database()})
	wantErr(t, err, ticket.ErrInvalid)
	rep, err := s.Doctor(ctx)
	if err != nil || rep.OK() {
		t.Fatalf("doctor on a moved board: ok %v, %v", rep.OK(), err)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	s, uri := newStore(t, Options{})
	setMeta(t, s, bson.D{{Key: "schema", Value: schemaVersion + 1}})
	_, err := s.List(context.Background(), ticket.ListFilter{})
	wantErr(t, err, ticket.ErrStoreTooNew)
	_, err = Open(context.Background(), uri, Options{Database: s.Database()})
	wantErr(t, err, ticket.ErrStoreTooNew)
}

func TestStopFileRefusesClaims(t *testing.T) {
	stop := filepath.Join(t.TempDir(), "STOP")
	clk := storetest.NewClock()
	s, _ := newStore(t, Options{StopFile: stop, Now: clk.Now})
	ctx := context.Background()
	tk, _ := s.Create(ctx, ticket.CreateRequest{Title: "x"})
	c, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stop, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s2"})
	wantErr(t, err, ticket.ErrStopped)
	// A live lease still beats; one that ran out is not revived.
	if _, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/s1", Token: c.Claim.Token, TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(2 * time.Minute)
	_, err = s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/s1", Token: c.Claim.Token, TTL: time.Minute})
	wantErr(t, err, ticket.ErrLeaseLost)
	wantErr(t, err, ticket.ErrStopped)
}

func TestErrorsHideTheURI(t *testing.T) {
	uri := "mongodb://maestro:s3cret-pw@127.0.0.1:1/?replicaSet=rs0&serverSelectionTimeoutMS=200"
	s, err := Open(context.Background(), uri, Options{Database: "mt_unreachable"})
	if err == nil {
		s.Close()
		t.Fatal("open of an unreachable server succeeded")
	}
	if strings.Contains(err.Error(), "s3cret-pw") || strings.Contains(err.Error(), uri) {
		t.Fatalf("error leaks the URI: %v", err)
	}
	_, err = Open(context.Background(), "mongodb://u:s3cret-pw@h:99999999/", Options{})
	if !errors.Is(err, ticket.ErrInvalid) || strings.Contains(err.Error(), "s3cret-pw") {
		t.Fatalf("bad URI: %v", err)
	}
}

func TestBackupKeepsEveryCollection(t *testing.T) {
	s, _ := newStore(t, Options{})
	ctx := context.Background()
	tk, err := s.Create(ctx, ticket.CreateRequest{Title: "x", IdempotencyKey: "cos/s1:create"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: "note"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backups", "tickets-1.json")
	if err := s.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.Backup(ctx, path), ticket.ErrInvalid) // never overwrites
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got backupFile
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{cMeta: 1, cTickets: 1, cComments: 1, cEvents: 2, cIdempotency: 1, cRuns: 0, cLaunches: 0, cETAs: 0}
	for c, n := range want {
		if got.Counts[c] != n || int64(len(got.Collections[c])) != n {
			t.Errorf("%s: count %d, %d documents; want %d", c, got.Counts[c], len(got.Collections[c]), n)
		}
	}
	if _, err := os.Lstat(path + ".partial"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial left: %v", err)
	}
}

func TestDoctorPassesOnAFreshBoard(t *testing.T) {
	s, _ := newStore(t, Options{})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := s.Create(ctx, ticket.CreateRequest{Title: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := s.Doctor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("doctor: %+v", rep.Checks)
	}
	names := map[string]bool{}
	for _, c := range rep.Checks {
		names[c.Name] = true
	}
	for _, n := range []string{"connection", "schema", "transaction_limit", "transactions", "leases", "counters", "events"} {
		if !names[n] {
			t.Errorf("no %s check in %+v", n, rep.Checks)
		}
	}
}

// A latest comment longer than the ticket keeps still matches a long
// prefix, read from the comment itself.
func TestLatestPrefixPastWhatTheTicketKeeps(t *testing.T) {
	s, _ := newStore(t, Options{})
	ctx := context.Background()
	tk, _ := s.Create(ctx, ticket.CreateRequest{Title: "x"})
	long := strings.Repeat("é", latestKeep) + "Review: GO"
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: tk.ID, Body: long + " and more"}); err != nil {
		t.Fatal(err)
	}
	for prefix, want := range map[string]int{long: 1, long + "x": 0, "é": 1, "e": 0} {
		got, err := s.List(ctx, ticket.ListFilter{LatestPrefix: prefix})
		if err != nil || len(got) != want {
			t.Errorf("prefix %q: %d tickets, %v; want %d", prefix, len(got), err, want)
		}
		not, _ := s.List(ctx, ticket.ListFilter{NotLatestPrefix: prefix})
		if len(not) != 1-want {
			t.Errorf("not prefix %q: %d tickets; want %d", prefix, len(not), 1-want)
		}
	}
}

// With no clock injected, writes take the server's time from the writer
// document and reads from $$NOW.
func TestServerClock(t *testing.T) {
	s, _ := newStore(t, Options{})
	ctx := context.Background()
	before := time.Now()
	tk, err := s.Create(ctx, ticket.CreateRequest{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	const skew = 5 * time.Second
	if tk.CreatedAt.Before(before.Add(-skew)) || tk.CreatedAt.After(after.Add(skew)) {
		t.Fatalf("created at %s; want near %s", tk.CreatedAt, before)
	}
	if exp := c.Claim.ExpiresAt; exp.Before(before.Add(time.Hour-skew)) || exp.After(after.Add(time.Hour+skew)) {
		t.Fatalf("lease expires %s; want an hour from %s", exp, before)
	}
	got, err := s.Get(ctx, tk.ID)
	if err != nil || got.LiveClaim() == nil {
		t.Fatalf("read: claim %+v, %v", got.Claim, err)
	}
	// Two conditions on the lease both hold.
	no := false
	if l, _ := s.List(ctx, ticket.ListFilter{Holder: "jse/s1", Claimed: &no}); len(l) != 0 {
		t.Fatalf("held and unclaimed: %d tickets", len(l))
	}
	if l, _ := s.List(ctx, ticket.ListFilter{Ready: true, Claimed: &no}); len(l) != 0 {
		t.Fatalf("ready: %d tickets", len(l))
	}
}
