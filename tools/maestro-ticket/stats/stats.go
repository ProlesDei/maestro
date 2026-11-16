// Package stats keeps typed facts about each run in stats.db, beside the
// ticket store, so maintainers and a team can see where runs go wrong and
// where spend goes. It stores numbers, bools, id-shaped text and enum ids
// from Go's own lists; never a prompt, reply, path, name or other text.
// Nothing here sends anything anywhere (decision 22).
//
// Only the supervisor creates or migrates the file (Prepare). Other
// writers open it only when it exists at this binary's schema, wait at
// most a quarter second, and drop the write. A stats error never prints:
// a run's end must look the same with stats on, off or broken.
//
// This package imports the standard library only. The caller registers
// the SQLite driver.
package stats

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Files beside the store.
const (
	DBName  = "stats.db"
	OffFile = "STATS_OFF" // while it exists, nothing is written
	ShaFile = "stats.sha" // install.yaml's maestro-sha, as apply set it
	// OffEnv turns stats off for one shell when set to off.
	OffEnv = "MAESTRO_TICKET_STATS"
)

// writeLimit bounds one run's write: the ring waits on it.
const writeLimit = 300 * time.Millisecond

var (
	// ErrNewer is a stats.db a newer binary migrated: this one drops its
	// write, as a supervisor that outlived an upgrade does.
	ErrNewer = errors.New("stats.db has a newer schema")
	// ErrNoSchema is a stats.db no supervisor prepared yet.
	ErrNoSchema = errors.New("stats.db has no schema yet")
)

// Lists are the Go lists the codes table holds, by kind. Each kind also
// gets other, which any name off its list becomes.
type Lists map[string][]string

// Other is the name any value off its kind's list is stored as.
const Other = "other"

// hash is a fingerprint of the lists, so Prepare seeds codes only when
// they changed.
func (l Lists) hash() int64 {
	kinds := make([]string, 0, len(l))
	for k := range l {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	h := sha256.New()
	for _, k := range kinds {
		names := slices.Clone(l[k])
		slices.Sort(names)
		for _, n := range append(names, Other) {
			fmt.Fprintf(h, "%s\x00%s\x00", k, n)
		}
	}
	return int64(binary.BigEndian.Uint64(h.Sum(nil)[:8]) >> 1)
}

// Off says why stats are off for the store in dir, or "" when they are
// on: the STATS_OFF marker, MAESTRO_TICKET_STATS=off, or a store on a
// filesystem the store checks flagged (unsafe).
func Off(dir string, getenv func(string) string, unsafe bool) string {
	switch {
	case getenv != nil && getenv(OffEnv) == "off":
		return "env"
	case unsafe:
		return "unsafe filesystem"
	}
	if _, err := os.Lstat(filepath.Join(dir, OffFile)); !errors.Is(err, fs.ErrNotExist) {
		return "marker" // a marker it can't read counts as one
	}
	return ""
}

// SetOff makes or removes the STATS_OFF marker in dir.
func SetOff(dir string, off bool) error {
	p := filepath.Join(dir, OffFile)
	if !off {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

var shaShape = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// ValidSha reports whether s is a commit sha of 7 to 40 lowercase hex.
func ValidSha(s string) bool { return shaShape.MatchString(s) }

// SetSha writes the maestro-sha apply pinned, whole, through a rename.
func SetSha(dir, sha string) error {
	if !ValidSha(sha) {
		return fmt.Errorf("want a commit sha of 7 to 40 hex, not %q", sha)
	}
	f, err := os.CreateTemp(dir, ".tmp-stats-sha-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(sha + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, ShaFile))
}

// Sha reads the maestro-sha apply wrote, or "" for none or one off its
// shape.
func Sha(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ShaFile))
	if err != nil || len(b) > 64 {
		return ""
	}
	if s := strings.TrimSpace(string(b)); ValidSha(s) {
		return s
	}
	return ""
}

// Open opens stats.db at path for one write; create lets it make the
// file. The caller closes it.
func Open(path string, create bool) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	mode := "rw"
	if create {
		mode = "rwc"
	}
	// A file: name keeps SQLite's mode; these three would end the path.
	name := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(abs)
	db, err := sql.Open("sqlite", "file:"+name+"?mode="+mode+
		"&_busy_timeout=250&_auto_vacuum=2&_journal_mode=WAL&_synchronous=NORMAL&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// isBusy reports SQLITE_BUSY or SQLITE_LOCKED.
func isBusy(err error) bool {
	var se interface{ Code() int }
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case 5, 6:
			return true
		}
	}
	return false
}

// Prepare creates or migrates stats.db and seeds its codes when lists
// changed, retrying a busy lock until ctx ends. Many supervisors may race
// to do it: each takes the write lock and reads the version again.
func Prepare(ctx context.Context, db *sql.DB, lists Lists) error {
	for {
		err := prepare(ctx, db, lists)
		if err == nil || !isBusy(err) || ctx.Err() != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(20+rand.IntN(40)) * time.Millisecond):
		}
	}
}

func prepare(ctx context.Context, db *sql.DB, lists Lists) error {
	tx, err := db.BeginTx(ctx, nil) // BEGIN IMMEDIATE, from the DSN
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var v int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v > Version() {
		return ErrNewer
	}
	if v < Version() {
		for ; v < Version(); v++ {
			if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
				return fmt.Errorf("migrate stats.db %d -> %d: %w", v, v+1, err)
			}
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", Version())); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO meta (key, value) VALUES (?, 0), (?, 0)`, metaLastPrune, metaCodesVersion); err != nil {
		return err
	}
	h := lists.hash()
	var cur int64
	if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, metaCodesVersion).Scan(&cur); err != nil {
		return err
	}
	if cur != h {
		// Codes grow by insert, never by rebuild: a name a later binary
		// drops keeps its id, so old rows still read.
		kinds := make([]string, 0, len(lists))
		for k := range lists {
			kinds = append(kinds, k)
		}
		slices.Sort(kinds)
		for _, k := range kinds {
			for _, n := range append(slices.Clone(lists[k]), Other) {
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO codes (kind, name) VALUES (?, ?)`, k, n); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE meta SET value = ? WHERE key = ?`, h, metaCodesVersion); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Run is one run's row and its models, as cli reads them from the run's
// record and result. Enum fields hold names from the lists; Write stores
// their ids. A field off its shape or list is stored as NULL or other.
type Run struct {
	Run, Session, ResumeOf string
	Ticket                 int64
	// Enums.
	Role, Job, Model, Effort, Nudge, State, Outcome, Reason string
	Subtype, StopReason, Stream, OS, Arch                   string
	Lean, AfterDeath                                        bool
	StartedAt, EndedAt                                      time.Time
	Exit                                                    *int64
	IsError                                                 *bool
	ClaudeMs, APIMs, Turns                                  *int64
	ReplyBytes                                              int64
	DiskFreePct, InodeFreePct                               *int64
	// VTool is the binary's commit, 40 hex; VMaestro install.yaml's
	// maestro-sha; VClaude Claude Code's version, digits and dots.
	VTool, VMaestro, VClaude string
	Models                   []Model
}

// Model is a run's use of one model family, as the session's totals so
// far: deltas come at read time.
type Model struct {
	Model                                            string
	InputCum, OutputCum, CacheReadCum, CacheWriteCum int64
	CostCum                                          *float64
	CostSrc                                          string
}

var (
	runShape    = regexp.MustCompile(`^R-[a-z2-7]{8}$`)
	uuidShape   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	sha40Shape  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	semverShape = regexp.MustCompile(`^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}$`)
)

// shaped is s when it matches re, else NULL.
func shaped(s string, re *regexp.Regexp) any {
	if re.MatchString(s) {
		return s
	}
	return nil
}

func ms(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// codeIDs reads every code once a write: a few hundred rows.
func codeIDs(ctx context.Context, tx *sql.Tx) (func(kind, name string) any, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, kind, name FROM codes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]map[string]int64{}
	for rows.Next() {
		var id int64
		var k, n string
		if err := rows.Scan(&id, &k, &n); err != nil {
			return nil, err
		}
		if ids[k] == nil {
			ids[k] = map[string]int64{}
		}
		ids[k][n] = id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return func(kind, name string) any {
		if name == "" {
			return nil
		}
		id, ok := ids[kind][name]
		if !ok {
			id, ok = ids[kind][Other]
		}
		if !ok {
			return nil
		}
		return id
	}, nil
}

// checkVersion refuses a stats.db at any schema but this binary's.
func checkVersion(ctx context.Context, tx *sql.Tx) error {
	var v int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	switch {
	case v > Version():
		return ErrNewer
	case v < Version():
		return ErrNoSchema
	}
	return nil
}

// Write stores one run's row and its models in one transaction, within
// 300 ms. A run already stored stays as it was.
func Write(ctx context.Context, db *sql.DB, r Run) error {
	ctx, cancel := context.WithTimeout(ctx, writeLimit)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkVersion(ctx, tx); err != nil {
		return err
	}
	code, err := codeIDs(ctx, tx)
	if err != nil {
		return err
	}
	if !runShape.MatchString(r.Run) {
		return fmt.Errorf("run id off its shape")
	}
	var cols []string
	var vals []any
	add := func(c string, v any) { cols, vals = append(cols, c), append(vals, v) }
	add("run", r.Run)
	add("session", shaped(r.Session, uuidShape))
	if r.Ticket > 0 {
		add("ticket", r.Ticket)
	}
	for _, e := range []struct{ col, name string }{
		{"role", r.Role}, {"job", r.Job}, {"model", r.Model}, {"effort", r.Effort}, {"nudge", r.Nudge},
		{"state", r.State}, {"outcome", r.Outcome}, {"reason", r.Reason}, {"subtype", r.Subtype},
		{"stop_reason", r.StopReason}, {"stream", r.Stream}, {"os", r.OS}, {"arch", r.Arch},
	} {
		add(e.col, code(e.col, e.name))
	}
	add("lean", boolInt(r.Lean))
	add("after_death", boolInt(r.AfterDeath))
	add("resume_of", shaped(r.ResumeOf, runShape))
	add("started_at", ms(r.StartedAt))
	add("ended_at", ms(r.EndedAt))
	if !r.StartedAt.IsZero() && !r.EndedAt.IsZero() {
		add("wall_ms", r.EndedAt.Sub(r.StartedAt).Milliseconds())
	}
	if r.Exit != nil {
		add("exit", *r.Exit)
	}
	if r.IsError != nil {
		add("is_error", boolInt(*r.IsError))
	}
	for _, n := range []struct {
		col string
		v   *int64
	}{{"claude_ms", r.ClaudeMs}, {"api_ms", r.APIMs}, {"turns", r.Turns}, {"disk_free_pct", r.DiskFreePct}, {"inode_free_pct", r.InodeFreePct}} {
		if n.v != nil {
			add(n.col, *n.v)
		}
	}
	add("reply_bytes", r.ReplyBytes)
	add("v_tool", shaped(r.VTool, sha40Shape))
	add("v_maestro", shaped(r.VMaestro, shaShape))
	add("v_claude", shaped(r.VClaude, semverShape))
	q := fmt.Sprintf("INSERT INTO run_facts (%s) VALUES (%s) ON CONFLICT (run) DO NOTHING",
		strings.Join(cols, ", "), strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", "))
	res, err := tx.ExecContext(ctx, q, vals...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // stored once already
	}
	// Two raw ids of one family, or two off the list, make one row.
	type sum struct {
		in, out, cr, cw int64
		cost            *float64
		src             any
	}
	sums := map[any]*sum{}
	var order []any
	for _, m := range r.Models {
		id := code("model", m.Model)
		if id == nil {
			continue
		}
		s := sums[id]
		if s == nil {
			s = &sum{src: code("cost_src", m.CostSrc)}
			sums[id] = s
			order = append(order, id)
		}
		s.in += m.InputCum
		s.out += m.OutputCum
		s.cr += m.CacheReadCum
		s.cw += m.CacheWriteCum
		if m.CostCum != nil {
			c := *m.CostCum
			if s.cost != nil {
				c += *s.cost
			}
			s.cost = &c
		}
	}
	for _, id := range order {
		s := sums[id]
		var cost any
		if s.cost != nil {
			cost = *s.cost
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_models (run, model, input_cum, output_cum, cache_read_cum, cache_write_cum, cost_cum, cost_src)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, r.Run, id, s.in, s.out, s.cr, s.cw, cost, s.src); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetRing records, after a run's first ring, whether it reached the
// webhook and how it failed (a ring_err name).
func SetRing(ctx context.Context, db *sql.DB, run string, rang bool, ringErr string) error {
	ctx, cancel := context.WithTimeout(ctx, writeLimit)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkVersion(ctx, tx); err != nil {
		return err
	}
	code, err := codeIDs(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE run_facts SET rang = ?, ring_err = ? WHERE run = ?`,
		boolInt(rang), code("ring_err", ringErr), run); err != nil {
		return err
	}
	return tx.Commit()
}
