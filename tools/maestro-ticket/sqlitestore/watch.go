package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

const launchCols = "id, actor, ticket_id, pid, stamp, started_at, ended_at, outcome, session"

// StartLaunch implements ticket.Store. A ticket id that names no ticket
// fails with ErrNotFound, as LogRun does, so the caller can record the run
// with none.
func (s *Store) StartLaunch(ctx context.Context, req ticket.StartLaunchRequest) (ticket.Launch, error) {
	if err := req.Validate(); err != nil {
		return ticket.Launch{}, err
	}
	var out ticket.Launch
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
		res, err := tx.ExecContext(ctx, "INSERT INTO launches (actor, ticket_id, pid, stamp, started_at) VALUES (?, ?, ?, ?, ?)",
			strings.TrimSpace(req.Actor), tid, req.PID, req.Stamp, now)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		out, err = scanLaunch(tx.QueryRowContext(ctx, "SELECT "+launchCols+" FROM launches WHERE id = ?", id))
		return err
	})
	return out, err
}

// EndLaunch implements ticket.Store.
func (s *Store) EndLaunch(ctx context.Context, req ticket.EndLaunchRequest) (bool, error) {
	if err := req.Validate(); err != nil {
		return false, err
	}
	var ended bool
	err := s.write(ctx, func(tx *sql.Tx, now int64) error {
		res, err := tx.ExecContext(ctx, "UPDATE launches SET ended_at = ?, outcome = ?, session = ? WHERE id = ? AND ended_at IS NULL",
			now, req.Outcome, req.Session, req.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		ended = n == 1
		return err
	})
	return ended, err
}

// OpenLaunches implements ticket.Store.
func (s *Store) OpenLaunches(ctx context.Context) ([]ticket.Launch, error) {
	out := []ticket.Launch{}
	err := s.read(ctx, func(tx *sql.Tx, now int64) error {
		out = out[:0]
		rows, err := tx.QueryContext(ctx, "SELECT "+launchCols+" FROM launches WHERE ended_at IS NULL ORDER BY started_at, id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			l, err := scanLaunch(rows)
			if err != nil {
				return err
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	return out, err
}

func scanLaunch(row scanner) (ticket.Launch, error) {
	var l ticket.Launch
	var tid, ended sql.NullInt64
	var started int64
	if err := row.Scan(&l.ID, &l.Actor, &tid, &l.PID, &l.Stamp, &started, &ended, &l.Outcome, &l.Session); err != nil {
		return ticket.Launch{}, err
	}
	l.Ticket = ticket.ID(tid.Int64)
	l.StartedAt = msTime(started)
	if ended.Valid {
		l.EndedAt = msTime(ended.Int64)
	}
	return l, nil
}

// SetETA implements ticket.Store.
func (s *Store) SetETA(ctx context.Context, req ticket.SetETARequest) (ticket.ETA, error) {
	if err := req.Validate(); err != nil {
		return ticket.ETA{}, err
	}
	var out ticket.ETA
	err := s.write(ctx, func(tx *sql.Tx, now int64) error {
		// A goal no ticket carries, such as a misspelt slug, would read as
		// met for ever and never alert.
		var one int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM tickets WHERE goal = ? LIMIT 1", req.Goal).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: no ticket has goal %s", ticket.ErrNotFound, req.Goal)
		}
		if err != nil {
			return err
		}
		ids := make([]string, len(req.Tickets))
		for i, id := range req.Tickets {
			var one int
			err := tx.QueryRowContext(ctx, "SELECT 1 FROM tickets WHERE id = ?", id).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ticket.ErrNotFound, id)
			}
			if err != nil {
				return err
			}
			ids[i] = fmt.Sprint(int64(id))
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO etas (goal, due_at, tickets, set_by, set_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(goal) DO UPDATE SET due_at = excluded.due_at, tickets = excluded.tickets,
				set_by = excluded.set_by, set_at = excluded.set_at, met_at = NULL`,
			req.Goal, req.Due.UnixMilli(), strings.Join(ids, ","), req.Actor, now)
		if err != nil {
			return err
		}
		out, err = scanETA(tx.QueryRowContext(ctx, "SELECT goal, due_at, tickets, set_by, set_at, met_at FROM etas WHERE goal = ?", req.Goal))
		return err
	})
	return out, err
}

// ClearETA implements ticket.Store. Clearing a goal with no ETA fails with
// ErrNotFound.
func (s *Store) ClearETA(ctx context.Context, goal string) error {
	return s.write(ctx, func(tx *sql.Tx, now int64) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM etas WHERE goal = ?", strings.TrimSpace(goal))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: no ETA for goal %q", ticket.ErrNotFound, goal)
		}
		return nil
	})
}

// MarkETAMet implements ticket.Store. The due and set times name the ETA a
// look judged, so a replacement set meanwhile, even one due at the same
// time, is left alone.
func (s *Store) MarkETAMet(ctx context.Context, goal string, due, setAt time.Time) error {
	return s.write(ctx, func(tx *sql.Tx, now int64) error {
		_, err := tx.ExecContext(ctx, "UPDATE etas SET met_at = ? WHERE goal = ? AND due_at = ? AND set_at = ? AND met_at IS NULL",
			now, goal, due.UnixMilli(), setAt.UnixMilli())
		return err
	})
}

// ETAs implements ticket.Store.
func (s *Store) ETAs(ctx context.Context) ([]ticket.ETA, error) {
	out := []ticket.ETA{}
	err := s.read(ctx, func(tx *sql.Tx, now int64) error {
		out = out[:0]
		var err error
		out, err = queryETAs(ctx, tx, out)
		return err
	})
	return out, err
}

func queryETAs(ctx context.Context, q querier, out []ticket.ETA) ([]ticket.ETA, error) {
	rows, err := q.QueryContext(ctx, "SELECT goal, due_at, tickets, set_by, set_at, met_at FROM etas ORDER BY due_at, goal")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanETA(rows)
		if err != nil {
			return out, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanETA(row scanner) (ticket.ETA, error) {
	var e ticket.ETA
	var due, set int64
	var met sql.NullInt64
	var ids string
	if err := row.Scan(&e.Goal, &due, &ids, &e.SetBy, &set, &met); err != nil {
		return ticket.ETA{}, err
	}
	e.Due, e.SetAt = msTime(due), msTime(set)
	if met.Valid {
		e.MetAt = msTime(met.Int64)
	}
	e.Tickets = []ticket.ID{}
	if ids != "" {
		var err error
		if e.Tickets, err = ticket.ParseIDs(ids); err != nil {
			return ticket.ETA{}, err
		}
	}
	return e, nil
}
