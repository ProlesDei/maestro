package stats

import (
	"context"
	"database/sql"
	"time"
)

// limits bound what stats.db keeps and how long one prune runs.
type limits struct {
	age      time.Duration // detail rows older than this go
	bytes    int64         // or, while live pages hold more, the oldest go
	dailyAge time.Duration // daily rows older than this go
	gap      time.Duration // a pruned session's totals wait this long past age for a resume
	budget   time.Duration // one prune stops after this long
	batch    int           // runs per delete transaction
}

var defaultLimits = limits{
	age:      90 * 24 * time.Hour,
	bytes:    256 << 20,
	dailyAge: 400 * 24 * time.Hour,
	gap:      30 * 24 * time.Hour,
	budget:   2 * time.Second,
	batch:    1000,
}

// pruneEvery is how often one supervisor, of however many, prunes.
const pruneEvery = time.Hour

// Prune runs once an hour across every supervisor: the first to claim the
// hour rolls old runs up into daily, keeps each session's last totals for
// a later resume, deletes the runs in short transactions,
// so a run's write can get in between, and gives back free pages. It
// reports whether this call claimed the hour.
func Prune(ctx context.Context, db *sql.DB, now time.Time) (bool, error) {
	return prune(ctx, db, now, defaultLimits)
}

func prune(ctx context.Context, db *sql.DB, now time.Time, l limits) (bool, error) {
	var v int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return false, err
	}
	if v != Version() {
		return false, nil
	}
	// The claim seeds itself: one statement, so of supervisors at once
	// only the one whose upsert changed the row prunes.
	res, err := db.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value WHERE value < excluded.value - ?`,
		metaLastPrune, now.UnixMilli(), pruneEvery.Milliseconds())
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	stop := time.Now().Add(l.budget)
	cutoff := now.Add(-l.age).UnixMilli()
	for time.Now().Before(stop) && ctx.Err() == nil {
		var live int64
		if err := db.QueryRowContext(ctx, `SELECT (page_count - freelist_count) * page_size
			FROM pragma_page_count, pragma_freelist_count, pragma_page_size`).Scan(&live); err != nil {
			return true, err
		}
		n, err := pruneBatch(ctx, db, cutoff, live > l.bytes, l.batch)
		if err != nil {
			return true, err
		}
		if n == 0 {
			break
		}
	}
	day := now.Add(-l.dailyAge).UnixMilli() / int64(24*time.Hour/time.Millisecond)
	if _, err := db.ExecContext(ctx, `DELETE FROM daily WHERE day < ?`, day); err != nil {
		return true, err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM pruned_cum WHERE ended_at < ?`, cutoff-l.gap.Milliseconds()); err != nil {
		return true, err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA incremental_vacuum(2000)`); err != nil {
		return true, err
	}
	_, err = db.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`)
	return true, err
}

// batchRuns picks the oldest runs to go: those past the age, or any while
// the file is over its size. Each statement of a batch picks the same
// runs, since run_facts changes only in the last.
const batchRuns = `SELECT run FROM run_facts WHERE (?1 OR ended_at < ?2) ORDER BY ended_at, run LIMIT ?3`

// streamSrc is the cost_src id of a run_models row from a stream.
const streamSrc = `(SELECT id FROM codes WHERE kind = 'cost_src' AND name = 'stream')`

// spendCTE names d (run, model, cost, tok): each run's own spend per
// model, of the runs where picks (f is run_facts), which must take in the
// rest of their sessions, so each finds the run before it. A run's spend is its session's total less the run
// before it in that session: from run_facts, or, once that run is gone,
// from pruned_cum. A fresh session's first run spent its whole total, as
// did a run with no session; so did a model new to a known session. A
// resume with no trace of its session's past spent an unknown sum: NULL.
// A run with no result (cost_src stream) holds its own turns' tokens, not
// a total, and no dollars: it spent those tokens whole, and the next run
// in its session, whose total takes in the dead run's, spent NULL.
func spendCTE(where string) string {
	return `x AS (
			SELECT m.run, m.model, f.session, f.resume_of, f.ended_at, m.cost_cum,
				m.input_cum + m.output_cum + m.cache_read_cum + m.cache_write_cum AS tok,
				COALESCE(m.cost_src = ` + streamSrc + `, 0) AS own,
				EXISTS (SELECT 1 FROM run_facts e WHERE e.session = f.session AND (e.ended_at, e.run) < (f.ended_at, f.run))
					OR EXISTS (SELECT 1 FROM pruned_cum e WHERE e.session = f.session) AS known
			FROM run_models m JOIN run_facts f ON f.run = m.run
			WHERE ` + where + `
		),
		d AS (
			SELECT x.run, x.model,
				x.cost_cum - CASE WHEN x.own OR x.resume_of IS NULL OR x.session IS NULL THEN 0
					WHEN LAG(x.run) OVER w IS NOT NULL THEN LAG(x.cost_cum) OVER w
					WHEN p.session IS NOT NULL THEN p.cost_cum
					WHEN x.known THEN 0 END AS cost,
				x.tok - CASE WHEN x.own OR x.resume_of IS NULL OR x.session IS NULL THEN 0
					WHEN LAG(x.run) OVER w IS NOT NULL THEN CASE WHEN LAG(x.own) OVER w THEN NULL ELSE LAG(x.tok) OVER w END
					WHEN p.session IS NOT NULL THEN p.tok_cum
					WHEN x.known THEN 0 END AS tok
			FROM x LEFT JOIN pruned_cum p ON p.session = x.session AND p.model = x.model
			WINDOW w AS (PARTITION BY COALESCE(x.session, x.run), x.model ORDER BY x.ended_at, x.run)
		)`
}

// pruneBatch rolls one batch of runs up into daily and deletes them, in
// one transaction, and returns how many went. A day's percentiles over
// two batches are their weighted mean, so a day of more than a batch of
// runs has close, not exact, ones.
func pruneBatch(ctx context.Context, db *sql.DB, cutoff int64, over bool, batch int) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `WITH b AS (`+batchRuns+`),
		`+spendCTE(`f.run IN (SELECT run FROM b)
				OR f.session IN (SELECT session FROM run_facts WHERE run IN (SELECT run FROM b))`)+`,
		r AS (
			SELECT f.ended_at / 86400000 AS day, COALESCE(f.job, 0) AS job, COALESCE(f.model, 0) AS model,
				COALESCE(f.outcome, 0) AS outcome, f.wall_ms,
				(SELECT SUM(cost) FROM d WHERE d.run = f.run) AS cost,
				(SELECT SUM(tok) FROM d WHERE d.run = f.run) AS tok
			FROM run_facts f WHERE f.run IN (SELECT run FROM b)
		),
		q AS (
			SELECT *, ROW_NUMBER() OVER (g ORDER BY wall_ms) AS rn, COUNT(*) OVER g AS cnt FROM r
			WINDOW g AS (PARTITION BY day, job, model, outcome)
		)
		INSERT INTO daily (day, job, model, outcome, n, cost_usd, tokens, ms_p50, ms_p90)
		SELECT day, job, model, outcome, COUNT(*), SUM(cost), SUM(tok),
			MAX(CASE WHEN rn = (cnt + 1) / 2 THEN wall_ms END),
			MAX(CASE WHEN rn = (cnt * 9 + 9) / 10 THEN wall_ms END)
		FROM q WHERE true GROUP BY day, job, model, outcome
		ON CONFLICT (day, job, model, outcome) DO UPDATE SET
			ms_p50 = (COALESCE(ms_p50, 0) * n + COALESCE(excluded.ms_p50, 0) * excluded.n) / (n + excluded.n),
			ms_p90 = (COALESCE(ms_p90, 0) * n + COALESCE(excluded.ms_p90, 0) * excluded.n) / (n + excluded.n),
			n = n + excluded.n,
			cost_usd = CASE WHEN cost_usd IS NULL AND excluded.cost_usd IS NULL THEN NULL
				ELSE COALESCE(cost_usd, 0) + COALESCE(excluded.cost_usd, 0) END,
			tokens = CASE WHEN tokens IS NULL AND excluded.tokens IS NULL THEN NULL
				ELSE COALESCE(tokens, 0) + COALESCE(excluded.tokens, 0) END`,
		over, cutoff, batch); err != nil {
		return 0, err
	}
	// Keep each session's last totals in the batch, for a resume that a
	// later batch or prune rolls up. A stream's tokens are no total: NULL.
	if _, err := tx.ExecContext(ctx, `INSERT INTO pruned_cum (session, model, ended_at, cost_cum, tok_cum)
		SELECT session, model, ended_at, cost_cum, tok FROM (
			SELECT f.session, m.model, f.ended_at, m.cost_cum,
				CASE WHEN COALESCE(m.cost_src = `+streamSrc+`, 0) THEN NULL
					ELSE m.input_cum + m.output_cum + m.cache_read_cum + m.cache_write_cum END AS tok,
				ROW_NUMBER() OVER (PARTITION BY f.session, m.model ORDER BY f.ended_at DESC, f.run DESC) AS rn
			FROM run_models m JOIN run_facts f ON f.run = m.run
			WHERE f.run IN (`+batchRuns+`) AND f.session IS NOT NULL
		) WHERE rn = 1
		ON CONFLICT (session, model) DO UPDATE SET
			ended_at = excluded.ended_at, cost_cum = excluded.cost_cum, tok_cum = excluded.tok_cum
			WHERE excluded.ended_at >= ended_at`, over, cutoff, batch); err != nil {
		return 0, err
	}
	var n int64
	for _, t := range []string{"run_models", "run_steps", "findings", "run_facts"} {
		res, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE run IN (`+batchRuns+`)`, over, cutoff, batch)
		if err != nil {
			return 0, err
		}
		n, _ = res.RowsAffected()
	}
	return n, tx.Commit()
}
