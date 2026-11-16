//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
)

func reviewFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "review", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// reviewRow writes a run of job with reply and drops into a fresh
// stats.db in dir, as its end would, and returns the row.
func reviewRow(t *testing.T, dir, id, job, reply string, drops []string) stats.Run {
	t.Helper()
	runsDir := filepath.Join(dir, "runs")
	os.MkdirAll(runsDir, 0o755)
	if drops != nil {
		writeSide(runsDir, id, &sideFacts{Why: "route_full", Drops: drops})
	}
	if _, err := os.Stat(filepath.Join(dir, stats.DBName)); err != nil {
		prepareStatsAt(t, dir)
	}
	c := &call{env: Env{Getenv: func(string) string { return "" }, DiskFree: halfFree}}
	row := c.statsRow(dir, runs.Record{ID: id, Job: job, State: runs.Done, Reply: reply}, claudeReply{}, os.ErrNotExist)
	db, err := stats.Open(filepath.Join(dir, stats.DBName), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := stats.Write(t.Context(), db, row); err != nil {
		t.Fatalf("write: %v", err)
	}
	return row
}

// findingRows are run id's findings rows, as "fid kind pass sev action
// prior n" with - for NULL, in insert order.
func findingRows(t *testing.T, dir, id string) []string {
	t.Helper()
	db, err := stats.Open(filepath.Join(dir, stats.DBName), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT coalesce(f.fid, '-'), coalesce(k.name, '-'), coalesce(f.pass, '-'), coalesce(s.name, '-'),
		coalesce(a.name, '-'), coalesce(p.name, '-'), f.n
		FROM findings f LEFT JOIN codes k ON k.id = f.kind LEFT JOIN codes s ON s.id = f.sev
		LEFT JOIN codes a ON a.id = f.action LEFT JOIN codes p ON p.id = f.prior WHERE f.run = ? ORDER BY f.rowid`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v [7]string
		if err := rows.Scan(&v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6]); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Join(v[:], " "))
	}
	return out
}

// reviewFactsRow is run id's head, verdict, v_tier, mode and reason, with
// - for NULL.
func reviewFactsRow(t *testing.T, dir, id string) string {
	t.Helper()
	var head, verdict, tier, mode, reason string
	statsQuery(t, dir, `SELECT coalesce(h.name, '-'), coalesce(v.name, '-'), coalesce(f.v_tier, '-'), coalesce(m.name, '-'), coalesce(r.name, '-')
		FROM run_facts f LEFT JOIN codes h ON h.id = f.head LEFT JOIN codes v ON v.id = f.verdict
		LEFT JOIN codes m ON m.id = f.mode LEFT JOIN codes r ON r.id = f.reason WHERE f.run = '`+id+`'`,
		&head, &verdict, &tier, &mode, &reason)
	return strings.Join([]string{head, verdict, tier, mode, reason}, " ")
}

// A reply's first line maps to its head by prefix, for every format the
// shipped skills ask for; any other line is other, and no reply is NULL.
func TestHeadCodes(t *testing.T) {
	heads := statsLists()["head"]
	sha40 := strings.Repeat("ab", 20)
	for in, want := range map[string]string{
		"":                                           "",
		"  \n\n":                                     "",
		"test: pass: T-12 at abc1234, tier 1":        "test:pass",
		"test: blocked: T-12 at abc1234":             "test:blocked",
		"test: refused":                              "test:refused",
		"live: fail: T-3 at abc1234":                 "live:fail",
		"repro: not reproduced: T-4 at abc1234":      "repro:not_reproduced",
		"repro: reproduced: T-4 at abc1234":          "repro:reproduced",
		"second-look: disagree: T-4 at abc1234":      "second-look:disagree",
		"plan: draft: T-5":                           "plan:draft",
		"plan: pass: T-5 at abc1234":                 "plan:pass",
		"answer: done: T-5 at abc1234":               "answer:done",
		"judge: done: 3 decided, 1 up":               "judge:done",
		"rule: stopped: T-5 held":                    "rule:stopped",
		"finish: done: T-5 already done":             "finish:done",
		"design: ready":                              "design:ready",
		"design: refused: spec has no criteria":      "design:refused",
		"review: stopped: tier 2 needs the full":     "review:stopped",
		"infra block: disk full":                     "infra_block:disk_full",
		"\r\njudge: done: 0 decided, 0 up\r\nmore":   "judge:done",
		"test: passed: T-1":                          stats.Other,
		"test:pass":                                  stats.Other,
		"Test: pass: T-1":                            stats.Other,
		"testing: pass: T-1":                         stats.Other,
		"judge: ready":                               stats.Other,
		"…ut reply: test: pass":                      stats.Other,
		"# Code review: T-1 a title\nVerdict: GO":    "review:go",
		"# Code review: T-1 a\nPR: x\nVerdict: GOOD": stats.Other,
		"# Code review: T-1 a\nno verdict line":      stats.Other,
		"Verdict: GO":                                stats.Other,
		// A head reviewed before: the reply is its verdict's first line.
		"Review: GO at " + sha40 + " (PR x), tier 1":            "review:go",
		"Kickback: 2 blockers at " + sha40:                      "review:nogo",
		"Escalation: review at " + sha40 + ": PR x merged as y": "review:escalate",
		"Kickback: QA fail at " + sha40:                         stats.Other,
	} {
		if got := headCode(in); got != want {
			t.Errorf("headCode(%q) = %q; want %q", in, got, want)
		}
		if want != "" && want != stats.Other && !slices.Contains(heads, want) {
			t.Errorf("%q is not on the head list", want)
		}
	}
	for name, want := range map[string]string{
		"nogo": "review:nogo", "go": "review:go", "escalate_repeat": "review:escalate", "escalate_merged": "review:escalate",
		"report_only": "review:report_only_nogo", "light_stopped": "review:stopped",
	} {
		if got := headCode(reviewFixture(t, name)); got != want {
			t.Errorf("%s: head %q; want %q", name, got, want)
		}
	}

	// Every first line the shipped skills give maps to its own head.
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "skills", p))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	alts := func(text, re string) []string {
		m := regexp.MustCompile(re).FindStringSubmatch(text)
		if m == nil {
			t.Fatalf("no %s in the skill", re)
		}
		var out []string
		for _, s := range regexp.MustCompile(`\s*\|\s*`).Split(m[1], -1) {
			out = append(out, strings.TrimSpace(s))
		}
		return out
	}
	shipped := map[string][]string{}
	qa := read("maestro-qa-lead/SKILL.md")
	for _, m := range regexp.MustCompile(`\| \*\*([a-z-]+)\*\* \|`).FindAllStringSubmatch(qa, -1) {
		shipped[m[1]] = append(shipped[m[1]], alts(qa, "(?m)^<job>: <([^>]+)>:")...)
	}
	planner := alts(read("maestro-planner/SKILL.md"), "`<job>: <([^>]+)>:")
	for _, w := range []string{"plan", "rule", "finish", "check", "board", "answer"} {
		shipped[w] = append(shipped[w], planner...)
	}
	shipped["design"] = alts(read("maestro-designer/SKILL.md"), "(?m)^design: <([^>]+)>:")
	shipped["judge"] = []string{regexp.MustCompile("(?m)^judge: ([a-z]+):").FindStringSubmatch(read("maestro-planner/references/judge.md"))[1]}
	review := read("maestro-code-review/SKILL.md")
	shipped["review"] = []string{regexp.MustCompile("`review: ([a-z]+): tier").FindStringSubmatch(review)[1]}
	if len(shipped) < 13 {
		t.Fatalf("read %d words from the skills", len(shipped))
	}
	for w, sts := range shipped {
		for _, st := range sts {
			line := w + ": " + st + ": T-1 at abc1234"
			if got := headCode(line); got != headName(w, st) {
				t.Errorf("shipped %q: head %q", line, got)
			}
		}
	}
	verdicts := regexp.MustCompile(`(?m)^Verdict: (.+)$`).FindStringSubmatch(review)
	if verdicts == nil {
		t.Fatal("no Verdict: line in the review skill")
	}
	for _, v := range regexp.MustCompile(`\s*(?:\||, or|\()\s*`).Split(verdicts[1], -1) {
		if v = strings.TrimSpace(v); v == "" || strings.HasPrefix(v, "a REPORT-ONLY") {
			continue
		}
		if got := headCode("# Code review: T-1 t\nVerdict: " + v); got == stats.Other || !slices.Contains(heads, got) {
			t.Errorf("Verdict: %s: head %q", v, got)
		}
	}
}

// A review's findings JSON gives the row's verdict, tier and mode and one
// row per finding; the Follow-ups lines give one counted row.
func TestFindingsFromReply(t *testing.T) {
	dir := t.TempDir()
	reviewRow(t, dir, "R-aaaaaaaa", "review", reviewFixture(t, "nogo"), nil)
	if got, want := reviewFactsRow(t, dir, "R-aaaaaaaa"), "review:nogo nogo 1 incremental -"; got != want {
		t.Errorf("facts %q; want %q", got, want)
	}
	want := []string{
		"F1 finding 1 blocker auto-fix still_present 1",
		"F3 finding 5 major ask-user new 1",
		"F4 finding 7 minor no-op new 1",
		"- follow_up - - - - 2",
	}
	if got := findingRows(t, dir, "R-aaaaaaaa"); !slices.Equal(got, want) {
		t.Errorf("findings\n got %q\nwant %q", got, want)
	}

	// Keys in another order, over several lines, and no findings.
	reviewRow(t, dir, "R-aaaaaaab", "review", reviewFixture(t, "go"), nil)
	if got, want := reviewFactsRow(t, dir, "R-aaaaaaab"), "review:go go 0 full -"; got != want {
		t.Errorf("go: %q; want %q", got, want)
	}
	if got := findingRows(t, dir, "R-aaaaaaab"); len(got) != 0 {
		t.Errorf("go: findings %q", got)
	}
	// A report-only run in a fenced block: the head says report only, the
	// JSON gives the verdict.
	reviewRow(t, dir, "R-aaaaaaac", "review", reviewFixture(t, "report_only"), nil)
	if got, want := reviewFactsRow(t, dir, "R-aaaaaaac"), "review:report_only_nogo nogo 1 full -"; got != want {
		t.Errorf("report only: %q; want %q", got, want)
	}
	// No JSON: the head only. A cut JSON is no JSON.
	nogo := reviewFixture(t, "nogo")
	cut := nogo[:strings.Index(nogo, `"prior": "new"}]}`)]
	reviewRow(t, dir, "R-aaaaaaad", "review", cut, nil)
	if got, want := reviewFactsRow(t, dir, "R-aaaaaaad"), "review:nogo - - - -"; got != want {
		t.Errorf("cut JSON: %q; want %q", got, want)
	}
	// Another job's reply that quotes a findings JSON keeps none of it.
	reviewRow(t, dir, "R-aaaaaaae", "build", "Fixed the kickback:\n"+nogo, nil)
	if got, want := reviewFactsRow(t, dir, "R-aaaaaaae"), "other - - - -"; got != want {
		t.Errorf("build: %q; want %q", got, want)
	}
	if got := findingRows(t, dir, "R-aaaaaaae"); len(got) != 0 {
		t.Errorf("build: findings %q", got)
	}
	// A JSON the report quotes, then the real one: the last counts.
	two := `# Code review: T-1 t
Verdict: GO
## Earlier feedback   the last kickback said {"verdict": "NO-GO", "tier": 2, "findings": [{"id": "F9"}]}
{"verdict": "GO", "tier": 1, "mode": "full", "findings": []}`
	reviewRow(t, dir, "R-aaaaaaaf", "review", two, nil)
	if got, want := reviewFactsRow(t, dir, "R-aaaaaaaf"), "review:go go 1 full -"; got != want {
		t.Errorf("two JSONs: %q; want %q", got, want)
	}
}

// A value off its shape or range is stored as NULL or other, and never
// costs the row.
func TestFindingsClamp(t *testing.T) {
	dir := t.TempDir()
	reply := `{"verdict": "MAYBE", "tier": 7, "mode": "sideways", "findings": [
		{"id": "F1", "severity": "blocker", "action": "auto-fix", "pass": 4815162342, "prior": "new"},
		{"id": "F1234", "severity": "BLOCKER", "action": "fix-it", "pass": 0, "prior": "fixed"},
		{"id": "f2", "severity": "major", "action": "no-op", "pass": 11, "prior": "still present"},
		{"id": "F2; DROP TABLE findings", "severity": "nit", "action": "ask-user", "pass": -1},
		{"id": 3, "severity": ["major"], "action": null, "pass": "3", "prior": {"x": 1}},
		{"id": "F5", "pass": 3.5},
		{"id": "F6", "pass": 10},
		"not an object",
		{"id": "F7", "severity": "` + strings.Repeat("x", 200) + `", "pass": 1}
	]}`
	row := reviewRow(t, dir, "R-aaaaaaaa", "review", reply, nil)
	if row.VTier != nil {
		t.Errorf("tier 7 kept as %d", *row.VTier)
	}
	if got, want := reviewFactsRow(t, dir, "R-aaaaaaaa"), "other other - other -"; got != want {
		t.Errorf("facts %q; want %q", got, want)
	}
	want := []string{
		"F1 finding - blocker auto-fix new 1",
		"- finding - other other other 1",
		"- finding - major no-op still_present 1",
		"- finding - nit ask-user - 1",
		"- finding - - - - 1",
		"F5 finding - - - - 1",
		"F6 finding 10 - - - 1",
		"F7 finding 1 - - - 1",
	}
	if got := findingRows(t, dir, "R-aaaaaaaa"); !slices.Equal(got, want) {
		t.Errorf("findings\n got %q\nwant %q", got, want)
	}
	// findings that isn't an array, and a flood of them.
	reviewRow(t, dir, "R-aaaaaaab", "review", `{"verdict": "GO", "findings": {"F1": "x"}}`, nil)
	if got := findingRows(t, dir, "R-aaaaaaab"); len(got) != 0 {
		t.Errorf("an object for findings gave %q", got)
	}
	var many []string
	for i := range 1000 {
		many = append(many, fmt.Sprintf(`{"id": "F%d", "severity": "nit", "pass": 1}`, i%999+1))
	}
	reviewRow(t, dir, "R-aaaaaaac", "review", `{"verdict": "NO-GO", "findings": [`+strings.Join(many, ",")+`]}`, nil)
	if n := len(findingRows(t, dir, "R-aaaaaaac")); n != maxReviewFindings {
		t.Errorf("%d rows from 1,000 findings; want %d", n, maxReviewFindings)
	}
	// The table's own checks hold Write to the same shapes.
	db, _ := stats.Open(filepath.Join(dir, stats.DBName), false)
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO findings (run, fid, n) VALUES ('R-aaaaaaaa', 'F1234', 1)`,
		`INSERT INTO findings (run, fid, n) VALUES ('R-aaaaaaaa', 'Fx', 1)`,
		`INSERT INTO findings (run, pass, n) VALUES ('R-aaaaaaaa', 4815162342, 1)`,
	} {
		if _, err := db.Exec(q); err == nil {
			t.Errorf("stats.db took %s", q)
		}
	}
}

// reviewClaude makes j's claude reply with reply, as a review would.
func reviewClaude(t *testing.T, j *jobHarness, reply string) {
	t.Helper()
	res, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": reply,
		"session_id": "22222222-2222-4222-8222-222222222222", "total_cost_usd": 0.01, "modelUsage": map[string]any{}})
	if err := os.WriteFile(filepath.Join(j.out, "result.json"), append(res, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(j.bin, "claude"), []byte("#!/bin/sh\ncat >/dev/null\ncat \"$FAKE_OUT/result.json\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Each id --drop named becomes a dropped row of the review run, and of a
// job again of it, which keeps the first prompt's drops; never its reason.
func TestDroppedRowsFromSideFile(t *testing.T) {
	t.Parallel()
	j := newJobHarness(t, "reviewer")
	reviewScene(t, j)
	reviewClaude(t, j, reviewFixture(t, "go"))
	j.h.ok(nil, "create", "--title", "a", "--tier", "senior")
	j.h.ok(nil, "create", "--title", "b", "--tier", "junior")
	moveTo(j, "T-2", "in_progress", "in_review")
	_, r := j.job("F1 and F3 are moot\n", "review", "T-2", "--drop", "F1,F3,X.2", "--drop-file", "-")
	j.waitSupervisors()
	// go.txt's Follow-ups says none; X.2 is off the id shape.
	want := []string{"F1 dropped - - - - 1", "F3 dropped - - - - 1", "- dropped - - - - 1"}
	if got := findingRows(t, j.h.dir, r.ID); !slices.Equal(got, want) {
		t.Fatalf("review: findings %q; want %q", got, want)
	}
	if got, want := reviewFactsRow(t, j.h.dir, r.ID), "review:go go 0 full -"; got != want {
		t.Errorf("review: facts %q; want %q", got, want)
	}
	_, again := j.job("", "again", r.ID)
	j.waitSupervisors()
	if got := findingRows(t, j.h.dir, again.ID); !slices.Equal(got, want) {
		t.Fatalf("again: findings %q; want %q", got, want)
	}

	// No side file: no dropped rows, and the rest as before.
	dir := t.TempDir()
	reviewRow(t, dir, "R-aaaaaaaa", "review", reviewFixture(t, "go"), nil)
	if got := findingRows(t, dir, "R-aaaaaaaa"); len(got) != 0 {
		t.Errorf("no side file: %q", got)
	}
}

// Go works out why a review escalated from what it wrote; a failure's own
// reason wins, and a run that didn't escalate gets none.
func TestEscalateCause(t *testing.T) {
	dir := t.TempDir()
	for i, c := range []struct{ fixture, want string }{
		{"escalate_repeat", "review:escalate escalate 1 incremental repeat"},
		{"escalate_merged", "review:escalate - - - merged"},
		{"escalate_outside", "review:escalate escalate 2 full outside_step"},
		{"escalate_approval", "review:escalate escalate 2 full approval"},
		{"nogo", "review:nogo nogo 1 incremental -"},
		{"go", "review:go go 0 full -"},
	} {
		id := fmt.Sprintf("R-aaaaaaa%c", "abcdefghij"[i])
		reviewRow(t, dir, id, "review", reviewFixture(t, c.fixture), nil)
		if got := reviewFactsRow(t, dir, id); got != c.want {
			t.Errorf("%s: %q; want %q", c.fixture, got, c.want)
		}
	}
	// A repeat needs every blocker and major raised before.
	fs := []stats.Finding{{Kind: "finding", Sev: "blocker", Prior: "still_present"}, {Kind: "finding", Sev: "major", Prior: "new"}}
	if got := escalateCause("", fs); got != "approval" {
		t.Errorf("one new major: %q", got)
	}
	if got := escalateCause("", fs[:1]); got != "repeat" {
		t.Errorf("one blocker raised before: %q", got)
	}
	if got := escalateCause("", nil); got != "approval" {
		t.Errorf("no findings: %q", got)
	}
	// A run that failed keeps the failure's reason.
	c := &call{env: Env{Getenv: func(string) string { return "" }, DiskFree: halfFree}}
	row := c.statsRow(t.TempDir(), runs.Record{ID: "R-bbbbbbbb", Job: "review", State: runs.Failed, Reply: reviewFixture(t, "escalate_repeat")},
		claudeReply{}, os.ErrNotExist)
	if row.Reason != "no_result" || row.Verdict != "escalate" {
		t.Errorf("failed escalate: reason %q verdict %q", row.Reason, row.Verdict)
	}
	for _, cause := range escalateCauses {
		if !slices.Contains(statsLists()["reason"], cause) {
			t.Errorf("%s is not on the reason list", cause)
		}
	}
}

// grepStats fails for each marker stats.db or its WAL holds, before and
// after a checkpoint and VACUUM.
func grepStats(t *testing.T, dir string, markers []string) {
	t.Helper()
	grep := func(when string) {
		for _, f := range []string{stats.DBName, stats.DBName + "-wal"} {
			b, _ := os.ReadFile(filepath.Join(dir, f))
			for _, m := range markers {
				if bytes.Contains(b, []byte(m)) {
					t.Errorf("%s: %s holds %q", when, f, m)
				}
			}
		}
	}
	grep("before a checkpoint")
	db, err := stats.Open(filepath.Join(dir, stats.DBName), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{`PRAGMA wal_checkpoint(TRUNCATE)`, `VACUUM`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	grep("after a checkpoint and VACUUM")
}

// Markers in a finding's file, evidence, impact and fix, in the fields
// stats does decode, in the report and in the --drop-file reason miss
// stats.db.
func TestFindingsMarkers(t *testing.T) {
	t.Parallel()
	markers := []string{"zqmark7", "deadbeefcafe", strings.Repeat("d3adb33f", 5), "claude-zqmark", "cafe0123beef"}
	all := strings.Join(markers, " ")
	doc, _ := json.Marshal(map[string]any{"head": strings.Repeat("d3adb33f", 5), "verdict": "NO-GO", "tier": 1, "mode": "zqmark7",
		"findings": []map[string]any{
			{"id": "F1", "severity": "blocker", "action": "auto-fix", "pass": 2, "file": "zqmark7/deadbeefcafe.go", "line": 9,
				"evidence": all, "impact": all, "fix": all, "prior": "new"},
			{"id": "zqmark7", "severity": "deadbeefcafe", "action": "claude-zqmark", "pass": 3, "file": all, "prior": "cafe0123beef"},
		}})
	reply := "# Code review: T-2 " + all + "\nVerdict: NO-GO\n## Out of scope\n- " + all + "\n## Outcome " + all + "\n" + string(doc) + "\n" + all + "?"
	j := newJobHarness(t, "reviewer")
	reviewScene(t, j)
	reviewClaude(t, j, reply)
	prepareStatsAt(t, j.h.dir)
	j.h.ok(nil, "create", "--title", all, "--tier", "senior")
	j.h.ok(nil, "create", "--title", all, "--tier", "junior")
	moveTo(j, "T-2", "in_progress", "in_review")
	_, r := j.job(all+"\n", "review", "T-2", "--drop", "F1,zqmark7", "--drop-file", "-")
	j.waitSupervisors()
	got := findingRows(t, j.h.dir, r.ID)
	want := []string{"F1 finding 2 blocker auto-fix new 1", "- finding 3 other other other 1", "- follow_up - - - - 1",
		"F1 dropped - - - - 1", "- dropped - - - - 1"}
	if !slices.Equal(got, want) {
		t.Fatalf("findings\n got %q\nwant %q", got, want)
	}
	if got, want := reviewFactsRow(t, j.h.dir, r.ID), "review:nogo nogo 1 other -"; got != want {
		t.Errorf("facts %q; want %q", got, want)
	}
	grepStats(t, j.h.dir, markers)
}

// The review decoder reads only these fields, each loose; a new field is
// a choice to make here, on purpose.
func TestReviewDecoderFields(t *testing.T) {
	loose := map[reflect.Type]bool{
		reflect.TypeFor[looseStr](): true, reflect.TypeFor[looseNum](): true, reflect.TypeFor[looseFindings](): true,
	}
	fields := func(v any) []string {
		var out []string
		ty := reflect.TypeOf(v)
		for i := range ty.NumField() {
			f := ty.Field(i)
			if !loose[f.Type] {
				t.Errorf("%s.%s is %s, not a loose type", ty.Name(), f.Name, f.Type)
			}
			out = append(out, f.Tag.Get("json"))
		}
		return out
	}
	if got, want := fields(reviewDoc{}), []string{"verdict", "tier", "mode", "findings"}; !slices.Equal(got, want) {
		t.Errorf("reviewDoc decodes %q; want %q", got, want)
	}
	if got, want := fields(reviewFinding{}), []string{"id", "severity", "action", "pass", "prior"}; !slices.Equal(got, want) {
		t.Errorf("reviewFinding decodes %q; want %q", got, want)
	}
	if et := reflect.TypeFor[looseFindings]().Elem(); et != reflect.TypeFor[reviewFinding]() {
		t.Errorf("looseFindings holds %s", et)
	}
}
