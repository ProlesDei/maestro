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

	"modernc.org/sqlite"

	"github.com/ProlesDei/maestro/tools/at-ticket/ticket"
)

const (
	schemaVersion = 1
	busyTimeoutMS = 5000
	maxBusyRetry  = 5
)

const schema = `
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

// Options tune a Store. The zero value is fine for production.
type Options struct {
	// Now returns the current time. Tests inject a fake clock.
	Now func() time.Time
}

// Store is a ticket.Store backed by one SQLite file.
type Store struct {
	path string
	w    *sql.DB // writer pool: one connection, BEGIN IMMEDIATE
	r    *sql.DB // reader pool: deferred transactions
	now  func() time.Time
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
	if s.now == nil {
		s.now = time.Now
	}
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
		return fmt.Errorf("schema version %d is newer than this at-ticket (%d); upgrade at-ticket", v, schemaVersion)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
			return err
		}
		if v == schemaVersion {
			return nil
		}
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion))
		return err
	})
}

// write runs fn in a BEGIN IMMEDIATE transaction, retrying briefly if the
// write lock stays busy past busy_timeout.
func (s *Store) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return retryBusy(ctx, func() error {
		tx, err := s.w.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := fn(tx); err != nil {
			tx.Rollback()
			return err
		}
		return tx.Commit()
	})
}

func (s *Store) read(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return retryBusy(ctx, func() error {
		tx, err := s.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return fn(tx)
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
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case 5, 6: // SQLITE_BUSY, SQLITE_LOCKED
			return true
		}
	}
	return false
}

func (s *Store) nowMS() int64 { return s.now().UnixMilli() }

func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

const ticketCols = `id, title, body, status, role, priority, created_by, version,
	lease_holder, lease_expires_at, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

func (s *Store) scanTicket(row scanner, nowMS int64) (ticket.Ticket, error) {
	var (
		t                ticket.Ticket
		status           string
		holder           sql.NullString
		expires          sql.NullInt64
		created, updated int64
	)
	err := row.Scan(&t.ID, &t.Title, &t.Body, &status, &t.Role, &t.Priority, &t.CreatedBy,
		&t.Version, &holder, &expires, &created, &updated)
	if err != nil {
		return t, err
	}
	t.Status = ticket.Status(status)
	t.CreatedAt, t.UpdatedAt = msTime(created), msTime(updated)
	if holder.Valid {
		t.Claim = &ticket.Claim{
			Holder:    holder.String,
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

// idempotent runs do once per key. A retry with the same op and request
// replays the stored result; a different request under the same key fails.
func idempotent[T any](ctx context.Context, tx *sql.Tx, key, op string, req any, nowMS int64, do func() (T, error)) (T, error) {
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
	err := s.write(ctx, func(tx *sql.Tx) error {
		now := s.nowMS()
		var err error
		out, err = idempotent(ctx, tx, req.IdempotencyKey, "create", req, now, func() (ticket.Ticket, error) {
			res, err := tx.ExecContext(ctx, `INSERT INTO tickets
				(title, body, status, role, priority, created_by, version, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)`,
				req.Title, req.Body, req.Status, req.Role, *req.Priority, req.Actor, now, now)
			if err != nil {
				return ticket.Ticket{}, err
			}
			n, err := res.LastInsertId()
			if err != nil {
				return ticket.Ticket{}, err
			}
			id := ticket.ID(n)
			err = appendEvent(ctx, tx, id, ticket.EventCreated, req.Actor, map[string]any{
				"title": req.Title, "status": req.Status, "role": req.Role, "priority": *req.Priority,
			}, now)
			if err != nil {
				return ticket.Ticket{}, err
			}
			return s.getTicket(ctx, tx, id, now)
		})
		return err
	})
	return out, err
}

// Get implements ticket.Store.
func (s *Store) Get(ctx context.Context, id ticket.ID) (ticket.Detail, error) {
	var d ticket.Detail
	err := s.read(ctx, func(tx *sql.Tx) error {
		t, err := s.getTicket(ctx, tx, id, s.nowMS())
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
			var c ticket.Comment
			var created int64
			if err := rows.Scan(&c.ID, &c.TicketID, &c.Author, &c.Body, &created); err != nil {
				return err
			}
			c.CreatedAt = msTime(created)
			d.Comments = append(d.Comments, c)
		}
		return rows.Err()
	})
	return d, err
}

// List implements ticket.Store. Results are ordered by priority, then id.
func (s *Store) List(ctx context.Context, f ticket.ListFilter) ([]ticket.Ticket, error) {
	out := []ticket.Ticket{}
	err := s.read(ctx, func(tx *sql.Tx) error {
		now := s.nowMS()
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
	err := s.write(ctx, func(tx *sql.Tx) error {
		now := s.nowMS()
		var err error
		out, err = idempotent(ctx, tx, req.IdempotencyKey, "claim", req, now, func() (ticket.Ticket, error) {
			t, err := s.pickClaimable(ctx, tx, req, now)
			if err != nil {
				return t, err
			}
			return s.applyClaim(ctx, tx, t, req, now)
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
		if c := t.LiveClaim(); c != nil && c.Holder != req.Holder {
			return t, fmt.Errorf("%w: %s is held by %s until %s", ticket.ErrClaimed, t.ID, c.Holder, c.ExpiresAt.Format(time.RFC3339))
		}
		return t, nil
	}
	where := []string{"status IN (" + placeholders(len(req.Statuses)) + ")", "(lease_holder IS NULL OR lease_expires_at <= ?)"}
	var args []any
	for _, st := range req.Statuses {
		args = append(args, string(st))
	}
	args = append(args, now)
	if req.Role != "" {
		where = append(where, "role = ?")
		args = append(args, req.Role)
	}
	q := "SELECT " + ticketCols + " FROM tickets WHERE " + strings.Join(where, " AND ") + " ORDER BY priority, id LIMIT 1"
	t, err := s.scanTicket(tx.QueryRowContext(ctx, q, args...), now)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ticket.ErrNoWork
	}
	return t, err
}

func (s *Store) applyClaim(ctx context.Context, tx *sql.Tx, t ticket.Ticket, req ticket.ClaimRequest, now int64) (ticket.Ticket, error) {
	expires := now + req.TTL.Milliseconds()
	if c := t.LiveClaim(); c != nil && c.Holder == req.Holder {
		// Re-claiming your own live lease just refreshes it.
		if _, err := tx.ExecContext(ctx, "UPDATE tickets SET lease_expires_at = ? WHERE id = ?", expires, t.ID); err != nil {
			return t, err
		}
		return s.getTicket(ctx, tx, t.ID, now)
	}
	if t.Claim != nil {
		err := appendEvent(ctx, tx, t.ID, ticket.EventLeaseExpired, req.Actor, map[string]any{
			"holder": t.Claim.Holder, "expired_at": t.Claim.ExpiresAt,
		}, now)
		if err != nil {
			return t, err
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE tickets
		SET lease_holder = ?, lease_expires_at = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`, req.Holder, expires, now, t.ID, t.Version)
	if err != nil {
		return t, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return t, fmt.Errorf("%w: %s changed during claim", ticket.ErrVersionConflict, t.ID)
	}
	err = appendEvent(ctx, tx, t.ID, ticket.EventClaimed, req.Actor, map[string]any{
		"holder": req.Holder, "expires_at": msTime(expires), "status": t.Status,
	}, now)
	if err != nil {
		return t, err
	}
	return s.getTicket(ctx, tx, t.ID, now)
}

func containsStatus(list []ticket.Status, s ticket.Status) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Heartbeat implements ticket.Store. It fails with ErrLeaseLost if the lease
// expired or belongs to someone else; the session must stop work and re-claim.
func (s *Store) Heartbeat(ctx context.Context, req ticket.HeartbeatRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *sql.Tx) error {
		now := s.nowMS()
		t, err := s.getTicket(ctx, tx, req.ID, now)
		if err != nil {
			return err
		}
		if c := t.LiveClaim(); c == nil || c.Holder != req.Holder {
			return fmt.Errorf("%w: %s is not held by %s", ticket.ErrLeaseLost, t.ID, req.Holder)
		}
		_, err = tx.ExecContext(ctx, "UPDATE tickets SET lease_expires_at = ? WHERE id = ?",
			now+req.TTL.Milliseconds(), t.ID)
		if err != nil {
			return err
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
	err := s.write(ctx, func(tx *sql.Tx) error {
		now := s.nowMS()
		var err error
		out, err = idempotent(ctx, tx, req.IdempotencyKey, "release", req, now, func() (ticket.Ticket, error) {
			t, err := s.getTicket(ctx, tx, req.ID, now)
			if err != nil {
				return t, err
			}
			switch {
			case t.Claim == nil:
				return t, nil
			case t.Claim.Holder != req.Holder:
				if !t.Claim.Expired {
					return t, fmt.Errorf("%w: %s is held by %s", ticket.ErrClaimed, t.ID, t.Claim.Holder)
				}
				return t, nil // someone else's expired lease: nothing of ours to release
			}
			if err := s.clearLease(ctx, tx, t, now); err != nil {
				return t, err
			}
			if err := appendEvent(ctx, tx, t.ID, ticket.EventReleased, req.Actor, map[string]any{"holder": req.Holder}, now); err != nil {
				return t, err
			}
			return s.getTicket(ctx, tx, t.ID, now)
		})
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
	err := s.write(ctx, func(tx *sql.Tx) error {
		now := s.nowMS()
		var err error
		out, err = idempotent(ctx, tx, req.IdempotencyKey, "transition", req, now, func() (ticket.Ticket, error) {
			return s.applyTransition(ctx, tx, req, now)
		})
		return err
	})
	return out, err
}

func (s *Store) applyTransition(ctx context.Context, tx *sql.Tx, req ticket.TransitionRequest, now int64) (ticket.Ticket, error) {
	t, err := s.getTicket(ctx, tx, req.ID, now)
	if err != nil {
		return t, err
	}
	if req.From != "" && t.Status != req.From {
		return t, fmt.Errorf("%w: %s is %s, not %s", ticket.ErrStatusConflict, t.ID, t.Status, req.From)
	}
	if req.ExpectVersion != 0 && t.Version != req.ExpectVersion {
		return t, fmt.Errorf("%w: %s is at version %d, not %d", ticket.ErrVersionConflict, t.ID, t.Version, req.ExpectVersion)
	}
	if c := t.LiveClaim(); c != nil && c.Holder != req.Holder {
		return t, fmt.Errorf("%w: %s is held by %s; pass --holder to act on your own claim", ticket.ErrClaimed, t.ID, c.Holder)
	}
	if !ticket.CanTransition(t.Status, req.To) {
		return t, fmt.Errorf("%w: %s -> %s (allowed: %v)", ticket.ErrInvalidTransition, t.Status, req.To, ticket.NextStatuses(t.Status))
	}
	role := t.Role
	if req.Role != "" {
		role = req.Role
	}
	release := t.Claim != nil && (req.Release || req.To.Terminal())
	q := "UPDATE tickets SET status = ?, role = ?, version = version + 1, updated_at = ?"
	if release {
		q += ", lease_holder = NULL, lease_expires_at = NULL"
	}
	res, err := tx.ExecContext(ctx, q+" WHERE id = ? AND version = ?", req.To, role, now, t.ID, t.Version)
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
	err := s.write(ctx, func(tx *sql.Tx) error {
		now := s.nowMS()
		var err error
		out, err = idempotent(ctx, tx, req.IdempotencyKey, "comment", req, now, func() (ticket.Comment, error) {
			if _, err := s.getTicket(ctx, tx, req.ID, now); err != nil {
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
		})
		return err
	})
	return out, err
}

// Events implements ticket.Store.
func (s *Store) Events(ctx context.Context, f ticket.EventFilter) ([]ticket.Event, error) {
	out := []ticket.Event{}
	err := s.read(ctx, func(tx *sql.Tx) error {
		q := "SELECT seq, ticket_id, type, actor, data, created_at FROM events WHERE seq > ?"
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
			var e ticket.Event
			var data string
			var created int64
			if err := rows.Scan(&e.Seq, &e.TicketID, &e.Type, &e.Actor, &data, &created); err != nil {
				return err
			}
			e.Data = json.RawMessage(data)
			e.CreatedAt = msTime(created)
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// LastSeq implements ticket.Store.
func (s *Store) LastSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := s.read(ctx, func(tx *sql.Tx) error {
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
	add("foreign_key_check", fkViolations == 0, "%d violations", fkViolations)

	now := s.nowMS()
	var live, expired, total int
	err = conn.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(lease_holder IS NOT NULL AND lease_expires_at > ?), 0),
		COALESCE(SUM(lease_holder IS NOT NULL AND lease_expires_at <= ?), 0)
		FROM tickets`, now, now).Scan(&total, &live, &expired)
	if err != nil {
		return rep, err
	}
	add("leases", true, "%d tickets; %d live claims, %d expired (claimable)", total, live, expired)

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
