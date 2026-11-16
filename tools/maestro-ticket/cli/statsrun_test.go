//go:build unix

package cli

import (
	"bytes"
	"database/sql"
	"encoding/json"
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

// Every command a bot or a session reads prints the same bytes whatever
// state stats.db is in: the C0 goldens, under each.
func TestStatsModesKeepOutput(t *testing.T) {
	if *update {
		t.Skip("-update writes the goldens from the plain runs only")
	}
	for _, mode := range statsModes {
		t.Run(mode.name, func(t *testing.T) {
			setup := func(h *harness) { mode.setup(t, h.dir, h) }
			for name, got := range pinPickup(t, setup) {
				golden(t, name, got)
			}
			golden(t, "doctor", pinDoctor(t, setup))
			golden(t, "runs", pinRuns(t, setup))
			golden(t, "ack", pinAck(t, setup))
			golden(t, "costs", pinCosts(t, setup))
			golden(t, "check", pinCheck(t, func(env map[string]string) {
				mode.setup(t, filepath.Dir(env["MAESTRO_TICKET_STORE"]), nil)
			}))
		})
	}
}

// job, again, resume and look, whose runs' supervisors write stats.db,
// print the same with stats off and with a stats.db that isn't one.
func TestStatsModesKeepJobOutput(t *testing.T) {
	if *update {
		t.Skip("-update writes the goldens from the plain runs only")
	}
	for _, mode := range []struct {
		name  string
		setup func(j *jobHarness)
		check func(t *testing.T, dir string)
	}{
		{"off", func(j *jobHarness) { stats.SetOff(j.h.dir, true) }, func(t *testing.T, dir string) {
			if _, err := os.Stat(filepath.Join(dir, stats.DBName)); err == nil {
				t.Error("stats off, and a supervisor made stats.db")
			}
		}},
		{"broken", func(j *jobHarness) {
			os.WriteFile(filepath.Join(j.h.dir, stats.DBName), []byte("zqmark7 not a database\n"), 0o644)
		}, func(t *testing.T, dir string) {
			if b, _ := os.ReadFile(filepath.Join(dir, stats.DBName)); string(b) != "zqmark7 not a database\n" {
				t.Errorf("a supervisor changed a broken stats.db: %q", b)
			}
		}},
	} {
		for name, pin := range map[string]func(*testing.T, func(*jobHarness)) string{
			"job_start": pinJobStart, "job_again": pinJobAgain, "resume": pinResume, "look": pinLook,
		} {
			t.Run(mode.name+"/"+name, func(t *testing.T) {
				t.Parallel()
				var dirs []*jobHarness
				golden(t, name, pin(t, func(j *jobHarness) { mode.setup(j); dirs = append(dirs, j) }))
				for _, j := range dirs {
					j.waitSupervisors()
					mode.check(t, j.h.dir)
				}
			})
		}
	}
}

// The first run's end on a store makes stats.db, writes its row, and
// claims the first prune after its ring.
func TestPrepareFresh(t *testing.T) {
	t.Parallel()
	j := newJobHarness(t, "senior-engineer")
	j.h.ok(nil, "stats", "on", "--maestro-sha", "abc1234")
	j.h.ok(nil, "create", "--title", "a")
	if _, err := os.Stat(filepath.Join(j.h.dir, stats.DBName)); err == nil {
		t.Fatal("stats on made stats.db: only a supervisor does")
	}
	s, _ := j.job("", "build", "T-1")
	j.waitSupervisors()
	var job, role, model, effort, outcome, state, subtype, ringErr, vMaestro string
	var lean, rang, ticketN, turns int
	var lastPrune int64
	statsQuery(t, j.h.dir, `SELECT j.name, r.name, m.name, e.name, o.name, s.name, st.name, re.name, f.v_maestro, f.lean, f.rang, f.ticket, coalesce(f.turns, -1)
		FROM run_facts f JOIN codes j ON j.id = f.job JOIN codes r ON r.id = f.role JOIN codes m ON m.id = f.model
		JOIN codes e ON e.id = f.effort JOIN codes o ON o.id = f.outcome JOIN codes s ON s.id = f.state
		JOIN codes st ON st.id = f.subtype JOIN codes re ON re.id = f.ring_err WHERE f.run = '`+s.Run.ID+`'`,
		&job, &role, &model, &effort, &outcome, &state, &subtype, &ringErr, &vMaestro, &lean, &rang, &ticketN, &turns)
	got := fmt.Sprintf("%s %s %s %s %s %s %s %s %s lean %d rang %d ticket %d turns %d", job, role, model, effort, outcome, state, subtype, ringErr, vMaestro, lean, rang, ticketN, turns)
	if want := "build senior-engineer opus high done done success none abc1234 lean 0 rang 0 ticket 1 turns -1"; got != want {
		t.Fatalf("row:\n got %s\nwant %s", got, want)
	}
	statsQuery(t, j.h.dir, `SELECT value FROM meta WHERE key = 1`, &lastPrune)
	if lastPrune == 0 {
		t.Fatal("the first run's supervisor didn't claim the first prune")
	}
	var v, av int
	statsQuery(t, j.h.dir, `PRAGMA user_version`, &v)
	statsQuery(t, j.h.dir, `PRAGMA auto_vacuum`, &av)
	if v != stats.Version() || av != 2 {
		t.Fatalf("user_version %d, auto_vacuum %d", v, av)
	}
}

// A store a run's end found with no schema yet (a file but no tables)
// gets one in the same end, from the supervisor.
func TestSupervisorPreparesAtEnd(t *testing.T) {
	t.Parallel()
	j := newJobHarness(t, "senior-engineer")
	j.h.ok(nil, "create", "--title", "a")
	os.WriteFile(filepath.Join(j.h.dir, stats.DBName), nil, 0o644)
	s, _ := j.job("", "build", "T-1")
	j.waitSupervisors()
	var n int
	statsQuery(t, j.h.dir, `SELECT count(*) FROM run_facts WHERE run = '`+s.Run.ID+`'`, &n)
	if n != 1 {
		t.Fatal("no row")
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
		{runs.Record{State: runs.Failed, Error: "the launcher was stopped, so it stopped the run\nzqmark7"}, outFailed, ok, nil, "stopped"},
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
	if v := vTool(); v != "" && !sha40.MatchString(v) {
		t.Errorf("vTool %q", v)
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
	statsModes[5].setup(t, h.dir, h)
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
// row is dropped.
func TestOrphanBusyDrops(t *testing.T) {
	h, _ := orphanScene(t)
	prepareStatsAt(t, h.dir)
	release := holdStats(t, h.dir)
	begun := time.Now()
	code, out, errs := h.run("", "pickup")
	took := time.Since(begun)
	if code != 0 || errs != "" || !strings.Contains(out, "build died") {
		t.Fatalf("pickup: exit %d\n%s\n%s", code, out, errs)
	}
	if took > orphanWait+750*time.Millisecond {
		t.Fatalf("pickup took %s behind a locked stats.db", took)
	}
	h2, _ := orphanScene(t)
	_, out2, _ := h2.run("", "pickup")
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
		{[]string{"stats"}, 2, true},
		{[]string{"stats", "on", "--maestro-sha", "zqmark7"}, 2, true},
		{[]string{"stats", "on", "off"}, 2, true},
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

// Markers planted everywhere text lives miss stats.db and its WAL, before
// and after a checkpoint and VACUUM.
func TestMarkersMissStatsDB(t *testing.T) {
	t.Parallel()
	markers := []string{"zqmark7", "deadbeefcafe", strings.Repeat("d3adb33f", 5), "claude-zqmark", "cafe0123beef"}
	all := strings.Join(markers, " ")
	l := newLauncher(t, "zqmark7")
	l.h.checks = fixedChecks
	prepareStatsAt(t, l.h.dir)
	l.h.ok(nil, "create", "--title", all, "--body", all)
	l.h.run(all, "comment", "T-1", "--body-file", "-")
	sub := filepath.Join(l.h.dir, "zqmark7-deadbeefcafe")
	os.MkdirAll(sub, 0o755)
	res, _ := json.Marshal(map[string]any{
		"type": "result", "subtype": "zqmark7", "stop_reason": "deadbeefcafe", "is_error": false, "result": all,
		"session_id": "11111111-1111-4111-8111-111111111111", "total_cost_usd": 1,
		"modelUsage": map[string]any{"claude-zqmark": map[string]any{"inputTokens": 1, "costUSD": 1}, "zqmark7": map[string]any{"outputTokens": 2}},
	})
	script := fmt.Sprintf("cat >/dev/null; echo %q >&2; printf '%%s\\n' %s", all, strconv.Quote(string(res)))
	c := l.cmd(all, "launch", "--json", "--ticket", "T-1", "--", "env", "ANTHROPIC_MODEL=claude-zqmark", "sh", "-c", script,
		"--model", "claude-zqmark", "--effort", "zqmark7")
	c.Dir = sub
	out, err := c.Output()
	if err != nil {
		t.Fatalf("launch: %v %s", err, out)
	}
	var r runs.Record
	json.Unmarshal(out, &r)
	l.waitEnded(r.ID, 20*time.Second)
	l.waitSupervisors()
	// And a run whose supervisor died, ended by pickup.
	putRun(t, filepath.Join(l.h.dir, "runs"), runs.Record{Launcher: "zqmark7", Ticket: "T-1", Job: "build",
		Command: []string{"claude", "-p", "--model", "claude-zqmark", all}, Dir: sub, Limit: "2h", Host: "zqmark7-host",
		StartedAt: pinBase(), State: runs.Running, PID: 4005, Reply: all, Error: all, SessionID: "deadbeefcafe"})
	l.h.env["MAESTRO_TICKET_ACTOR"] = "zqmark7"
	l.h.run("", "pickup")
	if n := statsRowCount(t, l.h.dir); n != 2 {
		t.Fatalf("%d rows; want the launch's and the orphan's", n)
	}
	grep := func(when string) {
		for _, f := range []string{stats.DBName, stats.DBName + "-wal"} {
			b, _ := os.ReadFile(filepath.Join(l.h.dir, f))
			for _, m := range markers {
				if bytes.Contains(b, []byte(m)) {
					t.Errorf("%s: %s holds %q", when, f, m)
				}
			}
		}
	}
	grep("before a checkpoint")
	db, _ := stats.Open(filepath.Join(l.h.dir, stats.DBName), false)
	defer db.Close()
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`VACUUM`); err != nil {
		t.Fatal(err)
	}
	grep("after a checkpoint and VACUUM")
}

// Forty runs end over three seconds, each supervisor writing its row:
// every row lands and no launch prints. MAESTRO_STATS_LOAD=1 runs 300.
// Supervisors write stderr to /dev/null, so only launch's is checked
// here; that statsrun.go never prints rests on writeStats and afterRing,
// which take no writer.
func TestStatsConcurrentRunEnds(t *testing.T) {
	t.Parallel()
	n := 40
	if os.Getenv("MAESTRO_STATS_LOAD") == "1" {
		n = 300
	}
	l := newLauncher(t, "planner")
	prepareStatsAt(t, l.h.dir)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var bad []string
	begun := time.Now()
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i) * 3 * time.Second / time.Duration(n))
			c := l.cmd("", "launch", "--json", "--", "sh", "-c", fmt.Sprintf("sleep 0.%d", i%5))
			var out, errb bytes.Buffer
			c.Stdout, c.Stderr = &out, &errb
			if err := c.Run(); err != nil || errb.Len() > 0 {
				mu.Lock()
				bad = append(bad, fmt.Sprintf("%v %s", err, errb.String()))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(bad) > 0 {
		t.Fatalf("%d launches failed or printed on stderr: %s", len(bad), bad[0])
	}
	deadline := time.Now().Add(60 * time.Second)
	for statsRowCount(t, l.h.dir) < n && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if got := statsRowCount(t, l.h.dir); got != n {
		t.Fatalf("%d rows of %d runs", got, n)
	}
	t.Logf("%d run ends, every row in %s", n, time.Since(begun).Round(time.Millisecond))
}

// Stats code in cli reaches no network and runs nothing.
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
