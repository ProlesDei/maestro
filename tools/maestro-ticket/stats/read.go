package stats

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
)

// What the views read: rows as names, never ids, and each run's own
// spend, worked out from the session totals as prune works it out.

// Fact is one run's row as a view reads it. An enum field holds its name,
// or "" for none.
type Fact struct {
	Run       string     `json:"run"`
	Ticket    int64      `json:"ticket,omitempty"`
	ResumeOf  string     `json:"resume_of,omitempty"`
	AgainOf   string     `json:"again_of,omitempty"`
	Job       string     `json:"job,omitempty"`
	Model     string     `json:"model,omitempty"`
	Effort    string     `json:"effort,omitempty"`
	Lean      bool       `json:"lean"`
	Why       string     `json:"why,omitempty"`
	Tier      string     `json:"tier,omitempty"`
	Tries     *int64     `json:"tries,omitempty"`
	Nudge     string     `json:"nudge,omitempty"`
	Outcome   string     `json:"outcome,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	Verdict   string     `json:"verdict,omitempty"`
	Stream    string     `json:"stream,omitempty"`
	StartedAt time.Time  `json:"started_at,omitzero"`
	EndedAt   time.Time  `json:"ended_at,omitzero"`
	WallMs    *int64     `json:"wall_ms,omitempty"`
	Turns     *int64     `json:"turns,omitempty"`
	ToolCalls *int64     `json:"tool_calls,omitempty"`
	ToolFails *int64     `json:"tool_fails,omitempty"`
	OpenCode  string     `json:"open_code,omitempty"`
	OpenMs    *int64     `json:"open_ms,omitempty"`
	Tail      []TailCall `json:"tail,omitempty"`
	Spend     []Spend    `json:"spend,omitempty"`
	Findings  []Finding  `json:"findings,omitempty"`
}

// Spend is one run's own use of one model: its session's total less the
// run before it. USD is nil where it can't be known (see spendCTE).
type Spend struct {
	Model  string   `json:"model"`
	USD    *float64 `json:"usd"`
	Tokens *int64   `json:"tokens"`
}

// Filter picks the runs a view reads. Zero values pick all.
type Filter struct {
	Run    string
	Ticket int64
	Since  time.Time // started at or after
}

// picked is the runs a Filter picks, as ?1 run, ?2 ticket, ?3 since.
const picked = `SELECT run FROM run_facts WHERE (?1 = '' OR run = ?1) AND (?2 = 0 OR ticket = ?2) AND (?3 = 0 OR started_at >= ?3)`

func (f Filter) args() []any {
	var since int64
	if !f.Since.IsZero() {
		since = f.Since.UnixMilli()
	}
	return []any{f.Run, f.Ticket, since}
}

// codeNames reads every code, by id.
func codeNames(ctx context.Context, tx *sql.Tx) (map[int64]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, name FROM codes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[int64]string{}
	for rows.Next() {
		var id int64
		var n string
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		names[id] = n
	}
	return names, rows.Err()
}

// numOf is n, or nil for NULL.
func numOf(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// nameOf is the name of code id, or "" for NULL.
func nameOf(names map[int64]string, id sql.NullInt64) string {
	if !id.Valid {
		return ""
	}
	return names[id.Int64]
}

// parseTail reads run_facts.tail: code ids, ! after a failed call.
func parseTail(text string, names map[int64]string) []TailCall {
	var out []TailCall
	for _, w := range strings.Fields(text) {
		fail := strings.HasSuffix(w, "!")
		if id, err := strconv.ParseInt(strings.TrimSuffix(w, "!"), 10, 64); err == nil {
			out = append(out, TailCall{Code: names[id], Fail: fail})
		}
	}
	return out
}

// Read returns the runs f picks, oldest first, with their spend and
// findings. It refuses a stats.db at another schema: ErrNewer or
// ErrNoSchema.
func Read(ctx context.Context, db *sql.DB, f Filter) ([]Fact, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := checkVersion(ctx, tx); err != nil {
		return nil, err
	}
	names, err := codeNames(ctx, tx)
	if err != nil {
		return nil, err
	}
	at := func(n sql.NullInt64) time.Time {
		if !n.Valid {
			return time.Time{}
		}
		return time.UnixMilli(n.Int64)
	}
	rows, err := tx.QueryContext(ctx, `SELECT run, resume_of, again_of, ticket, job, model, effort, why, tier, nudge,
		outcome, reason, verdict, stream, open_code,
		lean, started_at, ended_at, wall_ms, turns, tries, open_ms, tool_calls, tool_fails, tail
		FROM run_facts WHERE run IN (`+picked+`) ORDER BY started_at, run`, f.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		var r Fact
		var resumeOf, againOf, tail sql.NullString
		var ticket, lean, started, ended, wall, turns, tries, openMs, calls, fails sql.NullInt64
		var e [11]sql.NullInt64
		if err := rows.Scan(&r.Run, &resumeOf, &againOf, &ticket, &e[0], &e[1], &e[2], &e[3], &e[4], &e[5],
			&e[6], &e[7], &e[8], &e[9], &e[10],
			&lean, &started, &ended, &wall, &turns, &tries, &openMs, &calls, &fails, &tail); err != nil {
			return nil, err
		}
		r.ResumeOf, r.AgainOf, r.Ticket = resumeOf.String, againOf.String, ticket.Int64
		for i, p := range []*string{&r.Job, &r.Model, &r.Effort, &r.Why, &r.Tier, &r.Nudge,
			&r.Outcome, &r.Reason, &r.Verdict, &r.Stream, &r.OpenCode} {
			*p = nameOf(names, e[i])
		}
		r.Lean = lean.Int64 == 1
		r.StartedAt, r.EndedAt = at(started), at(ended)
		r.WallMs, r.Turns, r.Tries, r.OpenMs = numOf(wall), numOf(turns), numOf(tries), numOf(openMs)
		r.ToolCalls, r.ToolFails = numOf(calls), numOf(fails)
		r.Tail = parseTail(tail.String, names)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if len(out) == 0 {
		return nil, tx.Commit()
	}
	spend, err := readSpend(ctx, tx, f, names)
	if err != nil {
		return nil, err
	}
	finds, err := readFindings(ctx, tx, f, names)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Spend, out[i].Findings = spend[out[i].Run], finds[out[i].Run]
	}
	return out, tx.Commit()
}

// readSpend reads each run's own spend per model, of the runs f picks,
// by run.
func readSpend(ctx context.Context, tx *sql.Tx, f Filter, names map[int64]string) (map[string][]Spend, error) {
	rows, err := tx.QueryContext(ctx, `WITH `+spendCTE(`f.run IN (`+picked+`)
				OR f.session IN (SELECT session FROM run_facts WHERE run IN (`+picked+`))`)+`
		SELECT run, model, cost, tok FROM d WHERE run IN (`+picked+`) ORDER BY run, model`, f.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Spend{}
	for rows.Next() {
		var run string
		var model, tok sql.NullInt64
		var cost sql.NullFloat64
		if err := rows.Scan(&run, &model, &cost, &tok); err != nil {
			return nil, err
		}
		s := Spend{Model: nameOf(names, model), Tokens: numOf(tok)}
		if cost.Valid {
			v := cost.Float64
			s.USD = &v
		}
		out[run] = append(out[run], s)
	}
	return out, rows.Err()
}

// readFindings reads the finding rows of the runs f picks, by run.
func readFindings(ctx context.Context, tx *sql.Tx, f Filter, names map[int64]string) (map[string][]Finding, error) {
	rows, err := tx.QueryContext(ctx, `SELECT run, fid, kind, pass, sev, action, prior, n FROM findings
		WHERE run IN (`+picked+`) ORDER BY run, rowid`, f.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Finding{}
	for rows.Next() {
		var run string
		var fid sql.NullString
		var kind, pass, sev, action, prior, n sql.NullInt64
		if err := rows.Scan(&run, &fid, &kind, &pass, &sev, &action, &prior, &n); err != nil {
			return nil, err
		}
		out[run] = append(out[run], Finding{Fid: fid.String, Kind: nameOf(names, kind), Pass: numOf(pass),
			Sev: nameOf(names, sev), Action: nameOf(names, action), Prior: nameOf(names, prior), N: n.Int64})
	}
	return out, rows.Err()
}

// State is what health reads of stats.db: its schema, and at this
// binary's schema, its rows.
type State struct {
	Version   int
	Runs      int64
	First     time.Time       // the oldest row's end
	LastPrune time.Time       // zero for none yet
	Stored    map[string]bool // every run with a row
}

// ReadState reads stats.db's schema and, when it is this binary's, its
// rows.
func ReadState(ctx context.Context, db *sql.DB) (State, error) {
	var s State
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return s, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&s.Version); err != nil {
		return s, err
	}
	if s.Version != Version() {
		return s, tx.Commit()
	}
	var first, prune sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT count(*), min(ended_at), (SELECT value FROM meta WHERE key = ?) FROM run_facts`,
		metaLastPrune).Scan(&s.Runs, &first, &prune); err != nil {
		return s, err
	}
	if first.Valid {
		s.First = time.UnixMilli(first.Int64)
	}
	if prune.Valid && prune.Int64 > 0 {
		s.LastPrune = time.UnixMilli(prune.Int64)
	}
	rows, err := tx.QueryContext(ctx, `SELECT run FROM run_facts`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	s.Stored = map[string]bool{}
	for rows.Next() {
		var run string
		if err := rows.Scan(&run); err != nil {
			return s, err
		}
		s.Stored[run] = true
	}
	if err := rows.Err(); err != nil {
		return s, err
	}
	return s, tx.Commit()
}
