//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
)

// statsPut writes rows into dir's stats.db, preparing it first.
func statsPut(t *testing.T, dir string, rows ...stats.Run) {
	t.Helper()
	prepareStatsAt(t, dir)
	db, err := stats.Open(filepath.Join(dir, stats.DBName), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, r := range rows {
		if err := stats.Write(t.Context(), db, r); err != nil {
			t.Fatalf("write %s: %v", r.Run, err)
		}
	}
}

const (
	caseS1 = "11111111-1111-4111-8111-111111111111"
	caseS2 = "22222222-2222-4222-8222-222222222222"
	caseS9 = "99999999-9999-4999-8999-999999999999"
)

// statsRow is a run row as a supervisor writes one: started at start,
// ended a minute on, its models' session totals in dollars.
func statsRow(id string, ticket int64, job string, start time.Time, session string, models ...stats.Model) stats.Run {
	return stats.Run{Run: id, Session: session, Ticket: ticket, Role: "senior-engineer", Job: job, Model: "opus",
		Effort: "high", State: runs.Done, Outcome: outDone, Stream: "ok", StartedAt: start, EndedAt: start.Add(time.Minute),
		Models: models}
}

func usdModel(model string, usd float64, tokens int64) stats.Model {
	return stats.Model{Model: model, InputCum: tokens, CostCum: &usd, CostSrc: "result"}
}

// caseRows are one ticket's build, its review, the build's resume, and a
// run on another ticket.
func caseRows(base time.Time) []stats.Run {
	build := statsRow("R-aaaaaaab", 1, "build", base, caseS1, usdModel("opus-5.5", 1.8, 1000), usdModel("haiku-4.5", 0.2, 100))
	build.Why, build.Tier, build.Tries = "tier_senior", "senior", ptr(int64(0))
	build.Turns, build.ToolCalls, build.ToolFails = ptr(int64(38)), ptr(int64(120)), ptr(int64(3))
	build.Tail = []stats.TailCall{{Code: "Bash:go"}, {Code: "Edit", Fail: true}}
	review := statsRow("R-aaaaaaac", 1, "review", base.Add(10*time.Minute), caseS2, usdModel("opus-5.5", 0.5, 400))
	review.Why, review.Verdict, review.Head = "route_full", "nogo", "review:nogo"
	review.Findings = []stats.Finding{{Fid: "F1", Kind: "finding", Sev: "blocker", Prior: "new", Pass: ptr(int64(2))},
		{Fid: "F2", Kind: "finding", Sev: "major", Prior: "new"}, {Kind: "follow_up", N: 2}}
	resume := statsRow("R-aaaaaaad", 1, "build", base.Add(20*time.Minute), caseS1, usdModel("opus-5.5", 3.0, 1600), usdModel("haiku-4.5", 0.3, 150))
	resume.ResumeOf, resume.Nudge, resume.Why = "R-aaaaaaab", "opus", "resume"
	resume.Outcome, resume.Reason, resume.OpenCode, resume.OpenMs = outTimedOut, "limit", "Bash:go", ptr(int64(1140000))
	other := statsRow("R-aaaaaaae", 2, "qa", base.Add(30*time.Minute), "", usdModel("sonnet-5.5", 0.4, 300))
	other.Lean = true
	return []stats.Run{build, review, resume, other}
}

func TestStatsCase(t *testing.T) {
	h, _ := pinHarness(t, "")
	base := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	statsPut(t, h.dir, caseRows(base)...)
	code, out, errs := h.run("", "stats", "case", "T-1")
	if code != 0 || errs != "" {
		t.Fatalf("exit %d: %s%s", code, out, errs)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	// 2.00 + 0.50 + (3.30 - 2.00): the resume counts what it added.
	if len(lines) != 4 || lines[0] != "T-1: 3 runs, $3.80" {
		t.Fatalf("case T-1:\n%s", out)
	}
	for i, want := range [][]string{
		{"R-aaaaaaab", "Nov 15 09:00", "build", "opus high", "tier_senior", "done", "1m", "$2.00", "turns 38",
			"tools 120, 3 failed", "tail Bash:go Edit!"},
		{"R-aaaaaaac", "review", "route_full", "$0.50", "verdict nogo", "F1 blocker new, F2 major new, 2 follow_up"},
		{"R-aaaaaaad", "resume", "timed_out", "$1.30", "reason limit", "resume of R-aaaaaaab (opus)", "open Bash:go 19m"},
	} {
		for _, w := range want {
			if !strings.Contains(lines[i+1], w) {
				t.Errorf("line %d lacks %q:\n%s", i+1, w, lines[i+1])
			}
		}
	}
	if strings.Contains(out, "R-aaaaaaae") {
		t.Errorf("case T-1 shows another ticket's run:\n%s", out)
	}
	// A flag may come before the view's name.
	if code, out, _ := h.run("", "stats", "--json", "case", "T-1"); code != 0 || !strings.HasPrefix(out, "{") {
		t.Fatalf("stats --json case T-1: exit %d\n%s", code, out)
	}
	// One run, and the same as JSON.
	if code, out, _ := h.run("", "stats", "case", "R-aaaaaaad"); code != 0 || !strings.HasPrefix(out, "R-aaaaaaad: 1 run, $1.30\n") {
		t.Fatalf("case of a run: exit %d\n%s", code, out)
	}
	var got struct{ Runs []stats.Fact }
	h.ok(&got, "stats", "case", "1")
	if len(got.Runs) != 3 || got.Runs[1].Findings[0].Fid != "F1" || got.Runs[2].Spend[0].USD == nil {
		t.Fatalf("case --json: %+v", got.Runs)
	}
	for _, args := range [][]string{{"stats", "case", "T-9"}, {"stats", "case", "R-zzzzzzzz"}} {
		if code, out, _ := h.run("", args...); code != 0 || !strings.HasSuffix(out, ": no runs in stats.db\n") {
			t.Errorf("%v: exit %d %q", args, code, out)
		}
	}
	for _, args := range [][]string{{"stats", "case"}, {"stats", "case", "build"}, {"stats", "case", "T-1", "T-2"}} {
		if code, _, _ := h.run("", args...); code != 2 {
			t.Errorf("%v: exit %d; want 2", args, code)
		}
	}
}

// Views read while a writer holds stats.db's lock, and make no file.
func TestStatsViewsOnlyRead(t *testing.T) {
	h, _ := pinHarness(t, "")
	for _, view := range [][]string{{"stats", "case", "T-1"}, {"stats", "health"}, {"stats", "costs"}} {
		if code, out, errs := h.run("", view...); code != 0 || errs != "" {
			t.Fatalf("%v with no stats.db: exit %d %s%s", view, code, out, errs)
		}
	}
	if ents, _ := filepath.Glob(filepath.Join(h.dir, "stats*")); len(ents) != 0 {
		t.Fatalf("views made %v", ents)
	}
	statsPut(t, h.dir, caseRows(time.Now().Add(-time.Hour))...)
	holdStats(t, h.dir)
	if code, out, errs := h.run("", "stats", "case", "T-1"); code != 0 || !strings.HasPrefix(out, "T-1: 3 runs") {
		t.Fatalf("case behind a writer's lock: exit %d %s%s", code, out, errs)
	}
}

// health counts the ended runs on disk since the first row that have
// none, such as a dropped write; a live run isn't one.
func TestStatsHealthMissingRows(t *testing.T) {
	h, _ := pinHarness(t, "")
	dir := filepath.Join(h.dir, "runs")
	base := pinBase()
	put := func(start time.Time, state string) runs.Record {
		r := runs.Record{Launcher: "senior-engineer", Job: "build", Command: []string{"claude", "-p"}, Dir: h.dir,
			Limit: "2h", StartedAt: start, State: state, PID: 4001}
		if state != runs.Running {
			r.EndedAt, r.Exit = ended(start, time.Minute), exitCode(0)
		}
		return putRun(t, dir, r)
	}
	put(base.Add(-time.Hour), runs.Done) // ended before stats.db's first row
	stored1 := put(base, runs.Done)
	stored2 := put(base.Add(time.Minute), runs.Failed)
	put(base.Add(2*time.Minute), runs.Done) // its write was dropped
	live := put(base.Add(3*time.Minute), runs.Running)
	holdRun(t, dir, live.ID)
	// With no row yet there is no first row to count from.
	if _, out, _ := h.run("", "stats", "health"); !strings.Contains(out, "missing rows: no row yet; 4 ended runs on disk\n") {
		t.Errorf("health, no stats.db:\n%s", out)
	}
	row := func(r runs.Record) stats.Run {
		return stats.Run{Run: r.ID, Job: "build", StartedAt: r.StartedAt, EndedAt: *r.EndedAt}
	}
	statsPut(t, h.dir, row(stored1), row(stored2))
	var got statsHealth
	h.ok(&got, "stats", "health")
	if got.Stats != "on" || got.File != "ok" || got.Schema != stats.Version() || got.Rows != 2 || got.Ended != 3 || got.Missing != 1 {
		t.Fatalf("health: %+v", got)
	}
	_, out, _ := h.run("", "stats", "health")
	for _, w := range []string{"stats: on\n", "stats.db: schema 1, 2 runs, ", "missing rows: 1 of 3 ended runs on disk since the first row\n",
		"disk free: 50% space, 50% inodes\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("health lacks %q:\n%s", w, out)
		}
	}

	// Off, newer and unreadable files say so, and exit 0.
	stats.SetOff(h.dir, true)
	if _, out, _ := h.run("", "stats", "health"); !strings.HasPrefix(out, "stats: off (STATS_OFF sits beside the store)") {
		t.Errorf("health, off:\n%s", out)
	}
	stats.SetOff(h.dir, false)
	statsModes[5].setup(t, h.dir, h) // newer
	if code, out, _ := h.run("", "stats", "health"); code != 0 || !strings.Contains(out, "stats.db: schema 99, newer than this maestro-ticket's 1") {
		t.Errorf("health, newer: exit %d\n%s", code, out)
	}
	os.Remove(filepath.Join(h.dir, stats.DBName+"-wal"))
	os.Remove(filepath.Join(h.dir, stats.DBName+"-shm"))
	statsModes[4].setup(t, h.dir, h) // corrupt
	if code, out, _ := h.run("", "stats", "health"); code != 0 || !strings.Contains(out, "stats.db: unreadable (") {
		t.Errorf("health, corrupt: exit %d\n%s", code, out)
	}
}

// stats costs counts each run's own spend: a resume what it added to its
// session's totals. A run whose dollars can't be known counts in the
// header, not the rows.
func TestStatsCostsUnits(t *testing.T) {
	h, _ := pinHarness(t, "")
	base := time.Now().Add(-48 * time.Hour)
	rows := caseRows(base)
	// A resume of a session stats.db never saw, and a run with only its
	// stream's tokens: neither has a delta in dollars.
	lost := statsRow("R-aaaaaaaf", 3, "build", base.Add(40*time.Minute), caseS9, usdModel("opus-5.5", 9, 9000))
	lost.ResumeOf, lost.Why = "R-zzzzzzzz", "resume"
	stream := statsRow("R-aaaaaaag", 3, "build", base.Add(50*time.Minute), "", stats.Model{Model: "opus-5.5", InputCum: 700, CostSrc: "stream"})
	stream.Why = "tier_senior"
	statsPut(t, h.dir, append(rows, lost, stream)...)

	type out struct {
		Rows    []costRow
		Total   costRow
		NoDelta int `json:"no_delta"`
		Tokens  int64
	}
	get := func(args ...string) (map[string]float64, out) {
		var o out
		h.ok(&o, append([]string{"stats", "costs"}, args...)...)
		m := map[string]float64{}
		for _, r := range o.Rows {
			m[r.Key] = float64(int(r.USD*100+0.5)) / 100
		}
		return m, o
	}
	same := func(name string, got, want map[string]float64) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: %v; want %v", name, got, want)
			return
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %v; want %v", name, got, want)
				return
			}
		}
	}
	m, o := get()
	same("by model", m, map[string]float64{"opus-5.5": 3.5, "haiku-4.5": 0.3, "sonnet-5.5": 0.4})
	// Tokens: 1100 + 400 + (1750 - 1100) + 300.
	if o.NoDelta != 2 || o.Total.Runs != 4 || int(o.Total.USD*100+0.5) != 420 || o.Tokens != 2450 {
		t.Errorf("by model: no delta %d, total %+v, tokens %d", o.NoDelta, o.Total, o.Tokens)
	}
	m, _ = get("--by", "job")
	same("by job", m, map[string]float64{"build": 3.3, "review": 0.5, "qa": 0.4})
	m, _ = get("--by", "why")
	same("by why", m, map[string]float64{"tier_senior": 2.0, "route_full": 0.5, "resume": 1.3, "(none)": 0.4})
	m, _ = get("--by", "lean")
	same("by lean", m, map[string]float64{"lean": 0.4, "not lean": 3.8})
	m, o = get("--since", "24h")
	if len(m) != 0 || o.Total.Runs != 0 {
		t.Errorf("--since 24h: %v %+v", m, o.Total)
	}

	code, text, errs := h.run("", "stats", "costs", "--by", "job")
	if code != 0 || errs != "" || !strings.HasPrefix(text, "per run, from stats.db; 2 runs with no delta\nJOB ") ||
		!strings.HasSuffix(text, "tokens: 2450\n") {
		t.Errorf("stats costs --by job: exit %d\n%s%s", code, text, errs)
	}
	if code, _, errs := h.run("", "stats", "costs", "--by", "actor"); code != 2 || !strings.Contains(errs, "--by wants model, job, why or lean") {
		t.Errorf("--by actor: exit %d %s", code, errs)
	}
	if code, _, _ := h.run("", "stats", "costs", "--since", "yesterday"); code != 2 {
		t.Errorf("--since yesterday: exit %d", code)
	}
}

// With stats off, or none yet, a view prints one line and exits 0.
func TestStatsCostsOff(t *testing.T) {
	h, _ := pinHarness(t, "")
	for _, view := range []string{"costs", "case"} {
		args := []string{"stats", view}
		if view == "case" {
			args = append(args, "T-1")
		}
		if code, out, _ := h.run("", args...); code != 0 || out != "no stats yet: a run's supervisor starts stats.db at the first run's end\n" {
			t.Errorf("%v, no stats.db: exit %d %q", args, code, out)
		}
		statsPut(t, h.dir, caseRows(time.Now().Add(-time.Hour))...)
		stats.SetOff(h.dir, true)
		if code, out, _ := h.run("", args...); code != 0 || out != "stats are off (STATS_OFF sits beside the store); stats health says more\n" {
			t.Errorf("%v, off: exit %d %q", args, code, out)
		}
		stats.SetOff(h.dir, false)
		h.env[stats.OffEnv] = "off"
		var note struct{ Note string }
		h.ok(&note, args...)
		if note.Note != "stats are off (MAESTRO_TICKET_STATS=off); stats health says more" {
			t.Errorf("%v, env off: %q", args, note.Note)
		}
		delete(h.env, stats.OffEnv)
		for _, f := range []string{"", "-wal", "-shm"} {
			os.Remove(filepath.Join(h.dir, stats.DBName+f))
		}
	}
}

// A bot in its own shell reads no view: what it reads reaches Grok. A
// Claude session it started, with MAESTRO_RUN_ID set, may.
func TestStatsRefusesBots(t *testing.T) {
	h, _ := pinHarness(t, "")
	statsPut(t, h.dir, caseRows(time.Now().Add(-time.Hour))...)
	views := [][]string{{"stats", "case", "T-1"}, {"stats", "health"}, {"stats", "costs"}, {"stats", "costs", "--by", "job"}}
	for _, bot := range botProfiles {
		h.env["MAESTRO_TICKET_ACTOR"] = bot
		for _, v := range views {
			if got := h.fails(2, v...); got != "forbidden" {
				t.Errorf("%s %v: %s; want forbidden", bot, v, got)
			}
		}
	}
	// Before a bad flag: the bot learns the rule, not the flag.
	if got := h.fails(2, "stats", "costs", "--by", "actor"); got != "forbidden" {
		t.Errorf("a bot's bad --by: %s", got)
	}
	// --actor names a bot too; a person's actor, or none, reads.
	delete(h.env, "MAESTRO_TICKET_ACTOR")
	if got := h.fails(2, "stats", "health", "--actor", "planner"); got != "forbidden" {
		t.Errorf("--actor planner: %s", got)
	}
	for _, actor := range []string{"", "paul"} {
		h.env["MAESTRO_TICKET_ACTOR"] = actor
		for _, v := range views {
			h.ok(nil, v...)
		}
	}
	h.env["MAESTRO_TICKET_ACTOR"] = "chief-of-staff"
	h.env[runs.RunIDEnv] = "R-aaaaaaaa"
	for _, v := range views {
		h.ok(nil, v...)
	}
	delete(h.env, runs.RunIDEnv)
	// The list of views, and on and off, are anyone's.
	if code, out, _ := h.run("", "stats"); code != 0 || out != statsHelp {
		t.Errorf("stats for a bot: exit %d", code)
	}
	if code, _, _ := h.run("", "stats", "on"); code != 0 {
		t.Errorf("stats on for a bot: exit %d", code)
	}
	// The message names the rule.
	_, _, errs := h.run("", "stats", "costs")
	if errs != "maestro-ticket: not allowed for this actor: stats costs: chief-of-staff is a bot, and stats are for people and Claude sessions\n" {
		t.Errorf("refusal: %q", errs)
	}
}

func TestStatsHelp(t *testing.T) {
	h, _ := pinHarness(t, "")
	for _, args := range [][]string{{"stats"}, {"stats", "--help"}, {"stats", "-h"}, {"stats", "help"}} {
		code, out, errs := h.run("", args...)
		if code != 0 || out != statsHelp || errs != "" {
			t.Errorf("%v: exit %d %q %q", args, code, out, errs)
		}
	}
	for _, line := range strings.Split(statsHelp, "\n") {
		if len(line) > 80 {
			t.Errorf("help line over 80 columns: %q", line)
		}
	}
	if code, _, errs := h.run("", "stats", "kickbacks"); code != 2 || !strings.Contains(errs, "a view: case, health, costs") {
		t.Errorf("stats kickbacks: exit %d %s", code, errs)
	}
	// The top usage text has no stats line.
	_, out, _ := h.run("", "--help")
	if strings.Contains(out, "stats") {
		t.Error("the usage text names stats")
	}
}
