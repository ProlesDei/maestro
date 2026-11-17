//go:build unix

package cli

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/streamfilter"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// prepareStats makes a stats.db in dir as a supervisor would.
func prepareStatsAt(t *testing.T, dir string) {
	t.Helper()
	db, err := stats.Open(filepath.Join(dir, stats.DBName), true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := stats.Prepare(t.Context(), db, statsLists()); err != nil {
		t.Fatal(err)
	}
}

// statsQuery runs one query on dir's stats.db and scans its one row.
func statsQuery(t *testing.T, dir, q string, dest ...any) {
	t.Helper()
	db, err := stats.Open(filepath.Join(dir, stats.DBName), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(q).Scan(dest...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func statsRowCount(t *testing.T, dir string) int {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, stats.DBName)); err != nil {
		return 0
	}
	var n int
	statsQuery(t, dir, `SELECT count(*) FROM run_facts`, &n)
	return n
}

// holdStats holds stats.db's write lock until release, or the test's end.
func holdStats(t *testing.T, dir string) (release func()) {
	t.Helper()
	db, err := stats.Open(filepath.Join(dir, stats.DBName), false)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() { once.Do(func() { tx.Rollback(); db.Close() }) }
	t.Cleanup(release)
	return release
}

// statsModes are the states stats.db can be in when a command runs; each
// sets one up in a store's dir. h is nil where the command builds its own
// Env.
var statsModes = []struct {
	name  string
	setup func(t *testing.T, dir string, h *harness)
}{
	{"off", func(t *testing.T, dir string, _ *harness) {
		prepareStatsAt(t, dir)
		stats.SetOff(dir, true)
	}},
	{"fresh", func(t *testing.T, dir string, _ *harness) { prepareStatsAt(t, dir) }},
	{"missing", func(*testing.T, string, *harness) {}},
	{"locked", func(t *testing.T, dir string, _ *harness) { prepareStatsAt(t, dir); holdStats(t, dir) }},
	{"corrupt", func(t *testing.T, dir string, _ *harness) {
		os.WriteFile(filepath.Join(dir, stats.DBName), bytes.Repeat([]byte("zqmark7 not a database\n"), 400), 0o644)
	}},
	{"newer", func(t *testing.T, dir string, _ *harness) {
		prepareStatsAt(t, dir)
		db, _ := stats.Open(filepath.Join(dir, stats.DBName), false)
		defer db.Close()
		if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
			t.Fatal(err)
		}
	}},
	{"broken", func(t *testing.T, dir string, h *harness) {
		prepareStatsAt(t, dir)
		if h != nil {
			h.statsOpen = func(string, bool) (*sql.DB, error) { return nil, errors.New("broken") }
		}
	}},
}

// statsMode sets dir's stats.db up as the mode of that name does.
func statsMode(t *testing.T, name, dir string, h *harness) {
	t.Helper()
	for _, m := range statsModes {
		if m.name == name {
			m.setup(t, dir, h)
			return
		}
	}
	t.Fatalf("no stats mode %q", name)
}

// Every command a bot or a session reads prints the same bytes whatever
// state stats.db is in: the C0 goldens, under each.
func TestStatsModesKeepOutput(t *testing.T) {
	if *update {
		t.Skip("-update writes the goldens from the plain runs only")
	}
	for _, mode := range statsModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			var ends []*harness // pickup's and doctor's, which end the died run
			setup := func(h *harness) { mode.setup(t, h.dir, h); ends = append(ends, h) }
			for name, got := range pinPickup(t, setup) {
				golden(t, name, got)
			}
			golden(t, "doctor", pinDoctor(t, setup))
			// The scene's died run started with stats on: into a fresh
			// stats.db its row goes.
			for _, h := range ends {
				if mode.name != "fresh" {
					break
				}
				if n := statsRowCount(t, h.dir); n != 1 {
					t.Errorf("%d rows; want the died run's", n)
				}
			}
			setup = func(h *harness) { mode.setup(t, h.dir, h) }
			golden(t, "runs", pinRuns(t, setup))
			golden(t, "ack", pinAck(t, setup))
			golden(t, "costs", pinCosts(t, setup))
			golden(t, "check", pinCheck(t, func(env map[string]string) {
				mode.setup(t, filepath.Dir(env["MAESTRO_TICKET_STORE"]), nil)
			}))
		})
	}
}

// codes holds exactly the Go lists, and other for each kind.
func TestCodesMatchGoLists(t *testing.T) {
	dir := t.TempDir()
	prepareStatsAt(t, dir)
	db, _ := stats.Open(filepath.Join(dir, stats.DBName), false)
	defer db.Close()
	rows, err := db.Query(`SELECT kind, name FROM codes ORDER BY kind, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var k, n string
		rows.Scan(&k, &n)
		got = append(got, k+"/"+n)
	}
	var want []string
	for k, names := range statsLists() {
		for _, n := range append(slices.Clone(names), stats.Other) {
			want = append(want, k+"/"+n)
		}
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("codes differ from the lists:\n got %d %v\nwant %d", len(got), got[:min(5, len(got))], len(want))
	}
	if d := len(slices.Compact(slices.Clone(want))); d != len(want) {
		t.Fatal("a list names one value twice")
	}
	// Every enum a row writes has its kind.
	for _, k := range []string{"role", "job", "model", "effort", "nudge", "state", "outcome", "reason", "subtype", "stop_reason", "stream", "os", "arch", "ring_err", "cost_src"} {
		if statsLists()[k] == nil {
			t.Errorf("no list for %s", k)
		}
	}
}

func TestRoleReduction(t *testing.T) {
	for in, want := range map[string]string{
		"chief-of-staff": "chief-of-staff", "planner": "planner", "senior-engineer": "senior-engineer",
		"junior-engineer": "junior-engineer", "qa-lead": "qa-lead", "reviewer": "reviewer", "designer": "designer",
		"Planner": "person", "planner ": "person", "paul": "person", "zqmark7": "person", "": "other",
	} {
		if got := statsRole(in); got != want {
			t.Errorf("statsRole(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestModelFamily(t *testing.T) {
	names := modelNames()
	for in, want := range map[string]string{
		"opus": "opus", "sonnet": "sonnet", "haiku": "haiku",
		"claude-opus-5-5": "opus-5.5", "claude-sonnet-5-5": "sonnet-5.5", "claude-sonnet-4-5-20250929": "sonnet-4.5",
		"claude-opus-4-20250514": "opus-4", "claude-opus-4-6[1m]": "opus-4.6", "claude-haiku-4-5-20251001": "haiku-4.5",
		"claude-3-5-sonnet-20241022": "sonnet-3.5", "claude-3-opus-20240229": "opus-3",
		"claude-acme-finetune-prod": "other", "claude-zqmark": "other", "<synthetic>": "other", "gpt-5": "other",
		"claude-opus-5-5-zqmark7": "other", "claude-opus-55": "other", "Claude-Opus-5-5": "other", "": "other",
		// Bedrock and Vertex ids.
		"us.anthropic.claude-opus-4-5-20251101-v1:0": "opus-4.5", "global.anthropic.claude-haiku-4-5-20251001-v1:0": "haiku-4.5",
		"anthropic.claude-3-5-sonnet-20241022-v2:0": "sonnet-3.5", "claude-opus-4-5@20251101": "opus-4.5",
		"claude-3-5-sonnet-v2@20241022": "sonnet-3.5", "claude-sonnet-4@20250514": "sonnet-4",
		"us.zqmark.claude-opus-4-5-v1:0": "other", "claude-opus-4-5@zqmark7": "other", "anthropic.gpt-5": "other",
	} {
		got := modelFamily(in)
		if got != want {
			t.Errorf("modelFamily(%q) = %q; want %q", in, got, want)
		}
		if got != stats.Other && !slices.Contains(names, got) {
			t.Errorf("modelFamily(%q) = %q, which isn't on the list", in, got)
		}
	}
}

// Model, effort and lean, read back from every command job builds.
func TestArgvFacts(t *testing.T) {
	for _, k := range jobKinds {
		models := []string{k.model}
		if k.model == "tier" || k.model == "route" {
			models = []string{modelOpus, modelSonnet}
		}
		for _, m := range models {
			cmd := claudeCommand(newSessionID(), m, effortHigh, k.lean)
			if k.slot != "" {
				cmd = inSlot(cmd, k.slot, 3)
			}
			for _, c := range [][]string{withEnv(cmd), resumeCommand(withEnv(cmd), newSessionID(), false)} {
				model, effort, lean := argvFacts(c)
				if model != m || effort != effortHigh || lean != k.lean {
					t.Errorf("%s on %s: argvFacts = %s %s %v; want %s high %v", k.name, m, model, effort, lean, m, k.lean)
				}
			}
			model, effort, _ := argvFacts(resumeCommand(withEnv(cmd), newSessionID(), true))
			if model != modelOpus || effort != effortHigh {
				t.Errorf("%s resumed --opus: %s %s", k.name, model, effort)
			}
		}
	}
	if m, e, l := argvFacts([]string{"claude", "-p", "--model=claude-zqmark", "--effort", "zqmark7"}); m != "other" || e != "zqmark7" || l {
		t.Errorf("a person's launch: %s %s %v", m, e, l) // effort goes to other at write, off its list
	}
	if m, e, _ := argvFacts([]string{"sleep", "1"}); m != "" || e != "" {
		t.Errorf("a plain launch: %q %q", m, e)
	}
}

// A result line as Claude Code 2.1.322 writes it: type is not its first
// key.
const r0Result = `{"duration_api_ms":4321,"duration_ms":5678.0,"is_error":false,"modelUsage":{"claude-sonnet-5-5":{"inputTokens":3,"outputTokens":40,"cacheReadInputTokens":30000,"cacheCreationInputTokens":7000,"webSearchRequests":0,"costUSD":0.05,"contextWindow":200000},"claude-haiku-4-5-20251001":{"inputTokens":100,"outputTokens":5,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.001},"claude-sonnet-5-5-20261101":{"inputTokens":1,"outputTokens":1,"costUSD":0.01}},"num_turns":3,"permission_denials":[],"result":"zqmark7 done","session_id":"11111111-1111-4111-8111-111111111111","stop_reason":"end_turn","subtype":"success","total_cost_usd":0.061,"type":"result","api_error_status":null,"usage":{}}`

func TestResultFacts(t *testing.T) {
	res, err := lastResult([]byte(r0Result))
	if err != nil {
		t.Fatal(err)
	}
	r := runs.Record{ID: "R-aaaaaaaa", State: runs.Done, Launcher: "reviewer", Job: "review", Exit: exitCode(0),
		StartedAt: time.Unix(1000, 0), EndedAt: ended(time.Unix(1000, 0), time.Minute), Reply: "zqmark7 done",
		Command: withEnv(claudeCommand("11111111-1111-4111-8111-111111111111", modelSonnet, effortHigh, false))}
	c := &call{env: Env{Getenv: func(string) string { return "" }, DiskFree: halfFree}}
	row := c.statsRow(t.TempDir(), r, res, nil)
	got := fmt.Sprintf("%s %s %s %s %s %v %d %d %d %s", row.Session, row.Subtype, row.StopReason, row.Outcome, row.Reason,
		*row.IsError, *row.ClaudeMs, *row.APIMs, *row.Turns, row.Model)
	if want := "11111111-1111-4111-8111-111111111111 success end_turn done  false 5678 4321 3 sonnet"; got != want {
		t.Fatalf("facts:\n got %s\nwant %s", got, want)
	}
	if *row.DiskFreePct != 50 || *row.InodeFreePct != 50 || row.ReplyBytes != 12 {
		t.Fatalf("disk %d inodes %d reply %d", *row.DiskFreePct, *row.InodeFreePct, row.ReplyBytes)
	}
	var fams []string
	for _, m := range row.Models {
		fams = append(fams, fmt.Sprintf("%s %d %d %d %d %.3f %s", m.Model, m.InputCum, m.OutputCum, m.CacheReadCum, m.CacheWriteCum, *m.CostCum, m.CostSrc))
	}
	slices.Sort(fams)
	want := []string{"haiku-4.5 100 5 0 0 0.001 result", "sonnet-5.5 1 1 0 0 0.010 result", "sonnet-5.5 3 40 30000 7000 0.050 result"}
	if !slices.Equal(fams, want) {
		t.Fatalf("models:\n got %q\nwant %q", fams, want)
	}
	// Written, two ids of one family make one row.
	dir := t.TempDir()
	prepareStatsAt(t, dir)
	db, _ := stats.Open(filepath.Join(dir, stats.DBName), false)
	defer db.Close()
	if err := stats.Write(t.Context(), db, row); err != nil {
		t.Fatal(err)
	}
	var n int
	var in int64
	var cost float64
	db.QueryRow(`SELECT count(*), sum(input_cum), sum(cost_cum) FROM run_models`).Scan(&n, &in, &cost)
	if n != 2 || in != 104 || fmt.Sprintf("%.3f", cost) != "0.061" {
		t.Fatalf("run_models: %d rows, input %d, cost %v", n, in, cost)
	}
	// A field whose type changes costs nothing: the reply still logs.
	odd := strings.NewReplacer(`"duration_ms":5678.0`, `"duration_ms":"soon"`, `"num_turns":3`, `"num_turns":null`,
		`"is_error":false`, `"is_error":"no"`, `"subtype":"success"`, `"subtype":{"zqmark7":1}`,
		`"stop_reason":"end_turn"`, `"stop_reason":"`+strings.Repeat("x", 100)+`"`, `"costUSD":0.05`, `"costUSD":[1]`,
		`"api_error_status":null`, `"api_error_status":529`).Replace(r0Result)
	res, err = lastResult([]byte(odd))
	if err != nil || *res.TotalCostUSD != 0.061 {
		t.Fatalf("an odd result: %v", err)
	}
	row = c.statsRow(t.TempDir(), r, res, nil)
	if row.ClaudeMs != nil || row.Turns != nil || row.IsError != nil || row.Subtype != "" || row.StopReason != "" {
		t.Fatalf("odd fields read as facts: %+v", row)
	}
	if !res.APIErrorStatus.Set {
		t.Fatal("api_error_status 529 not seen")
	}
	r.State, r.Exit = runs.Failed, exitCode(1)
	if got := reasonOf(r, outFailed, res, nil); got != "api_error" {
		t.Fatalf("reason %q; want api_error", got)
	}
}

// A run with no result has no session id from Claude; its row takes the
// one its command names, so its spend sits in its session.
func TestDeadRunKeepsSession(t *testing.T) {
	const s1 = "11111111-1111-4111-8111-111111111111"
	c := &call{env: Env{Getenv: func(string) string { return "" }, DiskFree: halfFree}}
	for _, cmd := range [][]string{
		claudeCommand(s1, modelSonnet, effortHigh, false),
		resumeCommand(claudeCommand("22222222-2222-4222-8222-222222222222", modelSonnet, effortHigh, false), s1, false),
	} {
		r := runs.Record{ID: "R-aaaaaaaa", State: runs.TimedOut, Launcher: "planner", Job: "build",
			StartedAt: time.Unix(1000, 0), EndedAt: ended(time.Unix(1000, 0), time.Minute), Command: withEnv(cmd)}
		if got := c.statsRow(t.TempDir(), r, claudeReply{}, errors.New("no result")).Session; got != s1 {
			t.Errorf("%q: session %q; want %s", cmd, got, s1)
		}
	}
}

func TestReasons(t *testing.T) {
	ok := claudeReply{}
	for _, c := range []struct {
		r       runs.Record
		outcome string
		res     claudeReply
		resErr  error
		want    string
	}{
		{runs.Record{State: runs.Died}, outDied, ok, errors.New("x"), "supervisor"},
		{runs.Record{State: runs.TimedOut}, outTimedOut, ok, errors.New("x"), "limit"},
		{runs.Record{State: runs.TimedOut, Frozen: true}, outTimedOut, ok, nil, "frozen"},
		{runs.Record{State: runs.Failed, Exit: exitCode(143), End: &runs.EndFacts{Cause: runs.CauseAsked}}, outFailed, ok, nil, "asked"},
		{runs.Record{State: runs.Failed, End: &runs.EndFacts{Cause: runs.CauseLauncher}}, outFailed, ok, errors.New("x"), "stopped"},
		{runs.Record{State: runs.Failed, Exit: exitCode(1)}, outFailed, ok, errors.New("x"), "no_result"},
		{runs.Record{State: runs.Done}, outFailed, claudeReply{Subtype: looseStr{"error_max_turns"}}, nil, "max_turns"},
		{runs.Record{State: runs.Done}, outFailed, claudeReply{Subtype: looseStr{"error_max_budget_usd"}}, nil, "budget"},
		{runs.Record{State: runs.Done, IsError: true}, outFailed, claudeReply{IsError: looseBool{true, true}}, nil, "is_error"},
		{runs.Record{State: runs.Failed, Exit: exitCode(2)}, outFailed, ok, nil, "exit_nonzero"},
		{runs.Record{State: runs.Done}, outDone, ok, nil, ""},
	} {
		if got := reasonOf(c.r, c.outcome, c.res, c.resErr); got != c.want {
			t.Errorf("%+v: reason %q; want %q", c.r, got, c.want)
		}
	}
}

// Versions go in only on their shapes.
func TestVersionShapes(t *testing.T) {
	dir := t.TempDir()
	prepareStatsAt(t, dir)
	db, _ := stats.Open(filepath.Join(dir, stats.DBName), false)
	defer db.Close()
	for i, c := range []struct {
		tool, maestro, claude string
		want                  string
	}{
		{strings.Repeat("ab", 20), "abc1234", "2.1.322", strings.Repeat("ab", 20) + " abc1234 2.1.322"},
		{"deadbeefcafe", strings.Repeat("a", 41), "2.1.322-beta", "- - -"},
		{strings.Repeat("AB", 20), "ZQMARK7", "v2.1", "- - -"},
		{"", "", "1.2", "- - -"},
	} {
		id := fmt.Sprintf("R-aaaaaaa%c", 'a'+i)
		if err := stats.Write(t.Context(), db, stats.Run{Run: id, VTool: c.tool, VMaestro: c.maestro, VClaude: c.claude}); err != nil {
			t.Fatal(err)
		}
		var a, b, v sql.NullString
		db.QueryRow(`SELECT v_tool, v_maestro, v_claude FROM run_facts WHERE run = ?`, id).Scan(&a, &b, &v)
		show := func(s sql.NullString) string { return map[bool]string{true: s.String, false: "-"}[s.Valid] }
		if got := show(a) + " " + show(b) + " " + show(v); got != c.want {
			t.Errorf("%+v: stored %s; want %s", c, got, c.want)
		}
	}
}

// orphanScene is a store with one run whose supervisor died, for the
// actor senior-engineer: pickup and doctor end it, as its supervisor
// would have.
func orphanScene(t *testing.T) (*harness, string) {
	h, _ := pinHarness(t, "senior-engineer")
	h.ok(nil, "create", "--title", "a")
	h.ok(nil, "transition", "T-1", "--to", "in_progress")
	r := putRun(t, filepath.Join(h.dir, "runs"), runs.Record{Launcher: "senior-engineer", Ticket: "T-1", Job: "build",
		Command: withEnv(claudeCommand(newSessionID(), modelOpus, effortHigh, false)), Dir: h.dir, Limit: "2h",
		StartedAt: pinBase(), State: runs.Running, PID: 4005})
	// As a start with stats on writes it.
	writeSide(filepath.Join(h.dir, "runs"), r.ID, &sideFacts{Why: "tier_senior", Tier: "senior", Tries: ptr(0)})
	return h, r.ID
}

// pickup and doctor write a died run's row only into a stats.db a
// supervisor prepared: they never make one, nor set one up.
func TestOrphanPathNeverCreates(t *testing.T) {
	for _, cmd := range []string{"pickup", "doctor"} {
		h, _ := orphanScene(t)
		h.run("", cmd)
		ents, _ := filepath.Glob(filepath.Join(h.dir, "stats*"))
		if len(ents) != 0 {
			t.Fatalf("%s made %v", cmd, ents)
		}
		h, _ = orphanScene(t)
		os.WriteFile(filepath.Join(h.dir, stats.DBName), nil, 0o644)
		h.run("", cmd)
		ents, _ = filepath.Glob(filepath.Join(h.dir, "stats*"))
		if fi, err := os.Stat(filepath.Join(h.dir, stats.DBName)); err != nil || fi.Size() != 0 || len(ents) != 1 {
			t.Fatalf("%s set up an empty stats.db: %v", cmd, ents)
		}
	}
	// One at a newer schema stays as it was.
	h, _ := orphanScene(t)
	statsMode(t, "newer", h.dir, h)
	h.run("", "pickup")
	if n := statsRowCount(t, h.dir); n != 0 {
		t.Fatalf("%d rows in a newer stats.db", n)
	}
}

// Into a prepared one, the died run's row goes, once.
func TestOrphanRow(t *testing.T) {
	h, id := orphanScene(t)
	prepareStatsAt(t, h.dir)
	h.run("", "pickup")
	h.run("", "pickup")
	h.run("", "doctor")
	var n int
	var outcome, reason string
	statsQuery(t, h.dir, `SELECT count(*), o.name, r.name FROM run_facts f JOIN codes o ON o.id = f.outcome
		JOIN codes r ON r.id = f.reason WHERE f.run = '`+id+`'`, &n, &outcome, &reason)
	if n != 1 || outcome != "died" || reason != "supervisor" || statsRowCount(t, h.dir) != 1 {
		t.Fatalf("%d rows, outcome %s, reason %s", n, outcome, reason)
	}
}

// A locked stats.db holds pickup up a quarter second at most, and the
// row is dropped: against the same pickup with no lock, it takes at most
// orphanWait more, give or take a scheduler's slack.
func TestOrphanBusyDrops(t *testing.T) {
	h2, _ := orphanScene(t)
	prepareStatsAt(t, h2.dir)
	begun := time.Now()
	_, out2, _ := h2.run("", "pickup")
	base := time.Since(begun)

	h, _ := orphanScene(t)
	prepareStatsAt(t, h.dir)
	release := holdStats(t, h.dir)
	begun = time.Now()
	code, out, errs := h.run("", "pickup")
	took := time.Since(begun)
	if code != 0 || errs != "" || !strings.Contains(out, "build died") {
		t.Fatalf("pickup: exit %d\n%s\n%s", code, out, errs)
	}
	if took > base+orphanWait+300*time.Millisecond {
		t.Fatalf("pickup took %s behind a locked stats.db, %s with none", took, base)
	}
	m := newMasker()
	if m.mask(out) != newMasker().mask(out2) {
		t.Fatalf("pickup behind a lock printed\n%s\nnot\n%s", out, out2)
	}
	release()
	if n := statsRowCount(t, h.dir); n != 0 {
		t.Fatalf("%d rows written through the lock", n)
	}
}

func TestStatsOffUnsafeStore(t *testing.T) {
	for _, c := range []struct {
		name   string
		checks func(string, bool) []ticket.Check
		env    string
		rows   int
	}{
		{"safe", fixedChecks, "", 1},
		{"allowed", func(string, bool) []ticket.Check {
			return []ticket.Check{{Name: "filesystem", OK: true, Detail: "allowed by " + envcheck.AllowEnv + ": nfs"}}
		}, "", 0},
		{"allow env", fixedChecks, "1", 0},
	} {
		h, _ := orphanScene(t)
		prepareStatsAt(t, h.dir)
		h.checks = c.checks
		if c.env != "" {
			h.env[envcheck.AllowEnv] = c.env
		}
		h.run("", "pickup")
		if n := statsRowCount(t, h.dir); n != c.rows {
			t.Errorf("%s: %d rows; want %d", c.name, n, c.rows)
		}
	}
	h, _ := orphanScene(t)
	prepareStatsAt(t, h.dir)
	h.env[stats.OffEnv] = "off"
	h.run("", "pickup")
	if n := statsRowCount(t, h.dir); n != 0 {
		t.Errorf("%s=off: %d rows", stats.OffEnv, n)
	}
}

// stats on and off: the marker, the sha, and the refusals.
func TestStatsOnOff(t *testing.T) {
	h, _ := pinHarness(t, "")
	marker := filepath.Join(h.dir, stats.OffFile)
	for _, c := range []struct {
		args   []string
		code   int
		marker bool
	}{
		{[]string{"stats", "off"}, 0, true},
		{[]string{"stats", "off"}, 0, true},
		{[]string{"stats", "on", "--maestro-sha", "abc1234"}, 0, false},
		{[]string{"stats", "off", "--maestro-sha", "abc12345"}, 0, true},
		{[]string{"stats", "maybe"}, 2, true},
		{[]string{"stats", "on", "--maestro-sha", "zqmark7"}, 2, true},
		{[]string{"stats", "on", "off"}, 2, true},
		// The sha alone, as apply with no stats: line runs it, leaves
		// the marker as it is; with no sha it is no switch.
		{[]string{"stats", "--maestro-sha", "abc123456"}, 0, true},
		{[]string{"stats", "on"}, 0, false},
		{[]string{"stats", "--maestro-sha", "abc12345"}, 0, false},
		{[]string{"stats", "--json"}, 2, false},
	} {
		code, out, errs := h.run("", c.args...)
		_, err := os.Stat(marker)
		if code != c.code || (err == nil) != c.marker || (code == 0 && out+errs != "") {
			t.Errorf("%v: exit %d, marker %v, printed %q", c.args, code, err == nil, out+errs)
		}
	}
	if got := stats.Sha(h.dir); got != "abc12345" {
		t.Errorf("stats.sha %q", got)
	}
}

// A died run that asked for a stream lost it only if its command
// started: one that never did had none to lose.
func TestStreamLostNeedsAStart(t *testing.T) {
	for pid, want := range map[int]string{0: "off", 4242: "lost"} {
		row := stats.Run{Stream: "off"}
		streamFacts(&row, runs.Record{State: runs.Died, PID: pid}, sideFacts{Stream: true}, true, false)
		if row.Stream != want {
			t.Errorf("pid %d: stream %s; want %s", pid, row.Stream, want)
		}
	}
}

// The stats*.go files of cli import no network or exec package
// themselves. This checks direct imports only: they share package cli,
// which imports both. stats.TestStatsDeps guards the stats package.
func TestStatsCLIImports(t *testing.T) {
	files, _ := filepath.Glob("stats*.go")
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		seen++
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			if p == "net" || strings.HasPrefix(p, "net/") || p == "os/exec" {
				t.Errorf("%s imports %s", f, p)
			}
		}
	}
	if seen < 2 {
		t.Fatalf("read %d files", seen)
	}
}

// A run started with stats off gets no row from its supervisor either,
// though they are on again at its end; one that ends with them off loses
// its side file, so stats health counts no row missing.
func TestStatsOffAtStartOrEnd(t *testing.T) {
	h, _ := pinHarness(t, "")
	prepareStatsAt(t, h.dir)
	dir := filepath.Join(h.dir, "runs")
	c := &call{env: Env{Getenv: func(string) string { return "" }, DiskFree: halfFree}, inSupervisor: true}
	end := func(side bool) runs.Record {
		r := putRun(t, dir, runs.Record{Job: "build", Command: []string{"claude", "-p"}, Dir: h.dir, Limit: "1h",
			StartedAt: pinBase(), EndedAt: ended(pinBase(), time.Minute), State: runs.Done, PID: 4001, Exit: exitCode(0)})
		if side {
			writeSide(dir, r.ID, &sideFacts{})
		}
		return r
	}
	c.writeStats(dir, end(false), claudeReply{}, os.ErrNotExist)
	c.waitStats()
	if n := statsRowCount(t, h.dir); n != 0 {
		t.Fatalf("%d rows of a run started with stats off", n)
	}
	r := end(true)
	stats.SetOff(h.dir, true)
	c.writeStats(dir, r, claudeReply{}, os.ErrNotExist)
	c.waitStats()
	stats.SetOff(h.dir, false)
	if _, err := os.Lstat(filepath.Join(dir, r.ID+sideExt)); err == nil {
		t.Fatal("a run that ended with stats off kept its side file")
	}
	c.writeStats(dir, end(true), claudeReply{}, os.ErrNotExist)
	c.waitStats()
	var hl statsHealth
	h.ok(&hl, "stats", "health")
	if statsRowCount(t, h.dir) != 1 || hl.Ended != 1 || hl.Missing != 0 {
		t.Fatalf("%d rows, health %+v; want the one run on all through", statsRowCount(t, h.dir), hl)
	}
}

// A died run's row ends when it last wrote, not when a reader found it.
func TestDiedRunEndsAtLastOutput(t *testing.T) {
	h, id := orphanScene(t)
	prepareStatsAt(t, h.dir)
	dir := filepath.Join(h.dir, "runs")
	last := pinBase().Add(10 * time.Minute)
	os.WriteFile(filepath.Join(dir, id+".err"), []byte("x\n"), 0o600)
	os.Chtimes(filepath.Join(dir, id+".err"), last, last)
	h.run("", "pickup")
	var wall, endedAt int64
	statsQuery(t, h.dir, `SELECT wall_ms, ended_at FROM run_facts WHERE run = '`+id+`'`, &wall, &endedAt)
	if wall != (10*time.Minute).Milliseconds() || endedAt != last.UnixMilli() {
		t.Fatalf("wall %dms, ended %d; want 10m, %d", wall, endedAt, last.UnixMilli())
	}
}

// A died review whose result its .out holds gets that result's head in
// its row, as its supervisor would have given it.
func TestOrphanReviewFacts(t *testing.T) {
	h, _ := pinHarness(t, "senior-engineer")
	h.ok(nil, "create", "--title", "a")
	h.ok(nil, "transition", "T-1", "--to", "in_progress")
	h.ok(nil, "transition", "T-1", "--to", "in_review")
	prepareStatsAt(t, h.dir)
	dir := filepath.Join(h.dir, "runs")
	r := putRun(t, dir, runs.Record{Launcher: "reviewer", Ticket: "T-1", Job: "review",
		Command: withEnv(claudeCommand(newSessionID(), modelOpus, effortHigh, false)), Dir: h.dir, Limit: "2h",
		StartedAt: pinBase(), State: runs.Running, PID: 4005})
	writeSide(dir, r.ID, &sideFacts{})
	res := `{"type":"result","subtype":"success","is_error":false,"result":"Review: GO at ` + strings.Repeat("ab", 20) +
		` (PR #4), tier 1","session_id":"11111111-2222-4333-8444-555555555555","total_cost_usd":0.5,"modelUsage":{}}`
	os.WriteFile(filepath.Join(dir, r.ID+".out"), []byte(res+"\n"), 0o600)
	h.run("", "pickup")
	var head string
	statsQuery(t, h.dir, `SELECT c.name FROM run_facts f JOIN codes c ON c.id = f.head WHERE f.run = '`+r.ID+`'`, &head)
	if head != "review:go" {
		t.Fatalf("head %s; want review:go", head)
	}
}

// reply_bytes is the whole result's size, not the 64 KB the record keeps.
func TestReplyBytesUncut(t *testing.T) {
	text := strings.Repeat("x", 100_000)
	res, err := lastResult([]byte(`{"type":"result","result":"` + text + `","session_id":"s","total_cost_usd":0}`))
	if err != nil {
		t.Fatal(err)
	}
	reply, _, _ := runs.ParseOutput([]byte(`{"type":"result","result":"` + text + `","session_id":"s"}`))
	c := &call{env: Env{Getenv: func(string) string { return "" }, DiskFree: halfFree}}
	row := c.statsRow(t.TempDir(), runs.Record{ID: "R-aaaaaaaa", State: runs.Done, Reply: reply}, res, nil)
	if len(reply) >= len(text) || row.ReplyBytes != int64(len(text)) {
		t.Fatalf("record keeps %d bytes, row says %d; want %d", len(reply), row.ReplyBytes, len(text))
	}
}

// A stream that showed no compaction or retry leaves both unknown: no
// probe has seen their events. A plain launch of claude --resume is a
// resume.
func TestStreamCountsUnseen(t *testing.T) {
	row := stats.Run{}
	streamFacts(&row, runs.Record{Stream: &streamfilter.Summary{Result: true}}, sideFacts{}, false, true)
	if row.Compactions != nil || row.Retries != nil {
		t.Fatalf("none seen stored as %v, %v", row.Compactions, row.Retries)
	}
	row = stats.Run{}
	streamFacts(&row, runs.Record{Stream: &streamfilter.Summary{Result: true, Compactions: 2, Retries: 1}}, sideFacts{}, false, true)
	if row.Compactions == nil || *row.Compactions != 2 || row.Retries == nil || *row.Retries != 1 {
		t.Fatalf("two compactions and a retry stored as %v, %v", row.Compactions, row.Retries)
	}
	c := &call{env: Env{Getenv: func(string) string { return "" }, DiskFree: halfFree}}
	for cmd, want := range map[string]bool{"claude -p --resume S": true, "claude -p --continue": true, "claude -p --session-id S": false} {
		if got := c.statsRow(t.TempDir(), runs.Record{ID: "R-aaaaaaaa", Command: strings.Fields(cmd)}, claudeReply{}, os.ErrNotExist).Resumed; got != want {
			t.Errorf("%s: resumed %v", cmd, got)
		}
	}
}
