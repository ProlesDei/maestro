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

// Runs implements ticket.Store. A run counts toward the ticket that
// carries its work now, as ticket.RunRow says.
func (s *Store) Runs(ctx context.Context, f ticket.RunFilter) ([]ticket.RunRow, error) {
	out := []ticket.RunRow{}
	var since int64
	if !f.Since.IsZero() {
		since = f.Since.UnixMilli()
	}
	err := s.read(ctx, func(tx *sql.Tx, now int64) error {
		out = out[:0]
		parents, err := splitParents(ctx, tx)
		if err != nil {
			return err
		}
		type info struct {
			goal, createdBy string
			tries           int
			cancelled       bool
		}
		tickets := map[ticket.ID]info{}
		rows, err := tx.QueryContext(ctx, "SELECT id, goal, created_by, tries, status FROM tickets")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var t info
			var status string
			if err := rows.Scan(&id, &t.goal, &t.createdBy, &t.tries, &status); err != nil {
				return err
			}
			t.cancelled = ticket.Status(status) == ticket.StatusCancelled
			tickets[ticket.ID(id)] = t
		}
		if err := rows.Err(); err != nil {
			return err
		}
		kids := map[ticket.ID][]ticket.ID{}
		for kid, p := range parents {
			kids[p] = append(kids[p], kid)
		}
		carry := func(id ticket.ID) ticket.ID {
			for seen := map[ticket.ID]bool{}; !seen[id]; {
				seen[id] = true
				if k := kids[id]; len(k) == 1 && tickets[id].cancelled {
					id = k[0]
					continue
				}
				break
			}
			return id
		}
		rows, err = tx.QueryContext(ctx, "SELECT "+runCols+" FROM runs r WHERE r.created_at >= ? ORDER BY r.created_at, r.session", since)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r ticket.RunRow
			if r.Run, err = scanRun(rows); err != nil {
				return err
			}
			if r.Ticket != 0 {
				if to := carry(r.Ticket); to != r.Ticket {
					r.CarriedFrom, r.Ticket = r.Ticket, to
				}
			}
			if f.Ticket != 0 && r.Ticket != f.Ticket && r.CarriedFrom != f.Ticket {
				continue
			}
			if r.Ticket != 0 {
				t := tickets[r.Ticket]
				r.Goal, r.Origin, r.Tries = t.goal, t.createdBy, t.tries
				if _, ok := parents[r.Ticket]; ok {
					r.Origin = "split"
				}
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// splitParents maps each ticket made or linked with --split-from to the
// ticket it splits or rewrites.
func splitParents(ctx context.Context, tx *sql.Tx) (map[ticket.ID]ticket.ID, error) {
	rows, err := tx.QueryContext(ctx, `SELECT ticket_id, json_extract(data, '$.split_from') FROM events
WHERE type IN (?, ?) AND json_extract(data, '$.split_from') IS NOT NULL`, ticket.EventCreated, ticket.EventUpdated)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[ticket.ID]ticket.ID{}
	for rows.Next() {
		var kid int64
		var from string
		if err := rows.Scan(&kid, &from); err != nil {
			return nil, err
		}
		p, err := ticket.ParseID(from)
		if err != nil {
			return nil, fmt.Errorf("T-%d's split_from: %w", kid, err)
		}
		out[ticket.ID(kid)] = p
	}
	return out, rows.Err()
}

// linkSplit checks that t may be linked to from, the cancelled ticket it
// replaces, and returns t's tries with from's added.
func linkSplit(ctx context.Context, tx *sql.Tx, t ticket.Ticket, from ticket.ID) (int, error) {
	var status string
	var tries int
	err := tx.QueryRowContext(ctx, "SELECT status, tries FROM tickets WHERE id = ?", from).Scan(&status, &tries)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: split from %s", ticket.ErrNotFound, from)
	}
	if err != nil {
		return 0, err
	}
	if ticket.Status(status) != ticket.StatusCancelled {
		return 0, fmt.Errorf("%w: %s is %s; link only a cancelled ticket to the one that replaced it (to split an open ticket, create the new ones with --split-from)",
			ticket.ErrInvalid, from, status)
	}
	parents, err := splitParents(ctx, tx)
	if err != nil {
		return 0, err
	}
	if p, ok := parents[t.ID]; ok {
		return 0, fmt.Errorf("%w: %s is already split from %s", ticket.ErrInvalid, t.ID, p)
	}
	for p, seen := from, map[ticket.ID]bool{}; p != 0 && !seen[p]; p = parents[p] {
		seen[p] = true
		if p == t.ID {
			return 0, fmt.Errorf("%w: %s comes from %s, so %s can't be split from it", ticket.ErrInvalid, from, t.ID, t.ID)
		}
	}
	return t.Tries + tries, nil
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
