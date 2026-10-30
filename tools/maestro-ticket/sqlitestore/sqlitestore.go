// Package sqlitestore implements ticket.Store on SQLite in WAL mode.
//
// Concurrency model: every write runs in a BEGIN IMMEDIATE transaction, so
// writers across processes are serialized by SQLite's write lock and wait up
// to busy_timeout (5s) for it, with a short bounded retry on top. Readers use
// deferred transactions and never block writers. Because writes are
// serialized, events commit in seq order: a reader holding cursor N never
// later sees a new event with seq <= N.
package sqlitestore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

const (
	busyTimeoutMS = 5000
	maxBusyRetry  = 5
)

// migrations[i] moves the schema from version i to i+1. A new store runs
// every step in order, so fresh and upgraded stores share one path. Never
// edit a step that has shipped; add a new one.
var migrations = []string{schemaV1, schemaV2, schemaV3, schemaV4, schemaV5}

// schemaVersion is the version this build writes.
var schemaVersion = len(migrations)

const schemaV1 = `
CREATE TABLE IF NOT EXISTS tickets (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	title            TEXT    NOT NULL,
	body             TEXT    NOT NULL DEFAULT '',
	status           TEXT    NOT NULL,
	role             TEXT    NOT NULL DEFAULT '',
	priority         INTEGER NOT NULL,
	created_by       TEXT    NOT NULL,
	version          INTEGER NOT NULL DEFAULT 1,
	lease_holder     TEXT,
	lease_expires_at INTEGER,
	created_at       INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL,
	CHECK ((lease_holder IS NULL) = (lease_expires_at IS NULL))
);
CREATE INDEX IF NOT EXISTS tickets_queue ON tickets(status, priority, id);
CREATE INDEX IF NOT EXISTS tickets_holder ON tickets(lease_holder) WHERE lease_holder IS NOT NULL;

CREATE TABLE IF NOT EXISTS comments (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	ticket_id  INTEGER NOT NULL REFERENCES tickets(id),
	author     TEXT    NOT NULL,
	body       TEXT    NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS comments_ticket ON comments(ticket_id, id);

CREATE TABLE IF NOT EXISTS events (
	seq        INTEGER PRIMARY KEY AUTOINCREMENT,
	ticket_id  INTEGER NOT NULL REFERENCES tickets(id),
	type       TEXT    NOT NULL,
	actor      TEXT    NOT NULL,
	data       TEXT    NOT NULL DEFAULT '{}',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS events_ticket ON events(ticket_id, seq);
CREATE TRIGGER IF NOT EXISTS events_no_update BEFORE UPDATE ON events
	BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
CREATE TRIGGER IF NOT EXISTS events_no_delete BEFORE DELETE ON events
	BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;

CREATE TABLE IF NOT EXISTS idempotency (
	key          TEXT    PRIMARY KEY,
	op           TEXT    NOT NULL,
	request_hash TEXT    NOT NULL,
	result       TEXT    NOT NULL,
	created_at   INTEGER NOT NULL
);
`

// schemaV2 adds ticket type, owner, tries, the lease number used for
// fencing, and the status a blocked ticket came from. Live v1 claims get
// lease number 1 so every claim has a non-zero token. v1 never recorded
// where a ticket was blocked from, so those return to todo: the earliest
// active status, which never skips review.
const schemaV2 = `
ALTER TABLE tickets ADD COLUMN type TEXT NOT NULL DEFAULT 'ship' CHECK (type IN ('ship', 'scout'));
ALTER TABLE tickets ADD COLUMN owner TEXT NOT NULL DEFAULT '';
UPDATE tickets SET owner = created_by;
ALTER TABLE tickets ADD COLUMN tries INTEGER NOT NULL DEFAULT 0 CHECK (tries >= 0);
ALTER TABLE tickets ADD COLUMN lease_token INTEGER NOT NULL DEFAULT 0;
UPDATE tickets SET lease_token = 1 WHERE lease_holder IS NOT NULL;
ALTER TABLE tickets ADD COLUMN blocked_from TEXT;
UPDATE tickets SET blocked_from = 'todo' WHERE status = 'blocked';
`

// schemaV3 counts expired leases per ticket and adds the outbox, a delivery
// queue with one row per event. v5 drops it.
const schemaV3 = `
ALTER TABLE tickets ADD COLUMN lease_expiries INTEGER NOT NULL DEFAULT 0 CHECK (lease_expiries >= 0);
CREATE INDEX IF NOT EXISTS tickets_lease_expiry ON tickets(lease_expires_at) WHERE lease_holder IS NOT NULL;

CREATE TABLE IF NOT EXISTS outbox (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id        INTEGER NOT NULL UNIQUE REFERENCES events(seq),
	kind            TEXT    NOT NULL,
	payload         TEXT    NOT NULL,
	attempts        INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
	next_attempt_at INTEGER NOT NULL,
	delivered_at    INTEGER
);
CREATE INDEX IF NOT EXISTS outbox_pending ON outbox(next_attempt_at) WHERE delivered_at IS NULL;
INSERT INTO outbox (event_id, kind, payload, next_attempt_at, delivered_at)
	SELECT seq, type, json_object('seq', seq, 'ticket_id', 'T-' || ticket_id, 'type', type, 'actor', actor,
		'data', json(data), 'created_at', strftime('%Y-%m-%dT%H:%M:%fZ', created_at / 1000.0, 'unixepoch')),
		created_at, created_at
	FROM events;
`

// schemaV4 adds dependencies between tickets and the code areas each
// ticket touches.
const schemaV4 = `
CREATE TABLE IF NOT EXISTS deps (
	ticket_id  INTEGER NOT NULL REFERENCES tickets(id),
	depends_on INTEGER NOT NULL REFERENCES tickets(id),
	PRIMARY KEY (ticket_id, depends_on),
	CHECK (ticket_id != depends_on)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS deps_reverse ON deps(depends_on, ticket_id);

CREATE TABLE IF NOT EXISTS areas (
	ticket_id INTEGER NOT NULL REFERENCES tickets(id),
	area      TEXT    NOT NULL,
	PRIMARY KEY (ticket_id, area)
) WITHOUT ROWID;
`

// schemaV5 cuts the store to three roles and one review step, and drops
// the outbox, which nothing reads any more.
//
// Tickets in the removed statuses in_qa and ready_to_release go back to
// in_review, the last step they had passed, where the reviewer sets them
// done; blocked tickets that came from one return there too. Old role
// names map onto the three roles; any other role is left for doctor to
// report. Each change is logged as an event by maestro-ticket, so the log
// still explains every ticket's state. A status move bumps version, as a
// transition does.
const schemaV5 = `
INSERT INTO events (ticket_id, type, actor, data, created_at)
	SELECT id, 'transitioned', 'maestro-ticket',
		json_object('from', status, 'to', 'in_review', 'note', 'schema v5: status ' || status || ' removed'),
		CAST(unixepoch('subsec') * 1000 AS INTEGER)
	FROM tickets WHERE status IN ('in_qa', 'ready_to_release') ORDER BY id;
UPDATE tickets SET status = 'in_review', version = version + 1, updated_at = CAST(unixepoch('subsec') * 1000 AS INTEGER)
	WHERE status IN ('in_qa', 'ready_to_release');

INSERT INTO events (ticket_id, type, actor, data, created_at)
	SELECT id, 'updated', 'maestro-ticket',
		json_object('blocked_from', 'in_review', 'note', 'schema v5: status ' || blocked_from || ' removed'),
		CAST(unixepoch('subsec') * 1000 AS INTEGER)
	FROM tickets WHERE blocked_from IN ('in_qa', 'ready_to_release') ORDER BY id;
UPDATE tickets SET blocked_from = 'in_review', updated_at = CAST(unixepoch('subsec') * 1000 AS INTEGER)
	WHERE blocked_from IN ('in_qa', 'ready_to_release');

CREATE TEMP TABLE v5_roles (old TEXT PRIMARY KEY, new TEXT NOT NULL);
INSERT INTO v5_roles VALUES
	('planner', 'chief-of-staff'),
	('senior-software-engineer', 'engineer'), ('junior-software-engineer', 'engineer'),
	('code-reviewer', 'reviewer'), ('qa-lead', 'reviewer'), ('operations-manager', 'reviewer');
INSERT INTO events (ticket_id, type, actor, data, created_at)
	SELECT t.id, 'updated', 'maestro-ticket',
		json_object('role', r.new, 'note', 'schema v5: role ' || r.old || ' renamed'),
		CAST(unixepoch('subsec') * 1000 AS INTEGER)
	FROM tickets t JOIN v5_roles r ON r.old = t.role ORDER BY t.id;
UPDATE tickets SET role = (SELECT new FROM v5_roles WHERE old = tickets.role),
	updated_at = CAST(unixepoch('subsec') * 1000 AS INTEGER)
	WHERE role IN (SELECT old FROM v5_roles);
DROP TABLE v5_roles;

DROP TABLE IF EXISTS outbox;
`

// Options tune a Store. The zero value is fine for production.
type Options struct {
	// Now returns the current time. Nil (production) uses the database's
	// clock. Tests inject a fake one.
	Now func() time.Time
}

// Store is a ticket.Store backed by one SQLite file.
type Store struct {
	path string
	w    *sql.DB // writer pool: one connection, BEGIN IMMEDIATE
	r    *sql.DB // reader pool: deferred transactions
	now  func() time.Time

	// faultRead, when set by a test, runs after a read closure succeeds. An
	// error it returns is handled as if SQLite had returned it, so tests can
	// force a busy retry mid-read.
	faultRead func() error
}

var _ ticket.Store = (*Store)(nil)

// Open opens (creating if needed) the store at path and migrates the schema.
func Open(ctx context.Context, path string, opts Options) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty store path", ticket.ErrInvalid)
	}
	if strings.ContainsRune(path, '?') {
		return nil, fmt.Errorf("%w: store path must not contain '?'", ticket.ErrInvalid)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	params := fmt.Sprintf("_busy_timeout=%d&_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=1", busyTimeoutMS)
	w, err := sql.Open("sqlite", abs+"?"+params+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	r, err := sql.Open("sqlite", abs+"?"+params+"&_txlock=deferred")
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	s := &Store{path: abs, w: w, r: r, now: opts.Now}
	if err := s.migrate(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("open store %s: %w", abs, err)
	}
	return s, nil
}

// Path returns the absolute path of the database file.
func (s *Store) Path() string { return s.path }

// Close closes both pools.
func (s *Store) Close() error {
	return errors.Join(s.w.Close(), s.r.Close())
}

func (s *Store) migrate(ctx context.Context) error {
	var v int
	err := retryBusy(ctx, func() error {
		return s.w.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	})
	if err != nil {
		return err
	}
	if v == schemaVersion {
		return nil
	}
	if v > schemaVersion {
		return fmt.Errorf("schema version %d is newer than this maestro-ticket (%d); upgrade maestro-ticket", v, schemaVersion)
	}
	// All steps run in one transaction: the store is either fully on the new
	// version or untouched. Re-read the version under the write lock, since
	// another process may have migrated first.
	return s.write(ctx, func(tx *sql.Tx, now int64) error {
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
			return err
		}
		if v >= schemaVersion {
			return nil
		}
		for ; v < schemaVersion; v++ {
			if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
				return fmt.Errorf("migrate schema %d -> %d: %w", v, v+1, err)
			}
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion))
		return err
	})
}

// write runs fn in a BEGIN IMMEDIATE transaction, retrying briefly if the
// write lock stays busy past busy_timeout. now is read under the write lock,
// so writes see time move forward in commit order.
func (s *Store) write(ctx context.Context, fn func(tx *sql.Tx, now int64) error) error {
	return retryBusy(ctx, func() error {
		tx, err := s.w.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		now, err := s.clock(ctx, tx)
		if err == nil {
			err = fn(tx, now)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		return tx.Commit()
	})
}

func (s *Store) read(ctx context.Context, fn func(tx *sql.Tx, now int64) error) error {
	return retryBusy(ctx, func() error {
		tx, err := s.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		now, err := s.clock(ctx, tx)
		if err != nil {
			return err
		}
		if err := fn(tx, now); err != nil {
			return err
		}
		if s.faultRead != nil {
			return s.faultRead()
		}
		return nil
	})
}

func retryBusy(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < maxBusyRetry; attempt++ {
		if err = fn(); err == nil || !isBusy(err) {
			return err
		}
		d := time.Duration(25*(attempt+1)+rand.IntN(50)) * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	return err
}

func isBusy(err error) bool {
	var se interface{ Code() int } // *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case 5, 6: // SQLITE_BUSY, SQLITE_LOCKED
			return true
		}
	}
	return false
}

// clock returns the current time in ms. Without an injected clock it is the
// database's: every process using the store reads one clock, as a Postgres
// driver would use now().
func (s *Store) clock(ctx context.Context, q querier) (int64, error) {
	if s.now != nil {
		return s.now().UnixMilli(), nil
	}
	var ms int64
	err := q.QueryRowContext(ctx, "SELECT CAST(unixepoch('subsec') * 1000 AS INTEGER)").Scan(&ms)
	return ms, err
}

func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

const ticketCols = `id, title, body, type, status, role, priority, created_by, owner, tries, lease_expiries,
	blocked_from, version, lease_holder, lease_token, lease_expires_at, created_at, updated_at,
	(SELECT group_concat(depends_on, ',' ORDER BY depends_on) FROM deps WHERE deps.ticket_id = tickets.id),
	(SELECT group_concat(area, char(10) ORDER BY area) FROM areas WHERE areas.ticket_id = tickets.id)`

type scanner interface{ Scan(dest ...any) error }

func (s *Store) scanTicket(row scanner, nowMS int64) (ticket.Ticket, error) {
	var (
		t                ticket.Ticket
		typ, status      string
		blockedFrom      sql.NullString
		holder           sql.NullString
		token            int64
		expires          sql.NullInt64
		created, updated int64
		deps, areas      sql.NullString
	)
	err := row.Scan(&t.ID, &t.Title, &t.Body, &typ, &status, &t.Role, &t.Priority, &t.CreatedBy, &t.Owner,
		&t.Tries, &t.LeaseExpiries, &blockedFrom, &t.Version, &holder, &token, &expires, &created, &updated, &deps, &areas)
	if err != nil {
		return t, err
	}
	t.DependsOn, t.Areas = []ticket.ID{}, []string{}
	if deps.Valid {
		if t.DependsOn, err = ticket.ParseIDs(deps.String); err != nil {
			return t, err
		}
	}
	if areas.Valid {
		t.Areas = strings.Split(areas.String, "\n")
	}
	t.Type, t.Status, t.BlockedFrom = ticket.Type(typ), ticket.Status(status), ticket.Status(blockedFrom.String)
	t.CreatedAt, t.UpdatedAt = msTime(created), msTime(updated)
	if holder.Valid {
		t.Claim = &ticket.Claim{
			Holder:    holder.String,
			Token:     token,
			ExpiresAt: msTime(expires.Int64),
			Expired:   expires.Int64 <= nowMS,
		}
	}
	return t, nil
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *Store) getTicket(ctx context.Context, q querier, id ticket.ID, nowMS int64) (ticket.Ticket, error) {
	t, err := s.scanTicket(q.QueryRowContext(ctx, "SELECT "+ticketCols+" FROM tickets WHERE id = ?", id), nowMS)
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("%w: ticket %s", ticket.ErrNotFound, id)
	}
	return t, err
}

// appendEvent logs an event in the caller's transaction, so the change and
// its event commit together or not at all.
func appendEvent(ctx context.Context, tx *sql.Tx, id ticket.ID, typ, actor string, data any, nowMS int64) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO events (ticket_id, type, actor, data, created_at) VALUES (?, ?, ?, ?, ?)",
		id, typ, actor, string(b), nowMS)
	return err
}

// loadForWrite reads a ticket for a write. An expired lease on it is first
// freed and logged (expireLease), so no write clears or overwrites an
// expired lease without a lease_expired event.
func (s *Store) loadForWrite(ctx context.Context, tx *sql.Tx, id ticket.ID, now int64) (ticket.Ticket, error) {
	t, err := s.getTicket(ctx, tx, id, now)
	if err != nil || t.Claim == nil || !t.Claim.Expired {
		return t, err
	}
	return s.expireLease(ctx, tx, t, now)
}

// expireLease frees t's expired lease, counts it and logs lease_expired with
// the holder that lost it. The store is the actor, not whoever noticed.
// Version doesn't change: the lease already read as expired, so a caller's
// --expect-version from before the expiry still matches. The lease token
// is kept, so the next claim still gets a larger one.
func (s *Store) expireLease(ctx context.Context, tx *sql.Tx, t ticket.Ticket, now int64) (ticket.Ticket, error) {
	c := t.Claim
	res, err := tx.ExecContext(ctx, `UPDATE tickets
		SET lease_holder = NULL, lease_expires_at = NULL, lease_expiries = lease_expiries + 1, updated_at = ?
		WHERE id = ? AND lease_holder = ? AND lease_token = ? AND lease_expires_at <= ?`,
		now, t.ID, c.Holder, c.Token, now)
	if err != nil {
		return t, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return t, fmt.Errorf("%w: %s lease changed while expiring it", ticket.ErrVersionConflict, t.ID)
	}
	err = appendEvent(ctx, tx, t.ID, ticket.EventLeaseExpired, ticket.SystemActor, map[string]any{
		"holder": c.Holder, "token": c.Token, "expired_at": c.ExpiresAt, "expiries": t.LeaseExpiries + 1,
	}, now)
	if err != nil {
		return t, err
	}
	return s.getTicket(ctx, tx, t.ID, now)
}

// Checkpoint copies committed WAL frames into the database file without
// waiting on readers or writers (PASSIVE), so the WAL stays short even when
// SQLite's own checkpoint on commit keeps losing to busy readers. It
// returns the WAL's size in frames and how many are now in the database.
// SQLite only; reap calls it.
func (s *Store) Checkpoint(ctx context.Context) (walFrames, checkpointed int, err error) {
	var busy int
	err = s.w.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &walFrames, &checkpointed)
	return walFrames, checkpointed, err
}

// reapBatch caps how many leases one reap transaction frees, so a backlog of
// expired leases after an outage doesn't hold the write lock for long.
const reapBatch = 100

// Reap implements ticket.Store.
func (s *Store) Reap(ctx context.Context) (ticket.ReapResult, error) {
	res := ticket.ReapResult{Reaped: []ticket.ID{}}
	for {
		var batch []ticket.ID
		err := s.write(ctx, func(tx *sql.Tx, now int64) error {
			batch = nil // reset: a busy retry reruns this closure
			rows, err := tx.QueryContext(ctx, `SELECT id FROM tickets
				WHERE lease_holder IS NOT NULL AND lease_expires_at <= ? ORDER BY lease_expires_at, id LIMIT ?`, now, reapBatch)
			if err != nil {
				return err
			}
			var ids []ticket.ID
			for rows.Next() {
				var id ticket.ID
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, id := range ids {
				if _, err := s.loadForWrite(ctx, tx, id, now); err != nil {
					return err
				}
			}
			batch = ids
			return nil
		})
		if err != nil {
			return res, err
		}
		res.Reaped = append(res.Reaped, batch...)
		if len(batch) < reapBatch {
			return res, nil
		}
	}
}

// idempotent runs do once per key. A retry with the same op and request
// replays the stored result; a different request under the same key fails.
// When replay is set, it vets the stored result against the current state
// and returns what the caller gets instead, for results that can go stale (a
// claim).
func idempotent[T any](ctx context.Context, tx *sql.Tx, key, op string, req any, nowMS int64,
	do func() (T, error), replay func(T) (T, error)) (T, error) {
	var zero T
	if key == "" {
		return do()
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return zero, err
	}
	sum := sha256.Sum256(append([]byte(op+"\x00"), reqJSON...))
	hash := hex.EncodeToString(sum[:])

	var gotOp, gotHash, result string
	err = tx.QueryRowContext(ctx, "SELECT op, request_hash, result FROM idempotency WHERE key = ?", key).
		Scan(&gotOp, &gotHash, &result)
	switch {
	case err == nil:
		if gotOp != op || gotHash != hash {
			return zero, fmt.Errorf("%w: key %q was used for %s", ticket.ErrIdempotencyConflict, key, gotOp)
		}
		var out T
		if err := json.Unmarshal([]byte(result), &out); err != nil {
			return zero, fmt.Errorf("decode stored result for key %q: %w", key, err)
		}
		if replay != nil {
			return replay(out)
		}
		return out, nil
	case !errors.Is(err, sql.ErrNoRows):
		return zero, err
	}

	out, err := do()
	if err != nil {
		return zero, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return zero, err
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO idempotency (key, op, request_hash, result, created_at) VALUES (?, ?, ?, ?, ?)",
		key, op, hash, string(b), nowMS)
	return out, err
}

// Create implements ticket.Store.
func (s *Store) Create(ctx context.Context, req ticket.CreateRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *sql.Tx, now int64) error {
		var err error
		out, err = idempotent(ctx, tx, req.IdempotencyKey, "create", req, now, func() (ticket.Ticket, error) {
			var blockedFrom any
			if req.Status == ticket.StatusBlocked {
				blockedFrom = ticket.StatusTodo
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO tickets
				(title, body, type, status, role, priority, created_by, owner, blocked_from, version, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
				req.Title, req.Body, req.Type, req.Status, req.Role, *req.Priority, req.Actor, req.Owner, blockedFrom, now, now)
			if err != nil {
				return ticket.Ticket{}, err
			}
			n, err := res.LastInsertId()
			if err != nil {
				return ticket.Ticket{}, err
			}
			id := ticket.ID(n)
			if err := addDeps(ctx, tx, id, req.DependsOn); err != nil {
				return ticket.Ticket{}, err
			}
			if err := setAreas(ctx, tx, id, req.Areas); err != nil {
				return ticket.Ticket{}, err
			}
			data := map[string]any{
				"title": req.Title, "type": req.Type, "status": req.Status, "role": req.Role,
				"priority": *req.Priority, "owner": req.Owner,
			}
			if len(req.DependsOn) > 0 {
				data["depends_on"] = req.DependsOn
			}
			if len(req.Areas) > 0 {
				data["areas"] = req.Areas
			}
			err = appendEvent(ctx, tx, id, ticket.EventCreated, req.Actor, data, now)
			if err != nil {
				return ticket.Ticket{}, err
			}
			return s.getTicket(ctx, tx, id, now)
		}, nil)
		return err
	})
	return out, err
}

// addDeps records that id depends on each of deps. Every dependency must
// exist, and none may already depend on id, directly or not: a cycle would
// leave every ticket in it waiting forever.
func addDeps(ctx context.Context, tx *sql.Tx, id ticket.ID, deps []ticket.ID) error {
	for _, d := range deps {
		var one int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM tickets WHERE id = ?", d).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: dependency %s", ticket.ErrNotFound, d)
		}
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `WITH RECURSIVE up(id) AS (
				SELECT ? UNION SELECT deps.depends_on FROM deps JOIN up ON deps.ticket_id = up.id)
			SELECT 1 FROM up WHERE id = ?`, d, id).Scan(&one)
		if err == nil {
			return fmt.Errorf("%w: %s already depends on %s; adding %s -> %s makes a cycle", ticket.ErrInvalid, d, id, id, d)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO deps (ticket_id, depends_on) VALUES (?, ?)", id, d); err != nil {
			return err
		}
	}
	return nil
}

// setAreas replaces id's areas.
func setAreas(ctx context.Context, tx *sql.Tx, id ticket.ID, areas []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM areas WHERE ticket_id = ?", id); err != nil {
		return err
	}
	for _, a := range areas {
		if _, err := tx.ExecContext(ctx, "INSERT INTO areas (ticket_id, area) VALUES (?, ?)", id, a); err != nil {
			return err
		}
	}
	return nil
}

// Update implements ticket.Store. It doesn't change version: the claim
// rules read dependencies and areas live, and a holder's --expect-version
// shouldn't fail because someone added a dependency.
func (s *Store) Update(ctx context.Context, req ticket.UpdateRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *sql.Tx, now int64) error {
		var err error
		out, err = idempotent(ctx, tx, req.IdempotencyKey, "update", req, now, func() (ticket.Ticket, error) {
			t, err := s.loadForWrite(ctx, tx, req.ID, now)
			if err != nil {
				return t, err
			}
			if err := t.CheckFence(req.Holder, req.Token); err != nil {
				return t, err
			}
			// A working claim already holds its areas: new ones must be free.
			if req.Areas != nil && t.LiveClaim() != nil && t.Status.WritesCode() {
				if err := checkAreas(ctx, tx, t.ID, *req.Areas, now); err != nil {
					return t, err
				}
			}
			for _, d := range req.RemoveDeps {
				if _, err := tx.ExecContext(ctx, "DELETE FROM deps WHERE ticket_id = ? AND depends_on = ?", t.ID, d); err != nil {
					return t, err
				}
			}
			if err := addDeps(ctx, tx, t.ID, req.AddDeps); err != nil {
				return t, err
			}
			data := map[string]any{}
			if len(req.AddDeps) > 0 {
				data["add_deps"] = req.AddDeps
			}
			if len(req.RemoveDeps) > 0 {
				data["remove_deps"] = req.RemoveDeps
			}
			if req.Areas != nil {
				if err := setAreas(ctx, tx, t.ID, *req.Areas); err != nil {
					return t, err
				}
				data["areas"] = *req.Areas
			}
			if _, err := tx.ExecContext(ctx, "UPDATE tickets SET updated_at = ? WHERE id = ?", now, t.ID); err != nil {
				return t, err
			}
			if err := appendEvent(ctx, tx, t.ID, ticket.EventUpdated, req.Actor, data, now); err != nil {
				return t, err
			}
			return s.getTicket(ctx, tx, t.ID, now)
		}, nil)
		return err
	})
	return out, err
}

// Get implements ticket.Store.
func (s *Store) Get(ctx context.Context, id ticket.ID) (ticket.Detail, error) {
	var d ticket.Detail
	err := s.read(ctx, func(tx *sql.Tx, now int64) error {
		t, err := s.getTicket(ctx, tx, id, now)
		if err != nil {
			return err
		}
		d = ticket.Detail{Ticket: t, Comments: []ticket.Comment{}}
		rows, err := tx.QueryContext(ctx,
			"SELECT id, ticket_id, author, body, created_at FROM comments WHERE ticket_id = ? ORDER BY id", id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanComment(rows)
			if err != nil {
				return err
			}
			d.Comments = append(d.Comments, c)
		}
		return rows.Err()
	})
	return d, err
}

// List implements ticket.Store. Results are ordered by priority, then id.
func (s *Store) List(ctx context.Context, f ticket.ListFilter) ([]ticket.Ticket, error) {
	var out []ticket.Ticket
	err := s.read(ctx, func(tx *sql.Tx, now int64) error {
		out = []ticket.Ticket{} // reset: a busy retry reruns this closure
		var where []string
		var args []any
		if len(f.Statuses) > 0 {
			where = append(where, "status IN ("+placeholders(len(f.Statuses))+")")
			for _, st := range f.Statuses {
				args = append(args, string(st))
			}
		}
		if f.Role != "" {
			where = append(where, "role = ?")
			args = append(args, f.Role)
		}
		if f.Holder != "" {
			where = append(where, "lease_holder = ? AND lease_expires_at > ?")
			args = append(args, f.Holder, now)
		}
		if f.Claimed != nil {
			if *f.Claimed {
				where = append(where, "lease_holder IS NOT NULL AND lease_expires_at > ?")
			} else {
				where = append(where, "(lease_holder IS NULL OR lease_expires_at <= ?)")
			}
			args = append(args, now)
		}
		q := "SELECT " + ticketCols + " FROM tickets"
		if len(where) > 0 {
			q += " WHERE " + strings.Join(where, " AND ")
		}
		q += " ORDER BY priority, id"
		if f.Limit > 0 {
			q += " LIMIT ?"
			args = append(args, f.Limit)
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := s.scanTicket(rows, now)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

// ClaimNext implements ticket.Store.
func (s *Store) ClaimNext(ctx context.Context, req ticket.ClaimRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *sql.Tx, now int64) error {
		var err error
		out, err = idempotent(ctx, tx, req.IdempotencyKey, "claim", req, now, func() (ticket.Ticket, error) {
			t, err := s.pickClaimable(ctx, tx, req, now)
			if err != nil {
				return t, err
			}
			return s.applyClaim(ctx, tx, t, req, now)
		}, func(prev ticket.Ticket) (ticket.Ticket, error) {
			// A replayed claim is only true while that lease is still live.
			// Return the ticket as it is now, so the session's next
			// --from or --expect-version check matches.
			cur, err := s.getTicket(ctx, tx, prev.ID, now)
			if err != nil {
				return cur, err
			}
			c := cur.LiveClaim()
			if prev.Claim == nil || c == nil || c.Holder != req.Holder || c.Token != prev.Claim.Token {
				return ticket.Ticket{}, fmt.Errorf("%w: claim %q of %s by %s is no longer live; claim again with a new key",
					ticket.ErrLeaseLost, req.IdempotencyKey, prev.ID, req.Holder)
			}
			return cur, nil
		})
		return err
	})
	return out, err
}

func (s *Store) pickClaimable(ctx context.Context, tx *sql.Tx, req ticket.ClaimRequest, now int64) (ticket.Ticket, error) {
	if req.ID != 0 {
		t, err := s.getTicket(ctx, tx, req.ID, now)
		if err != nil {
			return t, err
		}
		if t.Status.Terminal() {
			return t, fmt.Errorf("%w: %s is %s", ticket.ErrStatusConflict, t.ID, t.Status)
		}
		if len(req.Statuses) > 0 && !containsStatus(req.Statuses, t.Status) {
			return t, fmt.Errorf("%w: %s is %s", ticket.ErrStatusConflict, t.ID, t.Status)
		}
		if req.Role != "" && t.Role != req.Role {
			return t, fmt.Errorf("%w: %s is for role %q", ticket.ErrStatusConflict, t.ID, t.Role)
		}
		c := t.LiveClaim()
		if c != nil && c.Holder != req.Holder {
			return t, fmt.Errorf("%w: %s is held by %s until %s", ticket.ErrClaimed, t.ID, c.Holder, c.ExpiresAt.Format(time.RFC3339))
		}
		// Grooming, review and answering an escalation change
		// no code, and renewing a lease you hold starts nothing new.
		if !t.Status.WritesCode() || c != nil {
			return t, nil
		}
		return t, checkStart(ctx, tx, t, now)
	}
	busy, err := busyAreas(ctx, tx, 0, now)
	if err != nil {
		return ticket.Ticket{}, err
	}
	where := []string{
		"status IN (" + placeholders(len(req.Statuses)) + ")",
		"(lease_holder IS NULL OR lease_expires_at <= ?)",
		`(status NOT IN ('todo', 'in_progress') OR NOT EXISTS (SELECT 1 FROM deps JOIN tickets dep ON dep.id = deps.depends_on
			WHERE deps.ticket_id = tickets.id AND dep.status != 'done'))`,
	}
	var args []any
	for _, st := range req.Statuses {
		args = append(args, string(st))
	}
	args = append(args, now)
	if req.Role != "" {
		where = append(where, "role = ?")
		args = append(args, req.Role)
	}
	q := "SELECT " + ticketCols + " FROM tickets WHERE " + strings.Join(where, " AND ") + " ORDER BY priority, id"
	if len(busy.areas) == 0 {
		q += " LIMIT 1" // nothing to skip: the first match wins
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return ticket.Ticket{}, err
	}
	defer rows.Close()
	for rows.Next() {
		t, err := s.scanTicket(rows, now)
		if err != nil {
			return t, err
		}
		if _, _, ok := ticket.AnyAreaOverlaps(t.Areas, busy.areas); !ok || !t.Status.WritesCode() {
			return t, nil
		}
	}
	if err := rows.Err(); err != nil {
		return ticket.Ticket{}, err
	}
	return ticket.Ticket{}, ticket.ErrNoWork
}

// checkStart refuses work on t while a dependency isn't done or one of its
// areas overlaps a live claim's. A claim by id in todo or in_progress runs
// it, and so does a held ticket moving into those, since its claim in
// another status skipped it.
func checkStart(ctx context.Context, tx *sql.Tx, t ticket.Ticket, now int64) error {
	pending, err := pendingDeps(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("%w: %s waits on %v, not done yet", ticket.ErrWaitingOnDeps, t.ID, pending)
	}
	return checkAreas(ctx, tx, t.ID, t.Areas, now)
}

// checkAreas refuses areas that overlap those of a live claim on another
// ticket.
func checkAreas(ctx context.Context, tx *sql.Tx, id ticket.ID, areas []string, now int64) error {
	busy, err := busyAreas(ctx, tx, id, now)
	if err != nil {
		return err
	}
	if mine, theirs, ok := ticket.AnyAreaOverlaps(areas, busy.areas); ok {
		return fmt.Errorf("%w: %s area %s overlaps %s on %s, which is claimed", ticket.ErrAreaBusy, id, mine, theirs, busy.owner[theirs])
	}
	return nil
}

// pendingDeps lists the tickets id depends on that aren't done. Only done
// counts: a cancelled dependency keeps id waiting until someone removes it.
func pendingDeps(ctx context.Context, tx *sql.Tx, id ticket.ID) ([]ticket.ID, error) {
	rows, err := tx.QueryContext(ctx, `SELECT deps.depends_on FROM deps JOIN tickets dep ON dep.id = deps.depends_on
		WHERE deps.ticket_id = ? AND dep.status != 'done' ORDER BY deps.depends_on`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ticket.ID
	for rows.Next() {
		var d ticket.ID
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type areaSet struct {
	areas []string
	owner map[string]ticket.ID
}

// busyAreas lists the areas of tickets under a live claim in todo or
// in_progress, other than except. Claims in other statuses change no code,
// so they don't count.
func busyAreas(ctx context.Context, tx *sql.Tx, except ticket.ID, now int64) (areaSet, error) {
	set := areaSet{owner: map[string]ticket.ID{}}
	rows, err := tx.QueryContext(ctx, `SELECT areas.area, tickets.id FROM areas JOIN tickets ON tickets.id = areas.ticket_id
		WHERE tickets.lease_holder IS NOT NULL AND tickets.lease_expires_at > ? AND tickets.status IN ('todo', 'in_progress') AND tickets.id != ?`,
		now, except)
	if err != nil {
		return set, err
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		var id ticket.ID
		if err := rows.Scan(&a, &id); err != nil {
			return set, err
		}
		set.areas = append(set.areas, a)
		set.owner[a] = id
	}
	return set, rows.Err()
}

// applyClaim leases t to req.Holder. Every claim, including a holder
// re-claiming its own live lease, takes the next lease token and bumps the
// version, so writes carrying an older token are fenced out.
func (s *Store) applyClaim(ctx context.Context, tx *sql.Tx, t ticket.Ticket, req ticket.ClaimRequest, now int64) (ticket.Ticket, error) {
	expires := now + req.TTL.Milliseconds()
	if t.Claim != nil && t.Claim.Expired {
		var err error
		if t, err = s.expireLease(ctx, tx, t, now); err != nil {
			return t, err
		}
	}
	renewed := t.Claim != nil && t.Claim.Holder == req.Holder
	res, err := tx.ExecContext(ctx, `UPDATE tickets
		SET lease_holder = ?, lease_expires_at = ?, lease_token = lease_token + 1, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`, req.Holder, expires, now, t.ID, t.Version)
	if err != nil {
		return t, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return t, fmt.Errorf("%w: %s changed during claim", ticket.ErrVersionConflict, t.ID)
	}
	got, err := s.getTicket(ctx, tx, t.ID, now)
	if err != nil {
		return got, err
	}
	data := map[string]any{
		"holder": req.Holder, "token": got.Claim.Token, "expires_at": msTime(expires), "status": t.Status,
	}
	if renewed {
		data["renewed"] = true
	}
	if err := appendEvent(ctx, tx, t.ID, ticket.EventClaimed, req.Actor, data, now); err != nil {
		return got, err
	}
	return got, nil
}

func containsStatus(list []ticket.Status, s ticket.Status) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Heartbeat implements ticket.Store. It fails with ErrLeaseLost unless the
// live lease is the caller's, with the caller's token; the session must stop
// work and re-claim.
func (s *Store) Heartbeat(ctx context.Context, req ticket.HeartbeatRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *sql.Tx, now int64) error {
		t, err := s.getTicket(ctx, tx, req.ID, now)
		if err != nil {
			return err
		}
		c := t.LiveClaim()
		if c == nil || c.Holder != req.Holder || c.Token != req.Token {
			return fmt.Errorf("%w: %s has no live lease by %s with token %d", ticket.ErrLeaseLost, t.ID, req.Holder, req.Token)
		}
		// Guard on the token too, so the extension can only land on the
		// lease that was checked.
		res, err := tx.ExecContext(ctx, "UPDATE tickets SET lease_expires_at = ? WHERE id = ? AND lease_holder = ? AND lease_token = ?",
			now+req.TTL.Milliseconds(), t.ID, req.Holder, req.Token)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("%w: %s lease changed during heartbeat", ticket.ErrLeaseLost, t.ID)
		}
		out, err = s.getTicket(ctx, tx, t.ID, now)
		return err
	})
	return out, err
}

// Release implements ticket.Store.
func (s *Store) Release(ctx context.Context, req ticket.ReleaseRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *sql.Tx, now int64) error {
		var err error
		out, err = idempotent(ctx, tx, req.IdempotencyKey, "release", req, now, func() (ticket.Ticket, error) {
			// An expired lease is freed and logged as lease_expired first,
			// even our own: it ran out, it wasn't released.
			t, err := s.loadForWrite(ctx, tx, req.ID, now)
			if err != nil {
				return t, err
			}
			switch {
			case t.Claim == nil:
				return t, nil // already gone: nothing of ours to release
			case t.Claim.Holder != req.Holder:
				return t, fmt.Errorf("%w: %s is held by %s", ticket.ErrClaimed, t.ID, t.Claim.Holder)
			case req.Token != t.Claim.Token:
				return t, fmt.Errorf("%w: %s lease token is %d, not %d; it was re-claimed", ticket.ErrLeaseLost, t.ID, t.Claim.Token, req.Token)
			}
			if err := s.clearLease(ctx, tx, t, now); err != nil {
				return t, err
			}
			if err := appendEvent(ctx, tx, t.ID, ticket.EventReleased, req.Actor, map[string]any{"holder": req.Holder, "token": req.Token}, now); err != nil {
				return t, err
			}
			return s.getTicket(ctx, tx, t.ID, now)
		}, nil)
		return err
	})
	return out, err
}

func (s *Store) clearLease(ctx context.Context, tx *sql.Tx, t ticket.Ticket, now int64) error {
	res, err := tx.ExecContext(ctx, `UPDATE tickets
		SET lease_holder = NULL, lease_expires_at = NULL, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`, now, t.ID, t.Version)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: %s changed during release", ticket.ErrVersionConflict, t.ID)
	}
	return nil
}

// Transition implements ticket.Store.
func (s *Store) Transition(ctx context.Context, req ticket.TransitionRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *sql.Tx, now int64) error {
		var err error
		out, err = idempotent(ctx, tx, req.IdempotencyKey, "transition", req, now, func() (ticket.Ticket, error) {
			return s.applyTransition(ctx, tx, req, now)
		}, nil)
		return err
	})
	return out, err
}

func (s *Store) applyTransition(ctx context.Context, tx *sql.Tx, req ticket.TransitionRequest, now int64) (ticket.Ticket, error) {
	t, err := s.loadForWrite(ctx, tx, req.ID, now)
	if err != nil {
		return t, err
	}
	if req.From != "" && t.Status != req.From {
		return t, fmt.Errorf("%w: %s is %s, not %s", ticket.ErrStatusConflict, t.ID, t.Status, req.From)
	}
	if req.ExpectVersion != 0 && t.Version != req.ExpectVersion {
		return t, fmt.Errorf("%w: %s is at version %d, not %d", ticket.ErrVersionConflict, t.ID, t.Version, req.ExpectVersion)
	}
	if err := t.CheckFence(req.Holder, req.Token); err != nil {
		return t, err
	}
	if !t.CanMove(req.To) {
		return t, fmt.Errorf("%w: %s ticket %s -> %s (allowed: %v)", ticket.ErrInvalidTransition, t.Type, t.Status, req.To, t.NextStatuses())
	}
	if err := t.CheckGate(req.To, req.Actor, req.ActorRole); err != nil {
		return t, err
	}
	// A held ticket moving into work holds its areas from now on: check
	// what its claim in another status skipped.
	if t.LiveClaim() != nil && !req.Release && !t.Status.WritesCode() && req.To.WritesCode() {
		if err := checkStart(ctx, tx, t, now); err != nil {
			return t, err
		}
	}
	role := t.Role
	if req.Role != "" {
		role = req.Role
	}
	var blockedFrom any // cleared on every move except into blocked
	if req.To == ticket.StatusBlocked {
		blockedFrom = t.Status
	}
	tries := t.Tries
	if t.Type == ticket.TypeShip && ticket.IsSendBack(t.Status, req.To) {
		tries++
	}
	release := t.Claim != nil && (req.Release || req.To.Terminal())
	q := "UPDATE tickets SET status = ?, role = ?, blocked_from = ?, tries = ?, version = version + 1, updated_at = ?"
	if release {
		q += ", lease_holder = NULL, lease_expires_at = NULL"
	}
	res, err := tx.ExecContext(ctx, q+" WHERE id = ? AND version = ?", req.To, role, blockedFrom, tries, now, t.ID, t.Version)
	if err != nil {
		return t, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return t, fmt.Errorf("%w: %s changed during transition", ticket.ErrVersionConflict, t.ID)
	}
	data := map[string]any{"from": t.Status, "to": req.To}
	if req.Note != "" {
		data["note"] = req.Note
	}
	if role != t.Role {
		data["role"] = role
	}
	if req.ActorRole != "" {
		data["actor_role"] = req.ActorRole
	}
	if tries != t.Tries {
		data["tries"] = tries
	}
	if release {
		data["released"] = t.Claim.Holder
	}
	if err := appendEvent(ctx, tx, t.ID, ticket.EventTransitioned, req.Actor, data, now); err != nil {
		return t, err
	}
	return s.getTicket(ctx, tx, t.ID, now)
}

// Comment implements ticket.Store.
func (s *Store) Comment(ctx context.Context, req ticket.CommentRequest) (ticket.Comment, error) {
	if err := req.Validate(); err != nil {
		return ticket.Comment{}, err
	}
	var out ticket.Comment
	err := s.write(ctx, func(tx *sql.Tx, now int64) error {
		var err error
		out, err = idempotent(ctx, tx, req.IdempotencyKey, "comment", req, now, func() (ticket.Comment, error) {
			t, err := s.loadForWrite(ctx, tx, req.ID, now)
			if err != nil {
				return ticket.Comment{}, err
			}
			if err := t.CheckFence(req.Holder, req.Token); err != nil {
				return ticket.Comment{}, err
			}
			res, err := tx.ExecContext(ctx,
				"INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, ?, ?, ?)",
				req.ID, req.Actor, req.Body, now)
			if err != nil {
				return ticket.Comment{}, err
			}
			cid, err := res.LastInsertId()
			if err != nil {
				return ticket.Comment{}, err
			}
			if err := appendEvent(ctx, tx, req.ID, ticket.EventCommented, req.Actor, map[string]any{"comment_id": cid}, now); err != nil {
				return ticket.Comment{}, err
			}
			return ticket.Comment{ID: cid, TicketID: req.ID, Author: req.Actor, Body: req.Body, CreatedAt: msTime(now)}, nil
		}, nil)
		return err
	})
	return out, err
}

func scanComment(row scanner) (ticket.Comment, error) {
	var c ticket.Comment
	var created int64
	err := row.Scan(&c.ID, &c.TicketID, &c.Author, &c.Body, &created)
	c.CreatedAt = msTime(created)
	return c, err
}

const eventCols = "seq, ticket_id, type, actor, data, created_at"

func scanEvent(row scanner) (ticket.Event, error) {
	var e ticket.Event
	var data string
	var created int64
	err := row.Scan(&e.Seq, &e.TicketID, &e.Type, &e.Actor, &data, &created)
	e.Data = json.RawMessage(data)
	e.CreatedAt = msTime(created)
	return e, err
}

// Events implements ticket.Store.
func (s *Store) Events(ctx context.Context, f ticket.EventFilter) ([]ticket.Event, error) {
	var out []ticket.Event
	err := s.read(ctx, func(tx *sql.Tx, now int64) error {
		out = []ticket.Event{} // reset: a busy retry reruns this closure
		q := "SELECT " + eventCols + " FROM events WHERE seq > ?"
		args := []any{f.Since}
		if f.TicketID != 0 {
			q += " AND ticket_id = ?"
			args = append(args, f.TicketID)
		}
		if f.Type != "" {
			q += " AND type = ?"
			args = append(args, f.Type)
		}
		q += " ORDER BY seq"
		if f.Limit > 0 {
			q += " LIMIT ?"
			args = append(args, f.Limit)
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEvent(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// LastSeq implements ticket.Store.
func (s *Store) LastSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := s.read(ctx, func(tx *sql.Tx, now int64) error {
		return tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM events").Scan(&seq)
	})
	return seq, err
}

// Doctor implements ticket.Store. It inspects the writer connection, since
// that is the one whose settings protect the data.
func (s *Store) Doctor(ctx context.Context) (ticket.DoctorReport, error) {
	var rep ticket.DoctorReport
	add := func(name string, ok bool, format string, args ...any) {
		rep.Checks = append(rep.Checks, ticket.Check{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...)})
	}
	conn, err := s.w.Conn(ctx)
	if err != nil {
		return rep, err
	}
	defer conn.Close()

	pragma := func(name string) (string, error) {
		var v string
		err := conn.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v)
		return v, err
	}
	expect := []struct{ pragma, want, label string }{
		{"user_version", fmt.Sprint(schemaVersion), "schema"},
		{"journal_mode", "wal", "journal_mode"},
		{"synchronous", "1", "synchronous"},
		{"busy_timeout", fmt.Sprint(busyTimeoutMS), "busy_timeout"},
		{"foreign_keys", "1", "foreign_keys"},
		{"quick_check", "ok", "integrity"},
	}
	for _, e := range expect {
		got, err := pragma(e.pragma)
		if err != nil {
			return rep, fmt.Errorf("PRAGMA %s: %w", e.pragma, err)
		}
		add(e.label, strings.EqualFold(got, e.want), "%s = %s (want %s)", e.pragma, got, e.want)
	}

	var fkViolations int
	rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return rep, err
	}
	for rows.Next() {
		fkViolations++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, fmt.Errorf("PRAGMA foreign_key_check: %w", err)
	}
	add("foreign_key_check", fkViolations == 0, "%d violations", fkViolations)

	now, err := s.clock(ctx, conn)
	if err != nil {
		return rep, err
	}
	var live, expired, total int
	err = conn.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(lease_holder IS NOT NULL AND lease_expires_at > ?), 0),
		COALESCE(SUM(lease_holder IS NOT NULL AND lease_expires_at <= ?), 0)
		FROM tickets`, now, now).Scan(&total, &live, &expired)
	if err != nil {
		return rep, err
	}
	add("leases", true, "%d tickets; %d live claims, %d expired (claimable; reap frees them)", total, live, expired)

	// Statuses and roles are checked in Go, not by the schema: list the
	// open tickets whose values this build doesn't know.
	var badStatus, badRole []string
	vrows, err := conn.QueryContext(ctx, "SELECT id, status, role FROM tickets WHERE status NOT IN ('done', 'cancelled') ORDER BY id")
	if err != nil {
		return rep, err
	}
	for vrows.Next() {
		var id ticket.ID
		var status, role string
		if err := vrows.Scan(&id, &status, &role); err != nil {
			vrows.Close()
			return rep, err
		}
		if !ticket.Status(status).Valid() {
			badStatus = append(badStatus, fmt.Sprintf("%s (%s)", id, status))
		}
		if role != "" && !ticket.ValidRole(role) {
			badRole = append(badRole, fmt.Sprintf("%s (%s)", id, role))
		}
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return rep, err
	}
	if len(badStatus) == 0 {
		add("statuses", true, "every open ticket has a known status")
	} else {
		add("statuses", false, "unknown status on %s", strings.Join(badStatus, ", "))
	}
	if len(badRole) == 0 {
		add("roles", true, "every open ticket has a known role or none")
	} else {
		add("roles", false, "unknown role on %s (want %s; fix with transition --role)",
			strings.Join(badRole, ", "), strings.Join(ticket.Roles, ", "))
	}

	var lastSeq int64
	var triggers int
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM events").Scan(&lastSeq); err != nil {
		return rep, err
	}
	err = conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name IN ('events_no_update', 'events_no_delete')").Scan(&triggers)
	if err != nil {
		return rep, err
	}
	add("events", triggers == 2, "last seq %d; append-only triggers %d/2", lastSeq, triggers)
	return rep, nil
}

// Backup implements ticket.Store. VACUUM INTO reads one snapshot through a
// reader connection, so writers carry on, and writes a compact file with no
// WAL. The result is opened and checked before Backup returns.
func (s *Store) Backup(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("%w: backup %s already exists", ticket.ErrInvalid, abs)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	err = retryBusy(ctx, func() error {
		_, err := s.r.ExecContext(ctx, "VACUUM INTO ?", abs)
		return err
	})
	if err != nil {
		os.Remove(abs)
		return fmt.Errorf("backup to %s: %w", abs, err)
	}
	if err := checkBackup(ctx, abs); err != nil {
		os.Remove(abs)
		return err
	}
	return nil
}

func checkBackup(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	var check string
	var v int
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil {
		return fmt.Errorf("check backup %s: %w", path, err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("check backup %s: %w", path, err)
	}
	if check != "ok" || v != schemaVersion {
		return fmt.Errorf("backup %s failed its check: quick_check %q, schema %d (want ok, %d)", path, check, v, schemaVersion)
	}
	return nil
}

// Export implements ticket.Store.
func (s *Store) Export(ctx context.Context) (ticket.Export, error) {
	var out ticket.Export
	err := s.read(ctx, func(tx *sql.Tx, now int64) error {
		out = ticket.Export{SchemaVersion: schemaVersion, Tickets: []ticket.Detail{}, Events: []ticket.Event{}}
		byID := map[ticket.ID]int{}
		rows, err := tx.QueryContext(ctx, "SELECT "+ticketCols+" FROM tickets ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := s.scanTicket(rows, now)
			if err != nil {
				return err
			}
			byID[t.ID] = len(out.Tickets)
			out.Tickets = append(out.Tickets, ticket.Detail{Ticket: t, Comments: []ticket.Comment{}})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		crows, err := tx.QueryContext(ctx, "SELECT id, ticket_id, author, body, created_at FROM comments ORDER BY id")
		if err != nil {
			return err
		}
		defer crows.Close()
		for crows.Next() {
			c, err := scanComment(crows)
			if err != nil {
				return err
			}
			d := &out.Tickets[byID[c.TicketID]]
			d.Comments = append(d.Comments, c)
		}
		if err := crows.Err(); err != nil {
			return err
		}
		erows, err := tx.QueryContext(ctx, "SELECT "+eventCols+" FROM events ORDER BY seq")
		if err != nil {
			return err
		}
		defer erows.Close()
		for erows.Next() {
			e, err := scanEvent(erows)
			if err != nil {
				return err
			}
			out.Events = append(out.Events, e)
			out.LastSeq = e.Seq
		}
		return erows.Err()
	})
	return out, err
}
