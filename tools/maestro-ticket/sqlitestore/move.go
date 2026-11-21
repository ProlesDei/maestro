package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// A board moved to MongoDB leaves this file as the way back, with a
// trigger on every table that refuses each insert, update and delete, so
// a process that still has the file open can't write a second board.

// movedMsg starts the error every moved trigger raises.
const movedMsg = "board moved to mongodb"

// movedTrigger prefixes the moved triggers' names.
const movedTrigger = "board_moved_"

// boardTables are the tables a board's rows live in, each before the ones
// that refer to it.
var boardTables = []string{"tickets", "deps", "areas", "comments", "events", "idempotency", "runs", "launches", "etas"}

// counted are the tables whose ids sqlite_sequence counts.
var counted = []string{"tickets", "comments", "events", "launches"}

// movedErr turns a moved trigger's abort into ErrInvalid that says what
// happened, so a write to the old board fails at once, not as an internal
// error.
func (s *Store) movedErr(err error) error {
	if err == nil || !strings.Contains(err.Error(), movedMsg) {
		return err
	}
	return fmt.Errorf("%w: %s: %s is the copy from before the move and takes no writes; store.uri missing? Put it back beside it with maestro-ticket store --uri-file -",
		ticket.ErrInvalid, movedMsg, s.path)
}

// Mover is a move's view of the store: one write transaction, which
// Move holds from its first read to its commit.
type Mover struct {
	s   *Store
	ctx context.Context
	tx  *sql.Tx
	now int64
}

// Move runs fn in one BEGIN IMMEDIATE transaction, which holds the write
// lock throughout, and commits only if fn returns nil. A move to MongoDB
// reads the board, copies it, checks the copy and blocks the file inside
// it, so no write lands between the read and the block.
func (s *Store) Move(ctx context.Context, fn func(m *Mover) error) error {
	return s.write(ctx, func(tx *sql.Tx, now int64) error {
		return fn(&Mover{s: s, ctx: ctx, tx: tx, now: now})
	})
}

// Moved reports whether the board moved to MongoDB: the file carries the
// moved triggers.
func (s *Store) Moved(ctx context.Context) (bool, error) {
	var out bool
	err := s.read(ctx, func(tx *sql.Tx, now int64) error {
		var err error
		out, err = moved(ctx, tx)
		return err
	})
	return out, err
}

// Moved reports whether the file carries the moved triggers.
func (m *Mover) Moved() (bool, error) { return moved(m.ctx, m.tx) }

func moved(ctx context.Context, q querier) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE ?", movedTrigger+"%").Scan(&n)
	return n > 0, err
}

// Block adds the moved triggers to every table.
func (m *Mover) Block() error {
	for _, t := range boardTables {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			_, err := m.tx.ExecContext(m.ctx, fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s%s_%s BEFORE %s ON %s
	BEGIN SELECT RAISE(ABORT, '%s: this file is the copy from before the move; use the MongoDB board store.uri names'); END`,
				movedTrigger, t, strings.ToLower(op), op, t, movedMsg))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Mover) unblock() error {
	for _, t := range boardTables {
		for _, op := range []string{"insert", "update", "delete"} {
			if _, err := m.tx.ExecContext(m.ctx, "DROP TRIGGER IF EXISTS "+movedTrigger+t+"_"+op); err != nil {
				return err
			}
		}
	}
	return nil
}

// Export reads the store as Export does, inside the move's transaction,
// so it sees what the move wrote.
func (m *Mover) Export() (ticket.Export, error) { return m.s.export(m.ctx, m.tx, m.now) }

// Board reads every table as it is.
func (m *Mover) Board() (ticket.Board, error) {
	b := ticket.Board{Tickets: []ticket.BoardTicket{}, Comments: []ticket.BoardComment{}, Events: []ticket.BoardEvent{},
		Idempotency: []ticket.BoardIdem{}, Runs: []ticket.BoardRun{}, Launches: []ticket.BoardLaunch{}, ETAs: []ticket.BoardETA{}}
	byID := map[int64]int{}
	err := m.each(`SELECT id, title, body, type, status, role, priority, created_by, owner, tries, lease_expiries,
		COALESCE(blocked_from, ''), parked, goal, tier, direct_qa, version, lease_holder, lease_token, lease_expires_at, created_at, updated_at
		FROM tickets ORDER BY id`, func(r *sql.Rows) error {
		var t ticket.BoardTicket
		var holder sql.NullString
		var expires sql.NullInt64
		err := r.Scan(&t.ID, &t.Title, &t.Body, &t.Type, &t.Status, &t.Role, &t.Priority, &t.CreatedBy, &t.Owner, &t.Tries,
			&t.LeaseExpiries, &t.BlockedFrom, &t.Parked, &t.Goal, &t.Tier, &t.DirectQA, &t.Version, &holder, &t.Token, &expires, &t.CreatedAt, &t.UpdatedAt)
		if holder.Valid {
			t.Lease = &ticket.BoardLease{Holder: holder.String, Expires: expires.Int64}
		}
		t.Deps, t.Areas = []int64{}, []string{}
		byID[t.ID] = len(b.Tickets)
		b.Tickets = append(b.Tickets, t)
		return err
	})
	if err != nil {
		return b, err
	}
	err = m.each("SELECT ticket_id, depends_on FROM deps ORDER BY ticket_id, depends_on", func(r *sql.Rows) error {
		var id, dep int64
		err := r.Scan(&id, &dep)
		t := &b.Tickets[byID[id]]
		t.Deps = append(t.Deps, dep)
		return err
	})
	if err != nil {
		return b, err
	}
	err = m.each("SELECT ticket_id, area FROM areas", func(r *sql.Rows) error {
		var id int64
		var area string
		err := r.Scan(&id, &area)
		t := &b.Tickets[byID[id]]
		t.Areas = append(t.Areas, area)
		return err
	})
	if err != nil {
		return b, err
	}
	for i := range b.Tickets {
		slices.Sort(b.Tickets[i].Areas)
	}
	err = m.each("SELECT id, ticket_id, author, body, created_at FROM comments ORDER BY id", func(r *sql.Rows) error {
		var c ticket.BoardComment
		err := r.Scan(&c.ID, &c.TicketID, &c.Author, &c.Body, &c.CreatedAt)
		b.Comments = append(b.Comments, c)
		return err
	})
	if err != nil {
		return b, err
	}
	err = m.each("SELECT seq, ticket_id, type, actor, data, created_at FROM events ORDER BY seq", func(r *sql.Rows) error {
		var e ticket.BoardEvent
		err := r.Scan(&e.Seq, &e.TicketID, &e.Type, &e.Actor, &e.Data, &e.CreatedAt)
		b.Events = append(b.Events, e)
		return err
	})
	if err != nil {
		return b, err
	}
	err = m.each("SELECT key, op, request_hash, result, created_at FROM idempotency", func(r *sql.Rows) error {
		var k ticket.BoardIdem
		err := r.Scan(&k.Key, &k.Op, &k.RequestHash, &k.Result, &k.CreatedAt)
		b.Idempotency = append(b.Idempotency, k)
		return err
	})
	if err != nil {
		return b, err
	}
	err = m.each(`SELECT session, COALESCE(ticket_id, 0), actor, cost_usd, input_tokens, output_tokens, cache_write_tokens,
		cache_read_tokens, created_at, updated_at FROM runs`, func(r *sql.Rows) error {
		var x ticket.BoardRun
		err := r.Scan(&x.Session, &x.TicketID, &x.Actor, &x.CostUSD, &x.InputTokens, &x.OutputTokens, &x.CacheWriteTokens,
			&x.CacheReadTokens, &x.CreatedAt, &x.UpdatedAt)
		b.Runs = append(b.Runs, x)
		return err
	})
	if err != nil {
		return b, err
	}
	err = m.each(`SELECT id, actor, COALESCE(ticket_id, 0), pid, stamp, started_at, COALESCE(ended_at, 0), outcome, session
		FROM launches ORDER BY id`, func(r *sql.Rows) error {
		var l ticket.BoardLaunch
		err := r.Scan(&l.ID, &l.Actor, &l.TicketID, &l.PID, &l.Stamp, &l.StartedAt, &l.EndedAt, &l.Outcome, &l.Session)
		b.Launches = append(b.Launches, l)
		return err
	})
	if err != nil {
		return b, err
	}
	err = m.each("SELECT goal, due_at, tickets, set_by, set_at, COALESCE(met_at, 0) FROM etas", func(r *sql.Rows) error {
		var e ticket.BoardETA
		var ids string
		if err := r.Scan(&e.Goal, &e.DueAt, &ids, &e.SetBy, &e.SetAt, &e.MetAt); err != nil {
			return err
		}
		e.Tickets = []int64{}
		if ids != "" {
			for _, s := range strings.Split(ids, ",") {
				n, err := strconv.ParseInt(s, 10, 64)
				if err != nil {
					return fmt.Errorf("ETA %s's tickets %q: %w", e.Goal, ids, err)
				}
				e.Tickets = append(e.Tickets, n)
			}
		}
		b.ETAs = append(b.ETAs, e)
		return nil
	})
	if err != nil {
		return b, err
	}
	// Go's order, which the other store sorts by too.
	slices.SortFunc(b.Idempotency, func(x, y ticket.BoardIdem) int { return strings.Compare(x.Key, y.Key) })
	slices.SortFunc(b.Runs, func(x, y ticket.BoardRun) int { return strings.Compare(x.Session, y.Session) })
	slices.SortFunc(b.ETAs, func(x, y ticket.BoardETA) int { return strings.Compare(x.Goal, y.Goal) })
	err = m.each("SELECT name, seq FROM sqlite_sequence", func(r *sql.Rows) error {
		var name string
		var seq int64
		err := r.Scan(&name, &seq)
		switch name {
		case "tickets":
			b.Counters.Tickets = seq
		case "comments":
			b.Counters.Comments = seq
		case "events":
			b.Counters.Events = seq
		case "launches":
			b.Counters.Launches = seq
		}
		return err
	})
	return b, err
}

func (m *Mover) each(query string, fn func(r *sql.Rows) error) error {
	rows, err := m.tx.QueryContext(m.ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Replace empties every table and writes b in, with the counters set to
// b's, and drops the moved triggers: the board moves back here.
func (m *Mover) Replace(b ticket.Board) error {
	if err := m.unblock(); err != nil {
		return err
	}
	// Events are append-only to every write but this one.
	if _, err := m.tx.ExecContext(m.ctx, "DROP TRIGGER IF EXISTS events_no_delete"); err != nil {
		return err
	}
	for _, t := range slices.Backward(boardTables) {
		if _, err := m.tx.ExecContext(m.ctx, "DELETE FROM "+t); err != nil {
			return err
		}
	}
	_, err := m.tx.ExecContext(m.ctx, `CREATE TRIGGER IF NOT EXISTS events_no_delete BEFORE DELETE ON events
	BEGIN SELECT RAISE(ABORT, 'events are append-only'); END`)
	if err != nil {
		return err
	}
	null := func(v int64) any {
		if v == 0 {
			return nil
		}
		return v
	}
	nullText := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	for _, t := range b.Tickets {
		var holder, expires any
		if t.Lease != nil {
			holder, expires = t.Lease.Holder, t.Lease.Expires
		}
		_, err := m.tx.ExecContext(m.ctx, `INSERT INTO tickets (id, title, body, type, status, role, priority, created_by, owner,
			tries, lease_expiries, blocked_from, parked, goal, tier, direct_qa, version, lease_holder, lease_token, lease_expires_at,
			created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.Title, t.Body, t.Type, t.Status, t.Role, t.Priority, t.CreatedBy, t.Owner, t.Tries, t.LeaseExpiries,
			nullText(t.BlockedFrom), t.Parked, t.Goal, t.Tier, t.DirectQA, t.Version, holder, t.Token, expires, t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return fmt.Errorf("write %s: %w", ticket.ID(t.ID), err)
		}
	}
	// Deps after every ticket: one may name a later one.
	for _, t := range b.Tickets {
		for _, d := range t.Deps {
			if _, err := m.tx.ExecContext(m.ctx, "INSERT INTO deps (ticket_id, depends_on) VALUES (?, ?)", t.ID, d); err != nil {
				return fmt.Errorf("write %s's deps: %w", ticket.ID(t.ID), err)
			}
		}
		for _, a := range t.Areas {
			if _, err := m.tx.ExecContext(m.ctx, "INSERT INTO areas (ticket_id, area) VALUES (?, ?)", t.ID, a); err != nil {
				return fmt.Errorf("write %s's areas: %w", ticket.ID(t.ID), err)
			}
		}
	}
	for _, c := range b.Comments {
		_, err := m.tx.ExecContext(m.ctx, "INSERT INTO comments (id, ticket_id, author, body, created_at) VALUES (?, ?, ?, ?, ?)",
			c.ID, c.TicketID, c.Author, c.Body, c.CreatedAt)
		if err != nil {
			return fmt.Errorf("write comment %d: %w", c.ID, err)
		}
	}
	for _, e := range b.Events {
		_, err := m.tx.ExecContext(m.ctx, "INSERT INTO events (seq, ticket_id, type, actor, data, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			e.Seq, e.TicketID, e.Type, e.Actor, e.Data, e.CreatedAt)
		if err != nil {
			return fmt.Errorf("write event %d: %w", e.Seq, err)
		}
	}
	for _, k := range b.Idempotency {
		_, err := m.tx.ExecContext(m.ctx, "INSERT INTO idempotency (key, op, request_hash, result, created_at) VALUES (?, ?, ?, ?, ?)",
			k.Key, k.Op, k.RequestHash, k.Result, k.CreatedAt)
		if err != nil {
			return fmt.Errorf("write idempotency key %q: %w", k.Key, err)
		}
	}
	for _, r := range b.Runs {
		_, err := m.tx.ExecContext(m.ctx, `INSERT INTO runs (session, ticket_id, actor, cost_usd, input_tokens, output_tokens,
			cache_write_tokens, cache_read_tokens, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Session, null(r.TicketID), r.Actor, r.CostUSD, r.InputTokens, r.OutputTokens, r.CacheWriteTokens, r.CacheReadTokens,
			r.CreatedAt, r.UpdatedAt)
		if err != nil {
			return fmt.Errorf("write run %s: %w", r.Session, err)
		}
	}
	// A launch's host goes: every launch here is this machine's.
	for _, l := range b.Launches {
		_, err := m.tx.ExecContext(m.ctx, `INSERT INTO launches (id, actor, ticket_id, pid, stamp, started_at, ended_at, outcome, session)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			l.ID, l.Actor, null(l.TicketID), l.PID, l.Stamp, l.StartedAt, null(l.EndedAt), l.Outcome, l.Session)
		if err != nil {
			return fmt.Errorf("write launch %d: %w", l.ID, err)
		}
	}
	for _, e := range b.ETAs {
		ids := make([]string, len(e.Tickets))
		for i, id := range e.Tickets {
			ids[i] = strconv.FormatInt(id, 10)
		}
		_, err := m.tx.ExecContext(m.ctx, "INSERT INTO etas (goal, due_at, tickets, set_by, set_at, met_at) VALUES (?, ?, ?, ?, ?, ?)",
			e.Goal, e.DueAt, strings.Join(ids, ","), e.SetBy, e.SetAt, null(e.MetAt))
		if err != nil {
			return fmt.Errorf("write ETA %s: %w", e.Goal, err)
		}
	}
	for _, name := range counted {
		seq := map[string]int64{"tickets": b.Counters.Tickets, "comments": b.Counters.Comments,
			"events": b.Counters.Events, "launches": b.Counters.Launches}[name]
		res, err := m.tx.ExecContext(m.ctx, "UPDATE sqlite_sequence SET seq = ? WHERE name = ?", seq, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 && seq > 0 {
			if _, err := m.tx.ExecContext(m.ctx, "INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)", name, seq); err != nil {
				return err
			}
		}
	}
	return nil
}
