package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/storetest"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// TestConformance runs the tests every ticket.Store must pass.
func TestConformance(t *testing.T) {
	if storetest.ReapTickets <= 2*reapBatch {
		t.Fatalf("reap batch %d: the suite's %d leases don't cross two batches", reapBatch, storetest.ReapTickets)
	}
	if storetest.ForgetKeys <= 2*keyBatch || storetest.ForgetKeys > keyBatch*keyBatches {
		t.Fatalf("key batch %d, %d a reap: the suite's %d keys don't cross two batches in one reap", keyBatch, keyBatches, storetest.ForgetKeys)
	}
	storetest.Run(t, func(t *testing.T, now func() time.Time) func() (ticket.Store, error) {
		path := filepath.Join(t.TempDir(), "maestro", "tickets.db")
		return func() (ticket.Store, error) {
			s, err := Open(context.Background(), path, Options{Now: now})
			if err != nil {
				return nil, err
			}
			return s, nil
		}
	})
}

func newStore(t *testing.T) (*Store, *storetest.Clock) {
	t.Helper()
	clk := storetest.NewClock()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "maestro", "tickets.db"), Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, clk
}

func mustCreate(t *testing.T, s *Store, title, role string, prio int) ticket.Ticket {
	t.Helper()
	tk, err := s.Create(context.Background(), ticket.CreateRequest{Title: title, Role: role, Priority: &prio, Actor: "cos"})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v; want %v", err, want)
	}
}

func TestStopFileRefusesClaims(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, "x", "", 2)
	stop := filepath.Join(filepath.Dir(s.Path()), StopFile)
	if err := os.WriteFile(stop, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1"})
	wantErr(t, err, ticket.ErrStopped)
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: tk.ID, Holder: "jse/s1"})
	wantErr(t, err, ticket.ErrStopped)
	if code, _ := ticket.Classify(err); code != "stopped" || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("code %q, err %v", code, err)
	}
	os.Remove(stop)
	c, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	// A lease that ran out isn't revived while the switch is on.
	clk.Advance(2 * time.Minute)
	if err := os.WriteFile(stop, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: tk.ID, Holder: "jse/s1", Token: c.Claim.Token, TTL: time.Minute})
	wantErr(t, err, ticket.ErrLeaseLost)
	wantErr(t, err, ticket.ErrStopped)
}

// A claim waiting on the write lock when STOP appears must not land: the
// check runs under the lock, not before it.
func TestStopFileRefusesClaimWaitingOnLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "maestro", "tickets.db")
	ss := openMany(t, path, 3)
	mustCreate(t, ss[0], "x", "", 2)
	tx, err := ss[0].w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ss[1].ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1"})
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), StopFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A claim that starts under STOP fails at once, lock held or not.
	start := time.Now()
	_, err = ss[2].ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s2"})
	wantErr(t, err, ticket.ErrStopped)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("claim under STOP waited %v on the lock", d)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wantErr(t, <-done, ticket.ErrStopped)
}

func TestEventsAppendOnly(t *testing.T) {
	s, _ := newStore(t)
	mustCreate(t, s, "x", "", 2)
	if _, err := s.w.Exec("UPDATE events SET type = 'x'"); err == nil {
		t.Fatal("update of events should fail")
	}
	if _, err := s.w.Exec("DELETE FROM events"); err == nil {
		t.Fatal("delete of events should fail")
	}
}

func TestPragmasAndDoctor(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	rep, err := s.Doctor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("doctor: %+v", rep)
	}
	// The reader pool gets the same settings.
	var mode string
	var fk, timeout int
	s.r.QueryRow("PRAGMA journal_mode").Scan(&mode)
	s.r.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	s.r.QueryRow("PRAGMA busy_timeout").Scan(&timeout)
	if mode != "wal" || fk != 1 || timeout != 5000 {
		t.Fatalf("reader pragmas: %s %d %d", mode, fk, timeout)
	}
}

func TestDoctorFlagsBadSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	db, _ := sql.Open("sqlite", path)
	db.Exec("PRAGMA journal_mode=DELETE")
	db.Close()
	s, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Open always switches to WAL, so a store opened by us is healthy.
	rep, _ := s.Doctor(context.Background())
	if !rep.OK() {
		t.Fatalf("doctor: %+v", rep)
	}
	s.w.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion+1))
	rep, _ = s.Doctor(context.Background())
	if rep.OK() {
		t.Fatal("doctor should flag a wrong schema version")
	}
}

func TestDoctorFlagsUnknownStatus(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "t.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Create(ctx, ticket.CreateRequest{Title: "stale"})
	s.w.Exec("UPDATE tickets SET status = 'in_qa' WHERE id = 1")
	rep, _ := s.Doctor(ctx)
	for _, c := range rep.Checks {
		if c.Name == "statuses" {
			if c.OK || !strings.Contains(c.Detail, "T-1 (in_qa)") {
				t.Fatalf("statuses check: %+v", c)
			}
			return
		}
	}
	t.Fatalf("no statuses check: %+v", rep)
}

func TestReopenKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	ctx := context.Background()
	s, _ := Open(ctx, path, Options{})
	s.Create(ctx, ticket.CreateRequest{Title: "persist"})
	s.Close()
	s2, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if d, err := s2.Get(ctx, 1); err != nil || d.Title != "persist" {
		t.Fatalf("%+v, %v", d, err)
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	db, _ := sql.Open("sqlite", path)
	db.Exec("PRAGMA user_version = 99")
	db.Close()
	_, err := Open(context.Background(), path, Options{})
	if code, exit := ticket.Classify(err); code != "store_too_new" || exit != ticket.ExitUsage {
		t.Fatalf("err %v: code %q exit %d; want store_too_new, %d", err, code, exit, ticket.ExitUsage)
	}
}

// A store a newer binary migrates while Open waits on the write lock is
// refused too, not opened.
func TestNewerSchemaRefusedUnderLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", path+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := func(q string) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion-1))
	exec("BEGIN IMMEDIATE") // a newer binary, migrating
	done := make(chan error, 1)
	go func() {
		s, err := Open(ctx, path, Options{})
		if err == nil {
			s.Close()
		}
		done <- err
	}()
	time.Sleep(500 * time.Millisecond) // Open read the old version and waits on the lock
	exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion+1))
	exec("COMMIT")
	if err := <-done; !errors.Is(err, ticket.ErrStoreTooNew) {
		t.Fatalf("Open of a store migrated past it meanwhile: %v; want store_too_new", err)
	}
}

// busyErr looks like SQLITE_BUSY to isBusy.
type busyErr struct{}

func (busyErr) Error() string { return "database is locked (5) (test)" }

func (busyErr) Code() int { return 5 }

// A busy retry after a full scan must not repeat rows.
func TestReadRetryDoesNotDuplicate(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		mustCreate(t, s, "x", "", 2)
	}
	fails := 0
	s.faultRead = func() error {
		if fails < 2 {
			fails++
			return busyErr{}
		}
		return nil
	}
	list, err := s.List(ctx, ticket.ListFilter{})
	if err != nil || len(list) != 3 || fails != 2 {
		t.Fatalf("List after busy retries: %d tickets, %v (fails %d); want 3", len(list), err, fails)
	}
	fails = 0
	evs, err := s.Events(ctx, ticket.EventFilter{})
	if err != nil || len(evs) != 3 || fails != 2 {
		t.Fatalf("Events after busy retries: %d events, %v (fails %d); want 3", len(evs), err, fails)
	}
	fails = 0
	d, err := s.Get(ctx, 1)
	if err != nil || len(d.Comments) != 0 || fails != 2 {
		t.Fatalf("Get after busy retries: %+v, %v", d, err)
	}
}

// openV1 builds a store file exactly as schema v1 left it, with a todo
// ticket, a blocked ticket and a ticket under a live claim.
func openV1(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exp := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, q := range []string{
		schemaV1,
		`INSERT INTO tickets (title, status, priority, created_by, created_at, updated_at) VALUES ('a', 'todo', 2, 'cos', 1, 1)`,
		`INSERT INTO tickets (title, status, priority, created_by, created_at, updated_at) VALUES ('b', 'blocked', 2, 'cos', 1, 1)`,
		`INSERT INTO tickets (title, status, priority, created_by, version, lease_holder, lease_expires_at, created_at, updated_at)
			VALUES ('c', 'in_progress', 1, 'cos', 3, 'jse/s1', ` + fmt.Sprint(exp) + `, 1, 1)`,
		`INSERT INTO events (ticket_id, type, actor, created_at) VALUES (1, 'created', 'cos', 1)`,
		"PRAGMA user_version = 1",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return path
}

func TestMigrateV1(t *testing.T) {
	path := openV1(t)
	ctx := context.Background()
	// A migration killed mid-snapshot leaves a .partial; the next open
	// writes the snapshot anyway.
	snap := filepath.Join(filepath.Dir(path), "backups", "v1-pre-schema-v1.db")
	if err := os.MkdirAll(filepath.Dir(snap), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snap+".partial", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, path, Options{Now: func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	s.w.QueryRow("PRAGMA user_version").Scan(&v)
	if v != schemaVersion || schemaVersion < 2 {
		t.Fatalf("user_version %d; want %d", v, schemaVersion)
	}
	// The store as it was before the migration is kept.
	if err := checkBackup(ctx, snap, 1); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx, ticket.ListFilter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("list %+v, %v", all, err)
	}
	by := map[string]ticket.Ticket{}
	for _, tk := range all {
		by[tk.Title] = tk
		if tk.Type != ticket.TypeShip || tk.Tries != 0 || tk.Owner != tk.CreatedBy {
			t.Errorf("%s: type %q tries %d owner %q; want ship, 0, %q", tk.ID, tk.Type, tk.Tries, tk.Owner, tk.CreatedBy)
		}
	}
	if by["a"].BlockedFrom != "" || by["b"].BlockedFrom != ticket.StatusTodo {
		t.Errorf("blocked_from: a=%q b=%q; want \"\", todo", by["a"].BlockedFrom, by["b"].BlockedFrom)
	}
	if c := by["c"].Claim; c == nil || c.Holder != "jse/s1" || c.Token != 1 || c.Expired || by["c"].Version != 3 {
		t.Errorf("live v1 claim after migration: %+v v%d", c, by["c"].Version)
	}
	if evs, _ := s.Events(ctx, ticket.EventFilter{}); len(evs) != 1 {
		t.Errorf("events lost: %d", len(evs))
	}
	rep, err := s.Doctor(ctx)
	if err != nil || !rep.OK() {
		t.Fatalf("doctor after migration: %+v, %v", rep, err)
	}
	// Reopening a migrated store is a no-op.
	s2, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
	if _, err := s.w.Exec("UPDATE tickets SET type = 'epic' WHERE id = 1"); err == nil {
		t.Error("type column should reject unknown types")
	}
}

// A v4 store with tickets in removed statuses and old role names migrates
// to in_review and current roles, logs each change, and loses the outbox.
// planner and qa-lead stay as they are. A role with no mapping stays for
// doctor to report.
func TestMigrateV5(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v4.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := append([]string{}, migrations[:4]...)
	stmts = append(stmts,
		`INSERT INTO tickets (title, status, role, priority, created_by, version, created_at, updated_at) VALUES
			('qa', 'in_qa', 'qa-lead', 2, 'cos', 2, 1, 1),
			('rel', 'ready_to_release', 'operations-manager', 2, 'cos', 1, 1, 1),
			('custom', 'todo', 'art-director', 2, 'cos', 1, 1, 1),
			('old', 'done', 'junior-software-engineer', 2, 'cos', 1, 1, 1)`,
		`INSERT INTO tickets (title, status, blocked_from, role, priority, created_by, created_at, updated_at)
			VALUES ('blk', 'blocked', 'in_qa', 'senior-software-engineer', 2, 'cos', 1, 1)`,
		`INSERT INTO tickets (title, status, role, priority, created_by, version, created_at, updated_at) VALUES
			('plan', 'todo', 'planner', 2, 'cos', 1, 1, 1),
			('rev', 'in_review', 'code-reviewer', 2, 'cos', 1, 1, 1)`,
		`INSERT INTO events (ticket_id, type, actor, created_at) VALUES (1, 'created', 'cos', 1)`,
		`INSERT INTO outbox (event_id, kind, payload, next_attempt_at) VALUES (1, 'created', '{}', 1)`,
		"PRAGMA user_version = 4",
	)
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	ctx := context.Background()
	s, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := []struct {
		status, blockedFrom ticket.Status
		role                string
		version             int64
	}{
		{ticket.StatusInReview, "", ticket.RoleQALead, 3},
		{ticket.StatusInReview, "", ticket.RoleReviewer, 2},
		{ticket.StatusTodo, "", "art-director", 1},
		{ticket.StatusDone, "", ticket.RoleEngineer, 1},
		{ticket.StatusBlocked, ticket.StatusInReview, ticket.RoleEngineer, 1},
		{ticket.StatusTodo, "", ticket.RolePlanner, 1},
		{ticket.StatusInReview, "", ticket.RoleReviewer, 1},
	}
	for i, w := range want {
		d, err := s.Get(ctx, ticket.ID(i+1))
		if err != nil || d.Status != w.status || d.BlockedFrom != w.blockedFrom || d.Role != w.role || d.Version != w.version {
			t.Errorf("T-%d: %s from %q role %q v%d, %v; want %+v", i+1, d.Status, d.BlockedFrom, d.Role, d.Version, err, w)
		}
	}
	evs, _ := s.Events(ctx, ticket.EventFilter{})
	var moves, updates int
	for _, e := range evs {
		if e.Actor != ticket.SystemActor {
			continue
		}
		var d map[string]any
		json.Unmarshal(e.Data, &d)
		switch {
		case e.Type == ticket.EventTransitioned && d["to"] == "in_review":
			moves++
		case e.Type == ticket.EventUpdated && (d["role"] != nil || d["blocked_from"] == "in_review"):
			updates++
		default:
			t.Errorf("unexpected migration event %+v", e)
		}
	}
	if moves != 2 || updates != 5 {
		t.Errorf("migration logged %d moves, %d updates; want 2, 5 (4 roles, 1 blocked_from)", moves, updates)
	}
	var outbox int
	s.r.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'outbox'").Scan(&outbox)
	if outbox != 0 {
		t.Error("outbox table survived v5")
	}

	rep, err := s.Doctor(ctx)
	if err != nil || rep.OK() {
		t.Fatalf("doctor should flag the unknown role: %+v, %v", rep, err)
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: 3, To: ticket.StatusBacklog, Parked: ticket.ParkedHold, Role: ticket.RoleEngineer}); err != nil {
		t.Fatal(err)
	}
	if rep, err := s.Doctor(ctx); err != nil || !rep.OK() {
		t.Fatalf("doctor after fixing the role: %+v, %v", rep, err)
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: 1, To: ticket.StatusDone, ActorRole: ticket.RoleQALead}); err != nil {
		t.Fatalf("QA lead done after migration: %v", err)
	}
}

// Without an injected clock, lease times come from the database's clock.
func TestDatabaseClock(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "tickets.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustCreate(t, s, "x", "", 2)
	before := time.Now().Add(-time.Second)
	c, err := s.ClaimNext(ctx, ticket.ClaimRequest{Holder: "jse/s1", TTL: time.Minute})
	after := time.Now().Add(time.Second)
	if err != nil || c.Claim.ExpiresAt.Before(before.Add(time.Minute)) || c.Claim.ExpiresAt.After(after.Add(time.Minute)) || c.Claim.Expired {
		t.Fatalf("claim %+v, %v; want expiry about a minute from now", c.Claim, err)
	}
	var ms int64
	if ms, err = s.clock(ctx, s.r); err != nil || ms < before.UnixMilli() || ms > after.UnixMilli() {
		t.Fatalf("database clock %d, %v", ms, err)
	}
}

func TestCheckpoint(t *testing.T) {
	s, _ := newStore(t)
	for i := 0; i < 50; i++ {
		mustCreate(t, s, fmt.Sprint(i), "", 2)
	}
	frames, done, err := s.Checkpoint(context.Background())
	if err != nil || frames == 0 || done != frames {
		t.Fatalf("checkpoint frames %d done %d, %v", frames, done, err)
	}
}

func TestBusyAfterRetriesIsBusy(t *testing.T) {
	err := retryBusy(context.Background(), func() error { return busyErr{} })
	if code, _ := ticket.Classify(err); code != "busy" || !isBusy(err) {
		t.Fatalf("err %v: code %q", err, code)
	}
}

// A reap deletes at most keyBatch*keyBatches old idempotency keys, by the
// age index, and never a key inside the window.
func TestReapForgetsKeysInBounds(t *testing.T) {
	s, clk := newStore(t)
	ctx := context.Background()
	add := func(prefix string, n int) {
		t.Helper()
		tx, err := s.w.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for i := range n {
			if _, err := tx.Exec("INSERT INTO idempotency (key, op, request_hash, result, created_at) VALUES (?, 'comment', 'h', '{}', ?)",
				fmt.Sprint(prefix, i), clk.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	count := func(where string) int {
		t.Helper()
		var n int
		if err := s.r.QueryRow("SELECT count(*) FROM idempotency WHERE " + where).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	bound := keyBatch * keyBatches
	add("old-", bound+bound/2)
	clk.Advance(ticket.IdempotencyKeep - time.Millisecond)
	add("fresh-", 3)
	clk.Advance(time.Millisecond)
	if _, err := s.Reap(ctx); err != nil || count("1") != bound+bound/2+3 {
		t.Fatalf("a reap at exactly the window deleted keys: %d left, %v", count("1"), err)
	}
	clk.Advance(time.Millisecond)
	if _, err := s.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	if old, fresh := count("key LIKE 'old-%'"), count("key LIKE 'fresh-%'"); old != bound/2 || fresh != 3 {
		t.Fatalf("after one reap: %d old, %d fresh; want %d, 3", old, fresh, bound/2)
	}
	if _, err := s.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	if old, fresh := count("key LIKE 'old-%'"), count("key LIKE 'fresh-%'"); old != 0 || fresh != 3 {
		t.Fatalf("after two reaps: %d old, %d fresh; want 0, 3", old, fresh)
	}
	var plan strings.Builder
	rows, err := s.r.Query("EXPLAIN QUERY PLAN SELECT key FROM idempotency WHERE created_at < 0 ORDER BY created_at LIMIT 1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	if !strings.Contains(plan.String(), "idempotency_created") {
		t.Fatalf("the age query reads no index:\n%s", plan.String())
	}
}
