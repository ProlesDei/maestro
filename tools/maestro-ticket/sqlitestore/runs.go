package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

const runCols = "r.session, r.ticket_id, r.actor, r.cost_usd, r.input_tokens, r.output_tokens, r.cache_write_tokens, r.cache_read_tokens, r.created_at, r.updated_at"

// LogRun implements ticket.Store.
func (s *Store) LogRun(ctx context.Context, req ticket.LogRunRequest) (ticket.Run, error) {
	if err := req.Validate(); err != nil {
		return ticket.Run{}, err
	}
	var out ticket.Run
	err := s.write(ctx, func(tx *sql.Tx, now int64) error {
		var tid any
		if req.Ticket != 0 {
			var one int
			err := tx.QueryRowContext(ctx, "SELECT 1 FROM tickets WHERE id = ?", req.Ticket).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ticket.ErrNotFound, req.Ticket)
			}
			if err != nil {
				return err
			}
			tid = int64(req.Ticket)
		}
		// A session's total only grows, so a lower cost is an older reply
		// arriving late: keep what is there.
		_, err := tx.ExecContext(ctx, `
INSERT INTO runs (session, ticket_id, actor, cost_usd, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session) DO UPDATE SET
	ticket_id = COALESCE(excluded.ticket_id, runs.ticket_id),
	cost_usd = excluded.cost_usd,
	input_tokens = excluded.input_tokens,
	output_tokens = excluded.output_tokens,
	cache_write_tokens = excluded.cache_write_tokens,
	cache_read_tokens = excluded.cache_read_tokens,
	updated_at = excluded.updated_at
WHERE excluded.cost_usd >= runs.cost_usd`,
			req.Session, tid, req.Actor, req.CostUSD, req.InputTokens, req.OutputTokens,
			req.CacheWriteTokens, req.CacheReadTokens, now, now)
		if err != nil {
			return err
		}
		out, err = scanRun(tx.QueryRowContext(ctx, "SELECT "+runCols+" FROM runs r WHERE r.session = ?", req.Session))
		return err
	})
	return out, err
}

// Runs implements ticket.Store.
func (s *Store) Runs(ctx context.Context, f ticket.RunFilter) ([]ticket.RunRow, error) {
	out := []ticket.RunRow{}
	var since int64
	if !f.Since.IsZero() {
		since = f.Since.UnixMilli()
	}
	err := s.read(ctx, func(tx *sql.Tx, now int64) error {
		out = out[:0]
		rows, err := tx.QueryContext(ctx, `
SELECT `+runCols+`, COALESCE(t.goal, ''),
	CASE WHEN t.id IS NULL THEN ''
		WHEN EXISTS (SELECT 1 FROM events e WHERE e.ticket_id = t.id AND e.type = ?
			AND json_extract(e.data, '$.split_from') IS NOT NULL) THEN 'split'
		ELSE t.created_by END,
	COALESCE(t.tries, 0)
FROM runs r LEFT JOIN tickets t ON t.id = r.ticket_id
WHERE (? = 0 OR r.ticket_id = ?) AND r.created_at >= ?
ORDER BY r.created_at, r.session`, ticket.EventCreated, int64(f.Ticket), int64(f.Ticket), since)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r ticket.RunRow
			if r.Run, err = scanRun(rows, &r.Goal, &r.Origin, &r.Tries); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// scanRun reads runCols, then any extra columns into extra.
func scanRun(row scanner, extra ...any) (ticket.Run, error) {
	var r ticket.Run
	var tid sql.NullInt64
	var created, updated int64
	dest := append([]any{&r.Session, &tid, &r.Actor, &r.CostUSD, &r.InputTokens, &r.OutputTokens,
		&r.CacheWriteTokens, &r.CacheReadTokens, &created, &updated}, extra...)
	if err := row.Scan(dest...); err != nil {
		return ticket.Run{}, err
	}
	r.Ticket = ticket.ID(tid.Int64)
	r.CreatedAt, r.UpdatedAt = msTime(created), msTime(updated)
	return r, nil
}
