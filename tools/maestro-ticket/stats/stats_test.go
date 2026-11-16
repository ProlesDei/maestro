package stats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // the driver cli registers in the binary
)

// childEnv makes the test binary one process of TestPrepareRace: it
// prepares the file named and writes one run.
const childEnv = "STATS_TEST_CHILD"

func TestMain(m *testing.M) {
	if p := os.Getenv(childEnv); p != "" {
		os.Exit(child(p, os.Getenv("STATS_TEST_RUN")))
	}
	os.Exit(m.Run())
}

func child(path, run string) int {
	db, err := Open(path, true)
	if err != nil {
		return 1
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Prepare(ctx, db, testLists); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for {
		err := Write(ctx, db, testRun(run))
		if err == nil {
			return 0
		}
		if !isBusy(err) || ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
}

var testLists = Lists{
	"role": {"planner", "reviewer", "person"}, "job": {"build", "review"}, "model": {"opus", "opus-5.5"},
	"effort": {"high"}, "nudge": {"none", "opus"}, "state": {"done", "failed"}, "outcome": {"done", "failed"},
	"reason": {"no_result"}, "subtype": {"success"}, "stop_reason": {"end_turn"}, "stream": {"off"},
	"os": {"linux"}, "arch": {"amd64"}, "cost_src": {"result"}, "ring_err": {"none", "timeout"},
}

func runID(i int) string {
	const a = "abcdefghijklmnopqrstuvwxyz234567"
	b := []byte("R-aaaaaaaa")
	for j := 9; i > 0; j-- {
		b[j] = a[i%32]
		i /= 32
	}
	return string(b)
}

func testRun(id string) Run {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	cost := 1.5
	return Run{Run: id, Session: "11111111-1111-4111-8111-111111111111", Ticket: 3, Role: "planner", Job: "build",
		Model: "opus", Effort: "high", State: "done", Outcome: "done", Stream: "off", OS: "linux", Arch: "amd64",
		StartedAt: start, EndedAt: start.Add(time.Minute),
		Models: []Model{{Model: "opus-5.5", InputCum: 10, OutputCum: 2, CostCum: &cost, CostSrc: "result"}}}
}

// fresh is a prepared stats.db in a temp dir.
func fresh(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), DBName)
	db, err := Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Prepare(t.Context(), db, testLists); err != nil {
		t.Fatal(err)
	}
	return db, path
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriteStoresIDs(t *testing.T) {
	db, _ := fresh(t)
	r := testRun(runID(1))
	r.Model, r.Effort = "claude-zqmark", "nope" // off the list: other
	r.Session = "NOT-A-UUID"
	if err := Write(t.Context(), db, r); err != nil {
		t.Fatal(err)
	}
	var model, effort string
	var session sql.NullString
	if err := db.QueryRow(`SELECT m.name, e.name, f.session FROM run_facts f JOIN codes m ON m.id = f.model JOIN codes e ON e.id = f.effort`).Scan(&model, &effort, &session); err != nil {
		t.Fatal(err)
	}
	if model != Other || effort != Other || session.Valid {
		t.Fatalf("model %q effort %q session %v: want other, other, NULL", model, effort, session)
	}
	if n := count(t, db, `SELECT count(*) FROM run_models`); n != 1 {
		t.Fatalf("%d model rows", n)
	}
	// A second write of the run changes nothing.
	r.Outcome = "failed"
	if err := Write(t.Context(), db, r); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM run_facts f JOIN codes c ON c.id = f.outcome WHERE c.name = 'done'`); n != 1 {
		t.Fatal("a second write replaced the row")
	}
}

// A db one version behind migrates and takes the run in the same end:
// what a supervisor's run end does after an upgrade.
func TestPrepareUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), DBName)
	db, err := Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Prepare(t.Context(), db, testLists); err != nil {
		t.Fatal(err)
	}
	old := migrations
	t.Cleanup(func() { migrations = old })
	migrations = append(slices.Clone(old), `CREATE TABLE later (n INTEGER) STRICT`)
	if err := Write(t.Context(), db, testRun(runID(1))); !errors.Is(err, ErrNoSchema) {
		t.Fatalf("write before the migration: %v; want ErrNoSchema", err)
	}
	if err := Prepare(t.Context(), db, testLists); err != nil {
		t.Fatal(err)
	}
	if err := Write(t.Context(), db, testRun(runID(1))); err != nil {
		t.Fatal(err)
	}
	if v := count(t, db, `PRAGMA user_version`); v != 2 {
		t.Fatalf("user_version %d", v)
	}
	if count(t, db, `SELECT count(*) FROM run_facts`) != 1 || count(t, db, `SELECT count(*) FROM later`) != 0 {
		t.Fatal("no row after the migration")
	}
}

// Twenty processes find no file and race to make it: one schema, codes
// seeded once, every run stored.
func TestPrepareRace(t *testing.T) {
	path := filepath.Join(t.TempDir(), DBName)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	out := make([][]byte, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := exec.Command(exe)
			c.Env = append(os.Environ(), childEnv+"="+path, "STATS_TEST_RUN="+runID(i+1))
			out[i], errs[i] = c.CombinedOutput()
		}()
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("process %d: %v: %s", i, errs[i], out[i])
		}
	}
	db, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v := count(t, db, `PRAGMA user_version`); v != Version() {
		t.Fatalf("user_version %d", v)
	}
	if av := count(t, db, `PRAGMA auto_vacuum`); av != 2 {
		t.Fatalf("auto_vacuum %d; want 2 (incremental), set before the first table", av)
	}
	if got := count(t, db, `SELECT count(*) FROM run_facts`); got != n {
		t.Fatalf("%d rows; want %d", got, n)
	}
	if d := count(t, db, `SELECT count(*) - count(DISTINCT kind || '/' || name) FROM codes`); d != 0 {
		t.Fatalf("%d duplicate codes", d)
	}
	if got, want := count(t, db, `SELECT count(*) FROM codes`), codeCount(testLists); got != want {
		t.Fatalf("%d codes; want %d", got, want)
	}
	if got := count(t, db, `SELECT count(*) FROM sqlite_master WHERE type = 'table'`); got != 8 {
		t.Fatalf("%d tables", got)
	}
}

func codeCount(l Lists) int {
	n := 0
	for _, names := range l {
		n += len(names) + 1
	}
	return n
}

func TestNewerSchemaDrops(t *testing.T) {
	db, _ := fresh(t)
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := Write(t.Context(), db, testRun(runID(1))); !errors.Is(err, ErrNewer) {
		t.Fatalf("write: %v; want ErrNewer", err)
	}
	if err := Prepare(t.Context(), db, testLists); !errors.Is(err, ErrNewer) {
		t.Fatalf("prepare: %v; want ErrNewer", err)
	}
	if err := SetRing(t.Context(), db, runID(1), true, "none"); !errors.Is(err, ErrNewer) {
		t.Fatalf("set ring: %v; want ErrNewer", err)
	}
	if ok, err := Prune(t.Context(), db, time.Now()); ok || err != nil {
		t.Fatalf("prune: %v %v; want no claim", ok, err)
	}
	if n := count(t, db, `SELECT count(*) FROM run_facts`); n != 0 {
		t.Fatalf("%d rows", n)
	}
}

// Prepare seeds codes only when the lists changed, and only adds.
func TestCodesSeedOnce(t *testing.T) {
	db, _ := fresh(t)
	if _, err := db.Exec(`DELETE FROM codes WHERE kind = 'job' AND name = 'review'`); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(t.Context(), db, testLists); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM codes WHERE kind = 'job' AND name = 'review'`); n != 0 {
		t.Fatal("the same lists seeded again")
	}
	var buildID int
	db.QueryRow(`SELECT id FROM codes WHERE kind = 'job' AND name = 'build'`).Scan(&buildID)
	l := Lists{}
	for k, v := range testLists {
		l[k] = v
	}
	l["job"] = []string{"build", "review", "design"}
	if err := Prepare(t.Context(), db, l); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM codes WHERE kind = 'job' AND name IN ('review', 'design')`); n != 2 {
		t.Fatalf("changed lists seeded %d of 2 new names", n)
	}
	if id := count(t, db, `SELECT id FROM codes WHERE kind = 'job' AND name = 'build'`); id != buildID {
		t.Fatalf("build's id moved from %d to %d", buildID, id)
	}
	// A name a later list drops keeps its row.
	l["job"] = []string{"build"}
	if err := Prepare(t.Context(), db, l); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM codes WHERE kind = 'job' AND name = 'design'`); n != 1 {
		t.Fatal("a dropped name lost its code")
	}
}

var bannedCols = []string{"prompt", "reply", "stderr", "title", "body", "note", "path", "url", "host", "user", "command", "text", "name"}

// Every table is STRICT; every enum column is INTEGER; every TEXT column
// outside codes has a GLOB check on its shape; no column is named for
// text a run carries.
func TestSchemaLint(t *testing.T) {
	db, _ := fresh(t)
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]string{}
	for rows.Next() {
		var n, s string
		rows.Scan(&n, &s)
		tables[n] = s
	}
	rows.Close()
	for _, want := range []string{"codes", "meta", "run_facts", "run_models", "run_steps", "findings", "daily", "pruned_cum"} {
		if tables[want] == "" {
			t.Errorf("no table %s", want)
		}
	}
	for name, sqlText := range tables {
		if !regexp.MustCompile(`\)\s*STRICT\s*$`).MatchString(sqlText) {
			t.Errorf("%s isn't STRICT", name)
		}
		cols := map[string]string{}
		rows, err := db.Query(`SELECT name, type FROM pragma_table_info(?)`, name)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var c, ty string
			rows.Scan(&c, &ty)
			cols[c] = ty
			if slices.Contains(bannedCols, c) && name != "codes" {
				t.Errorf("%s.%s names text a run carries", name, c)
			}
			if ty != "INTEGER" && ty != "REAL" && ty != "TEXT" {
				t.Errorf("%s.%s is %s", name, c, ty)
			}
			if ty == "TEXT" && name != "codes" &&
				!regexp.MustCompile(`\b`+c+`\b[^,]*\bGLOB\b`).MatchString(sqlText) {
				t.Errorf("%s.%s is TEXT with no GLOB check on its shape", name, c)
			}
		}
		rows.Close()
		for _, e := range EnumCols[name] {
			if cols[e] != "INTEGER" {
				t.Errorf("enum %s.%s is %q, not INTEGER", name, e, cols[e])
			}
		}
	}
	for name := range EnumCols {
		if tables[name] == "" {
			t.Errorf("EnumCols names no table %s", name)
		}
	}
}

func TestStrictRefusesText(t *testing.T) {
	db, _ := fresh(t)
	for _, q := range []string{
		`INSERT INTO run_facts (run, exit) VALUES ('R-aaaaaaab', 'zqmark7')`,
		`INSERT INTO run_facts (run, job) VALUES ('R-aaaaaaab', 'build')`,
		`INSERT INTO run_facts (run) VALUES ('zqmark7')`,
		`INSERT INTO run_facts (run, session) VALUES ('R-aaaaaaab', 'zqmark7')`,
		`INSERT INTO run_facts (run, v_tool) VALUES ('R-aaaaaaab', 'deadbeefcafe')`,
		`INSERT INTO run_facts (run, v_maestro) VALUES ('R-aaaaaaab', 'zqmark7')`,
		`INSERT INTO run_facts (run, v_claude) VALUES ('R-aaaaaaab', '2.1.x')`,
		`INSERT INTO run_facts (run, tail) VALUES ('R-aaaaaaab', '12 zq')`,
		`INSERT INTO findings (run, fid) VALUES ('R-aaaaaaab', 'F12345')`,
		`INSERT INTO run_models (run, model, cost_cum) VALUES ('R-aaaaaaab', 1, 'lots')`,
	} {
		if _, err := db.Exec(q); err == nil {
			t.Errorf("stats.db took %s", q)
		}
	}
	if _, err := db.Exec(`INSERT INTO run_facts (run, v_claude, tail, v_maestro) VALUES ('R-aaaaaaab', '2.1.322', '12 7! 3', 'abc1234')`); err != nil {
		t.Errorf("a row of good shapes: %v", err)
	}
}

func TestStatsOffMarker(t *testing.T) {
	dir := t.TempDir()
	if why := Off(dir, os.Getenv, false); why != "" {
		t.Fatalf("on by default; got off: %s", why)
	}
	if err := SetOff(dir, true); err != nil {
		t.Fatal(err)
	}
	if why := Off(dir, nil, false); why != "marker" {
		t.Fatalf("with the marker: %q", why)
	}
	if err := SetOff(dir, true); err != nil {
		t.Fatal("a second off:", err)
	}
	if err := SetOff(dir, false); err != nil {
		t.Fatal(err)
	}
	if err := SetOff(dir, false); err != nil {
		t.Fatal("a second on:", err)
	}
	if why := Off(dir, nil, false); why != "" {
		t.Fatalf("after on: %q", why)
	}
}

func TestStatsOffEnv(t *testing.T) {
	dir := t.TempDir()
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == OffEnv {
				return v
			}
			return ""
		}
	}
	if why := Off(dir, env("off"), false); why != "env" {
		t.Fatalf("%s=off: %q", OffEnv, why)
	}
	if why := Off(dir, env("on"), false); why != "" {
		t.Fatalf("%s=on: %q", OffEnv, why)
	}
	if why := Off(dir, env(""), true); why == "" {
		t.Fatal("on over an unsafe filesystem")
	}
}

func TestShaFile(t *testing.T) {
	dir := t.TempDir()
	if Sha(dir) != "" {
		t.Fatal("a sha with no file")
	}
	for _, bad := range []string{"", "abc", "ZQMARK7", "deadbeefcafe; rm", strings.Repeat("a", 41)} {
		if SetSha(dir, bad) == nil {
			t.Errorf("SetSha took %q", bad)
		}
	}
	if err := SetSha(dir, "abc1234"); err != nil {
		t.Fatal(err)
	}
	if got := Sha(dir); got != "abc1234" {
		t.Fatalf("Sha %q", got)
	}
	os.WriteFile(filepath.Join(dir, ShaFile), []byte("zqmark7\n"), 0o644)
	if got := Sha(dir); got != "" {
		t.Fatalf("Sha read %q off its shape", got)
	}
}

// Fifty supervisors at the same hour: one prunes.
func TestPruneClaimOnce(t *testing.T) {
	_, path := fresh(t)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	claims := func() int {
		var wg sync.WaitGroup
		var mu sync.Mutex
		won := 0
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				db, err := Open(path, false)
				if err != nil {
					t.Error(err)
					return
				}
				defer db.Close()
				if ok, _ := Prune(t.Context(), db, now); ok {
					mu.Lock()
					won++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		return won
	}
	if n := claims(); n != 1 {
		t.Fatalf("%d claims; want 1", n)
	}
	now = now.Add(30 * time.Minute)
	if n := claims(); n != 0 {
		t.Fatalf("%d claims half an hour on; want 0", n)
	}
	now = now.Add(31 * time.Minute)
	if n := claims(); n != 1 {
		t.Fatalf("%d claims an hour on; want 1", n)
	}
}

// A self-seeding claim: a meta with no last_prune still claims once.
func TestPruneClaimSeeds(t *testing.T) {
	db, _ := fresh(t)
	db.Exec(`DELETE FROM meta WHERE key = ?`, metaLastPrune)
	now := time.Now()
	if ok, err := Prune(t.Context(), db, now); !ok || err != nil {
		t.Fatalf("first: %v %v", ok, err)
	}
	if ok, _ := Prune(t.Context(), db, now); ok {
		t.Fatal("claimed twice in one hour")
	}
}

// Runs past 90 days roll up into daily and go; so do the oldest while the
// file is over its size; daily keeps 400 days.
func TestPruneCaps(t *testing.T) {
	db, _ := fresh(t)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	put := func(i int, end time.Time, wall time.Duration, cost float64, resumeOf string, session string) {
		r := testRun(runID(i))
		r.EndedAt, r.StartedAt = end, end.Add(-wall)
		r.Session, r.ResumeOf = session, resumeOf
		r.Models[0].CostCum = &cost
		r.Models[0].InputCum = int64(cost * 100)
		if err := Write(t.Context(), db, r); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-100 * 24 * time.Hour)
	// One session over two runs: the resume spent 2.0 of its 3.0 total.
	put(1, old, time.Minute, 1, "", "11111111-1111-4111-8111-111111111111")
	put(2, old.Add(time.Hour), 3*time.Minute, 3, runID(1), "11111111-1111-4111-8111-111111111111")
	put(3, old.Add(2*time.Hour), 2*time.Minute, 0.5, "", "22222222-2222-4222-8222-222222222222")
	put(4, now.Add(-time.Hour), time.Minute, 1, "", "33333333-3333-4333-8333-333333333333")
	db.Exec(`INSERT INTO daily (day, job, model, outcome, n) VALUES (1, 1, 1, 1, 5)`) // 1970: past 400 days
	l := defaultLimits
	if ok, err := prune(t.Context(), db, now, l); !ok || err != nil {
		t.Fatalf("prune: %v %v", ok, err)
	}
	if n := count(t, db, `SELECT count(*) FROM run_facts`); n != 1 {
		t.Fatalf("%d runs left; want the one inside 90 days", n)
	}
	if n := count(t, db, `SELECT count(*) FROM run_models`); n != 1 {
		t.Fatalf("%d model rows left", n)
	}
	var n, p50, p90 int64
	var usd float64
	if err := db.QueryRow(`SELECT n, cost_usd, ms_p50, ms_p90 FROM daily`).Scan(&n, &usd, &p50, &p90); err != nil {
		t.Fatal(err)
	}
	if n != 3 || usd != 3.5 || p50 != 120000 || p90 != 180000 {
		t.Fatalf("daily n %d usd %v p50 %d p90 %d; want 3, 3.5 (1 + 2 + 0.5), 120000, 180000", n, usd, p50, p90)
	}
	// Over the size cap, the oldest go whatever their age, in batches.
	for i := 10; i < 25; i++ {
		put(i, now.Add(-time.Duration(30-i)*time.Hour), time.Minute, 1, "", "44444444-4444-4444-8444-444444444444")
	}
	l.bytes, l.batch = 1, 4
	if ok, err := prune(t.Context(), db, now.Add(2*time.Hour), l); !ok || err != nil {
		t.Fatalf("prune: %v %v", ok, err)
	}
	if n := count(t, db, `SELECT count(*) FROM run_facts`); n != 0 {
		t.Fatalf("%d runs left over the size cap", n)
	}
	if n := count(t, db, `SELECT sum(n) FROM daily`); n != 3+16 {
		t.Fatalf("daily counts %d runs; want 19", n)
	}
	// Within its budget.
	l.budget = 0
	put(30, old, time.Minute, 1, "", "55555555-5555-4555-8555-555555555555")
	if ok, _ := prune(t.Context(), db, now.Add(4*time.Hour), l); !ok {
		t.Fatal("no claim")
	}
	if n := count(t, db, `SELECT count(*) FROM run_facts`); n != 1 {
		t.Fatal("a prune with no time left deleted rows")
	}
}

// A resume rolls up its own spend even when its parent went in an
// earlier prune or batch; a model new to the session and a run with no
// session count their whole totals; a pruned session's totals go in time.
func TestPruneResumeAcrossPrunes(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	line := now.Add(-90 * 24 * time.Hour)
	const s1 = "11111111-1111-4111-8111-111111111111"
	put := func(db *sql.DB, i int, end time.Time, cost float64, session, resumeOf string, extra ...Model) {
		r := testRun(runID(i))
		r.EndedAt, r.StartedAt = end, end.Add(-time.Minute)
		r.Session, r.ResumeOf = session, resumeOf
		r.Models[0].CostCum = &cost
		r.Models[0].InputCum = int64(cost * 100)
		r.Models = append(r.Models, extra...)
		if err := Write(t.Context(), db, r); err != nil {
			t.Fatal(err)
		}
	}
	sums := func(db *sql.DB) (float64, int64) {
		var usd float64
		var tok int64
		if err := db.QueryRow(`SELECT total(cost_usd), total(tokens) FROM daily`).Scan(&usd, &tok); err != nil {
			t.Fatal(err)
		}
		return usd, tok
	}
	quarter := 0.25
	newModel := Model{Model: "opus", InputCum: 5, CostCum: &quarter, CostSrc: "result"}

	// The parent goes an hour before the resume crosses the line.
	db, _ := fresh(t)
	put(db, 1, line.Add(-30*time.Minute), 1, s1, "")
	put(db, 2, line.Add(30*time.Minute), 3, s1, runID(1), newModel)
	if ok, err := prune(t.Context(), db, now, defaultLimits); !ok || err != nil {
		t.Fatalf("prune: %v %v", ok, err)
	}
	if n := count(t, db, `SELECT count(*) FROM run_facts`); n != 1 {
		t.Fatalf("%d runs left after the first prune; want the resume", n)
	}
	if n := count(t, db, `SELECT count(*) FROM pruned_cum`); n != 1 {
		t.Fatalf("%d pruned_cum rows; want the parent's", n)
	}
	if ok, err := prune(t.Context(), db, now.Add(2*time.Hour), defaultLimits); !ok || err != nil {
		t.Fatalf("prune: %v %v", ok, err)
	}
	// Costs 1 + 2 + 0.25; tokens 102 + 200 + 5.
	if usd, tok := sums(db); usd != 3.25 || tok != 307 {
		t.Fatalf("over two prunes: daily usd %v tokens %d; want 3.25, 307", usd, tok)
	}

	// Parent and resume in one prune, a batch each; and a run with no
	// session, which counts its whole total.
	db, _ = fresh(t)
	put(db, 1, line.Add(-3*time.Hour), 1, s1, "")
	put(db, 2, line.Add(-2*time.Hour), 3, s1, runID(1))
	put(db, 3, line.Add(-time.Hour), 2, "not-a-uuid", runID(9))
	l := defaultLimits
	l.batch = 1
	if ok, err := prune(t.Context(), db, now, l); !ok || err != nil {
		t.Fatalf("prune: %v %v", ok, err)
	}
	if n := count(t, db, `SELECT count(*) FROM run_facts`); n != 0 {
		t.Fatalf("%d runs left", n)
	}
	if usd, tok := sums(db); usd != 5 || tok != 302+202 {
		t.Fatalf("a batch each: daily usd %v tokens %d; want 5, 504", usd, tok)
	}

	// A session's totals wait 30 days past the line, then go.
	if ok, err := prune(t.Context(), db, now.Add(29*24*time.Hour), l); !ok || err != nil {
		t.Fatalf("prune: %v %v", ok, err)
	}
	if n := count(t, db, `SELECT count(*) FROM pruned_cum`); n != 1 {
		t.Fatalf("%d pruned_cum rows 29 days on; want 1", n)
	}
	if ok, err := prune(t.Context(), db, now.Add(31*24*time.Hour), l); !ok || err != nil {
		t.Fatalf("prune: %v %v", ok, err)
	}
	if n := count(t, db, `SELECT count(*) FROM pruned_cum`); n != 0 {
		t.Fatalf("%d pruned_cum rows 31 days on; want 0", n)
	}
}

// Every write of a burst against a lock either lands whole or is
// dropped whole: rows and drops add up to the burst.
func TestStatsBurstBalances(t *testing.T) {
	_, path := fresh(t)
	hold, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	tx, err := hold.Begin()
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(600 * time.Millisecond); tx.Rollback() }()
	const n = 60
	var wg sync.WaitGroup
	var mu sync.Mutex
	drops := 0
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i) * 10 * time.Millisecond)
			db, err := Open(path, false)
			if err == nil {
				defer db.Close()
				err = Write(t.Context(), db, testRun(runID(i+1)))
			}
			if err != nil {
				mu.Lock()
				drops++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	db, _ := Open(path, false)
	defer db.Close()
	rows := count(t, db, `SELECT count(*) FROM run_facts`)
	if rows+drops != n || rows != count(t, db, `SELECT count(DISTINCT run) FROM run_models`) {
		t.Fatalf("rows %d + drops %d != %d, or a run lost its models", rows, drops, n)
	}
	t.Logf("%d stored, %d dropped behind a 600 ms lock", rows, drops)
}

// The stats package reaches no network, runs nothing and holds no
// driver: cli passes it plain structs.
func TestStatsDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal("go list:", err) // a skip would pass the guard unchecked
	}
	for _, p := range strings.Fields(string(out)) {
		switch {
		case p == "net", strings.HasPrefix(p, "net/"), p == "os/exec", p == "crypto/tls",
			strings.HasPrefix(p, "modernc.org/"), strings.HasPrefix(p, "github.com/google/uuid"),
			strings.HasSuffix(p, "/maestro-ticket/runs"), strings.HasSuffix(p, "/maestro-ticket/cli"),
			strings.HasSuffix(p, "/maestro-ticket/sqlitestore"):
			t.Errorf("stats depends on %s", p)
		}
	}
}

// Stats code names no file that holds text: a run's stdin or stderr,
// webhooks, backups, handouts, Claude's own files or the team's setup.
func TestStatsNeverReads(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	more, _ := filepath.Glob("../cli/stats*.go")
	files = append(files, more...)
	banned := func(s string) bool {
		if strings.HasSuffix(s, ".in") || strings.HasSuffix(s, ".err") {
			return true
		}
		for _, w := range []string{"notify", "backups", "handouts", ".claude", "project-setup"} {
			if strings.Contains(s, w) {
				return true
			}
		}
		return false
	}
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		seen++
		fs := token.NewFileSet()
		af, err := parser.ParseFile(fs, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.STRING {
				if s, err := strconv.Unquote(l.Value); err == nil && banned(s) {
					t.Errorf("%s: literal %q", fs.Position(l.Pos()), s)
				}
			}
			return true
		})
	}
	if seen < 4 {
		t.Fatalf("read %d files; want stats and cli/stats*", seen)
	}
}

// A view's spend per run is what prune rolls into daily: one rule, read
// before and after the runs go.
func TestReadSpendMatchesPrune(t *testing.T) {
	db, _ := fresh(t)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	end := now.Add(-100 * 24 * time.Hour)
	const s1 = "11111111-1111-4111-8111-111111111111"
	for i, c := range []struct {
		cost             float64
		session, resumes string
	}{{1, s1, ""}, {2.5, s1, runID(1)}, {4, s1, runID(2)}, {0.5, "", ""}, {7, "33333333-3333-4333-8333-333333333333", runID(30)}} {
		r := testRun(runID(i + 1))
		r.StartedAt, r.EndedAt = end.Add(time.Duration(i)*time.Minute), end.Add(time.Duration(i)*time.Minute+30*time.Second)
		r.Session, r.ResumeOf = c.session, c.resumes
		cost := c.cost
		r.Models[0].CostCum = &cost
		if err := Write(t.Context(), db, r); err != nil {
			t.Fatal(err)
		}
	}
	facts, err := Read(t.Context(), db, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var usd float64
	var unknown int
	for _, f := range facts {
		for _, s := range f.Spend {
			if s.USD == nil {
				unknown++
				continue
			}
			usd += *s.USD
		}
	}
	// 1 + 1.5 + 1.5 + 0.5; the resume of a session never seen has none.
	if len(facts) != 5 || usd != 4.5 || unknown != 1 {
		t.Fatalf("read: %d runs, $%v, %d unknown", len(facts), usd, unknown)
	}
	if one, err := Read(t.Context(), db, Filter{Run: runID(3)}); err != nil || len(one) != 1 || *one[0].Spend[0].USD != 1.5 {
		t.Fatalf("one run's spend: %+v %v", one, err)
	}
	if ok, err := prune(t.Context(), db, now, defaultLimits); !ok || err != nil {
		t.Fatalf("prune: %v %v", ok, err)
	}
	var daily float64
	if err := db.QueryRow(`SELECT total(cost_usd) FROM daily`).Scan(&daily); err != nil || daily != usd {
		t.Fatalf("daily $%v; the view read $%v", daily, usd)
	}
}
