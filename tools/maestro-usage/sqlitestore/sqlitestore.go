// Package sqlitestore is a usage.Store backed by one SQLite file in WAL mode,
// shared by every worktree and process of a repo.
package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"

	"github.com/ProlesDei/maestro/tools/maestro-usage/usage"
)

const (
	schemaVersion = 1
	busyTimeoutMS = 5000
	maxBusyRetry  = 8
)

const schema = `
CREATE TABLE IF NOT EXISTS usage (
	id            INTEGER PRIMARY KEY,
	recorded_at   INTEGER NOT NULL, -- unix milliseconds
	record_key    TEXT    NOT NULL UNIQUE,
	harness       TEXT    NOT NULL,
	session_id    TEXT,
	ticket        TEXT,
	workstream    TEXT,
	role          TEXT,
	bot           TEXT,
	model         TEXT,
	input_tokens  INTEGER NOT NULL DEFAULT 0,
	cached_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cost_usd      REAL,
	cost_source   TEXT,
	status        TEXT    NOT NULL,
	duration_ms   INTEGER
);
CREATE INDEX IF NOT EXISTS usage_ticket     ON usage(ticket, recorded_at);
CREATE INDEX IF NOT EXISTS usage_workstream ON usage(workstream, recorded_at);
CREATE INDEX IF NOT EXISTS usage_role       ON usage(role, recorded_at);
CREATE INDEX IF NOT EXISTS usage_time       ON usage(recorded_at);
`

// Store is a usage.Store backed by SQLite.
type Store struct {
	path string
	db   *sql.DB // transactions are BEGIN IMMEDIATE
}

var _ usage.Store = (*Store)(nil)

// Open opens (creating if needed) the store at path and migrates the schema.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" || strings.ContainsRune(path, '?') {
		return nil, fmt.Errorf("%w: usage db path %q", usage.ErrInvalid, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("create usage db dir: %w", err)
	}
	dsn := fmt.Sprintf("%s?_busy_timeout=%d&_journal_mode=WAL&_synchronous=NORMAL&_txlock=immediate", abs, busyTimeoutMS)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{path: abs, db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open usage db %s: %w", abs, err)
	}
	return s, nil
}

// Path returns the absolute path of the database file.
func (s *Store) Path() string { return s.path }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var v int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
			return err
		}
		if v == schemaVersion {
			return nil
		}
		if v > schemaVersion {
			return fmt.Errorf("schema version %d is newer than this maestro-usage (%d); upgrade maestro-usage", v, schemaVersion)
		}
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion))
		return err
	})
}

// Save inserts r, or replaces the row with the same key.
func (s *Store) Save(ctx context.Context, r usage.Record) error {
	if r.Key == "" {
		return fmt.Errorf("%w: record has no key", usage.ErrInvalid)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO usage (recorded_at, record_key, harness, session_id, ticket,
				workstream, role, bot, model, input_tokens, cached_tokens,
				output_tokens, cost_usd, cost_source, status, duration_ms)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(record_key) DO UPDATE SET
				recorded_at=excluded.recorded_at, harness=excluded.harness,
				session_id=excluded.session_id, ticket=excluded.ticket,
				workstream=excluded.workstream, role=excluded.role, bot=excluded.bot,
				model=excluded.model,
				input_tokens=excluded.input_tokens, cached_tokens=excluded.cached_tokens,
				output_tokens=excluded.output_tokens, cost_usd=excluded.cost_usd,
				cost_source=excluded.cost_source, status=excluded.status,
				duration_ms=excluded.duration_ms`,
			r.RecordedAt.UnixMilli(), r.Key, r.Harness, null(r.SessionID), null(r.Ticket),
			null(r.Workstream), null(r.Role), null(r.Bot), null(r.Model),
			r.InputTokens, r.CachedTokens, r.OutputTokens, r.CostUSD, null(r.CostSource),
			r.Status, r.DurationMS)
		return err
	})
}

// Spent sums cost and tokens over the records f selects.
func (s *Store) Spent(ctx context.Context, f usage.Filter) (usage.Totals, error) {
	q := `SELECT COALESCE(SUM(cost_usd),0), COALESCE(SUM(input_tokens+cached_tokens+output_tokens),0)
		FROM usage WHERE 1=1`
	var args []any
	if f.Field != "" {
		if !f.Field.Valid() {
			return usage.Totals{}, fmt.Errorf("%w: field %q", usage.ErrInvalid, f.Field)
		}
		q += " AND " + string(f.Field) + " = ?"
		args = append(args, f.Value)
	}
	if !f.Since.IsZero() {
		q += " AND recorded_at >= ?"
		args = append(args, f.Since.UnixMilli())
	}
	var t usage.Totals
	err := retryBusy(ctx, func() error {
		return s.db.QueryRowContext(ctx, q, args...).Scan(&t.USD, &t.Tokens)
	})
	return t, err
}

// RecentStatuses returns the statuses of role's newest n records, newest first.
func (s *Store) RecentStatuses(ctx context.Context, role string, n int) ([]string, error) {
	var out []string
	err := retryBusy(ctx, func() error {
		out = out[:0]
		rows, err := s.db.QueryContext(ctx,
			"SELECT status FROM usage WHERE role = ? ORDER BY recorded_at DESC, id DESC LIMIT ?", role, n)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var st string
			if err := rows.Scan(&st); err != nil {
				return err
			}
			out = append(out, st)
		}
		return rows.Err()
	})
	return out, err
}

// Report groups records by a field, most spent first.
func (s *Store) Report(ctx context.Context, by usage.Field, since time.Time) ([]usage.ReportRow, error) {
	if !by.Valid() {
		return nil, fmt.Errorf("%w: group by %q", usage.ErrInvalid, by)
	}
	col := string(by)
	q := `SELECT COALESCE(` + col + `, '(none)'), COUNT(*), COALESCE(SUM(cost_usd),0),
			SUM(input_tokens), SUM(cached_tokens), SUM(output_tokens),
			SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END),
			SUM(CASE WHEN cost_usd IS NULL THEN 1 ELSE 0 END)
		FROM usage WHERE recorded_at >= ?
		GROUP BY ` + col + ` ORDER BY 3 DESC, 1`
	var sinceMS int64
	if !since.IsZero() {
		sinceMS = since.UnixMilli()
	}
	var out []usage.ReportRow
	err := retryBusy(ctx, func() error {
		out = out[:0]
		rows, err := s.db.QueryContext(ctx, q, sinceMS)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r usage.ReportRow
			if err := rows.Scan(&r.Group, &r.Sessions, &r.USD, &r.Input, &r.Cached,
				&r.Output, &r.Failures, &r.Unpriced); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// write runs fn in a BEGIN IMMEDIATE transaction, retrying briefly if the
// write lock stays busy past busy_timeout.
func (s *Store) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return retryBusy(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
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
