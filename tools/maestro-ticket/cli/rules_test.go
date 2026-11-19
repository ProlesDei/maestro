//go:build unix

package cli

import (
	"encoding/json"
	"fmt"
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

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

func newRulesHarness(t *testing.T, actor string) *harness {
	t.Helper()
	h := newHarness(t)
	h.env["MAESTRO_TICKET_ACTOR"] = actor
	h.ok(nil, "doctor") // makes the store
	return h
}

// try runs a command and returns its exit code and output, both streams.
func (h *harness) try(stdin string, args ...string) (int, string) {
	code, out, errs := h.run(stdin, args...)
	return code, out + errs
}

func (h *harness) must(stdin string, args ...string) string {
	h.t.Helper()
	code, out := h.try(stdin, args...)
	if code != 0 {
		h.t.Fatalf("%v: exit %d\n%s", args, code, out)
	}
	return out
}

func (h *harness) board() board {
	h.t.Helper()
	b, _, err := readBoard(h.dir)
	if err != nil {
		h.t.Fatal(err)
	}
	return b
}

func (h *harness) rules() (string, []string) {
	h.t.Helper()
	via, lines, _, err := readRules(h.dir)
	if err != nil {
		h.t.Fatal(err)
	}
	return via, lines
}

func (h *harness) logLines() []string {
	b, _ := os.ReadFile(filepath.Join(h.dir, rulesLog))
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func today() string { return time.Now().UTC().Format("2006-01-02") }

func TestRulesSetRefusedInRun(t *testing.T) {
	h := newRulesHarness(t, "planner")
	h.must("", "rules", "set", "limit", "4")
	h.must("a rule\n", "rules", "text")
	files := func() string {
		var s string
		for _, f := range []string{boardFile, rulesFile, rulesLog} {
			b, _ := os.ReadFile(filepath.Join(h.dir, f))
			s += string(b)
		}
		return s
	}
	before := files()
	h.env[runs.RunIDEnv] = "R-aaaaaaaa"
	for _, c := range []struct {
		stdin string
		args  []string
	}{
		{"", []string{"rules", "set", "limit", "300"}},
		{"", []string{"rules", "set", "qa_slots", "9"}},
		{"x\n", []string{"rules", "text"}},
		{"x\n", []string{"rules", "add"}},
		{"", []string{"rules", "drop", "1"}},
		{"", []string{"look", "--limit", "5"}},
		{"x\n", []string{"look", "--judge"}},
	} {
		if code, out := h.try(c.stdin, c.args...); code != 2 || !strings.Contains(out, "MAESTRO_RUN_ID is set") {
			t.Errorf("%v in a run: exit %d %s", c.args, code, out)
		}
	}
	if files() != before {
		t.Fatalf("a refused write changed the files")
	}
	// Reading stays open to a run: a look with no flag, and rules show.
	h.must("", "look")
	h.must("", "rules", "show")
}

func TestRulesActorAllowlist(t *testing.T) {
	h := newRulesHarness(t, "")
	for _, c := range []struct {
		actor, key string
		ok         bool
	}{
		{"senior-engineer", "limit", false},
		{"reviewer", "limit", false},
		{"qa-lead", "limit", false},
		{"qa-lead", "qa_slots", true},
		{"planner", "qa_slots", false},
		{"planner", "limit", true},
		{"chief-of-staff", "limit", true},
		{"chief-of-staff", "qa_slots", true},
		{"paul", "limit", true},
		{"paul", "qa_slots", true},
	} {
		code, out := h.try("", "rules", "set", c.key, "3", "--actor", c.actor)
		if (code == 0) != c.ok || (!c.ok && !strings.Contains(out, "is a bot")) {
			t.Errorf("%s sets %s: exit %d %s", c.actor, c.key, code, out)
		}
	}
	for _, a := range []string{"reviewer", "qa-lead", "designer"} {
		if code, _ := h.try("x\n", "rules", "text", "--actor", a); code != 2 {
			t.Errorf("%s set the rules text", a)
		}
	}
	if code, out := h.try("", "rules", "set", "limit", "3"); code != 2 || !strings.Contains(out, "set MAESTRO_TICKET_ACTOR") {
		t.Fatalf("no actor: exit %d %s", code, out)
	}
}

func TestRulesActorFlagCannotMaskBotEnv(t *testing.T) {
	h := newRulesHarness(t, "reviewer")
	if code, out := h.try("", "rules", "set", "limit", "9", "--actor", "paul"); code != 2 || !strings.Contains(out, "reviewer is a bot") {
		t.Fatalf("reviewer as paul: exit %d %s", code, out)
	}
	h.env["MAESTRO_TICKET_ACTOR"] = "planner"
	h.must("", "rules", "set", "limit", "9", "--actor", "paul")
	if b := h.board(); *b.Limit != 9 || b.SetBy != "paul" || b.LimitVia != "rules" {
		t.Fatalf("board %+v", b)
	}
}

func TestRulesTextOwnsSetter(t *testing.T) {
	h := newRulesHarness(t, "chief-of-staff")
	h.must("kept rule\n", "rules", "text")
	_, first := h.rules()
	h.env["MAESTRO_TICKET_ACTOR"] = "planner"
	h.must("x (set by owner, 2026-01-01)\nkept rule (set by owner, 2026-01-01)\n\n", "rules", "text")
	via, got := h.rules()
	want := []string{"x (set by planner, " + today() + ")", first[0]}
	if via != "rules" || !slices.Equal(got, want) || !strings.Contains(first[0], "(set by chief-of-staff, ") {
		t.Fatalf("rules %s %q; want %q", via, got, want)
	}
}

func TestRulesAddDrop(t *testing.T) {
	h := newRulesHarness(t, "planner")
	h.must("one\ntwo\n", "rules", "text")
	_, before := h.rules()
	h.must("three. Lifts when: never (set by owner, 2026-01-01)\n", "rules", "add")
	_, got := h.rules()
	if !slices.Equal(got[:2], before) || got[2] != "three. Lifts when: never (set by planner, "+today()+")" {
		t.Fatalf("after add: %q", got)
	}
	h.must("", "rules", "drop", "1", "--why", "lifted")
	_, got2 := h.rules()
	if !slices.Equal(got2, got[1:]) {
		t.Fatalf("after drop: %q", got2)
	}
	if code, _ := h.try("", "rules", "drop", "9", "--why", "x"); code != 3 {
		t.Fatalf("drop past the end: exit %d", code)
	}
	if n := len(h.logLines()); n != 3 {
		t.Fatalf("%d log lines; want text, add and drop", n)
	}
	out := h.must("", "rules", "show")
	if !strings.Contains(out, "limit: none\n") || !strings.Contains(out, "1. two (set by planner, ") {
		t.Fatalf("show:\n%s", out)
	}
}

var logLine = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ \S+ (limit|text) (\S+) -> (\S+)$`)

// TestRulesWriteRace: 50 writers and 50 readers at once. No reader sees
// half a file, the last write logged is the one on disk, and the log
// holds one whole line per write.
func TestRulesWriteRace(t *testing.T) {
	h := newRulesHarness(t, "")
	h.must("seed\n", "rules", "text", "--actor", "paul")
	h.must("", "rules", "set", "limit", "1", "--actor", "paul")
	start := len(h.logLines())
	var wg sync.WaitGroup
	done := make(chan struct{})
	errs := make(chan string, 200)
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			actor := fmt.Sprintf("person%d", i)
			var code int
			var out string
			if i%2 == 0 {
				code, out = h.try("", "rules", "set", "limit", strconv.Itoa(i), "--actor", actor)
			} else {
				code, out = h.try(fmt.Sprintf("rule %d\nrule %d b\n", i, i), "rules", "text", "--actor", actor)
			}
			if code != 0 {
				errs <- out
			}
		}()
	}
	var rg sync.WaitGroup
	for range 50 {
		rg.Add(1)
		go func() {
			defer rg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				if _, _, err := readBoard(h.dir); err != nil {
					errs <- "board: " + err.Error()
				}
				b, err := os.ReadFile(filepath.Join(h.dir, rulesFile))
				if err != nil || !strings.HasPrefix(string(b), "# via: rules\n") || !strings.HasSuffix(string(b), ")\n") {
					errs <- fmt.Sprintf("rules.md half written: %q %v", b, err)
				}
			}
		}()
	}
	ended := make(chan struct{})
	go func() { wg.Wait(); close(ended) }()
	select {
	case <-ended:
	case <-time.After(30 * time.Second):
		t.Fatal("50 writers took over 30s")
	}
	close(done)
	rg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	log := h.logLines()[start:]
	if len(log) != 50 {
		t.Fatalf("%d log lines; want 50", len(log))
	}
	// Each write read what the one before it wrote: none was lost.
	last := map[string]string{}
	for _, l := range h.logLines()[:start] {
		m := logLine.FindStringSubmatch(l)
		last[m[1]] = m[3]
	}
	for _, l := range log {
		m := logLine.FindStringSubmatch(l)
		if m == nil {
			t.Fatalf("bad log line %q", l)
		}
		if m[2] != last[m[1]] {
			t.Fatalf("%q read %s; the write before it left %s", l, m[2], last[m[1]])
		}
		last[m[1]] = m[3]
	}
	lastLimit, lastText := last["limit"], last["text"]
	if b := h.board(); strconv.Itoa(*b.Limit) != lastLimit {
		t.Fatalf("limit %d; last logged %s", *b.Limit, lastLimit)
	}
	if _, lines := h.rules(); linesSum(lines) != lastText {
		t.Fatalf("rules %q; last logged %s", lines, lastText)
	}
}

// counts reads the limit a look handed out by from its counts line.
var buildingLimit = regexp.MustCompile(`building \d+/(\d+)`)

func lookLimitOf(t *testing.T, out string) string {
	t.Helper()
	m := buildingLimit.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no counts in %q", out)
	}
	return m[1]
}

func TestLookLimitSeedsBoard(t *testing.T) {
	h := newRulesHarness(t, "planner")
	if code, out := h.try("", "look"); code != 2 || !strings.Contains(out, "rules set limit <n>") {
		t.Fatalf("no board, no flag: exit %d %s", code, out)
	}
	h.must("", "look", "--limit", "7")
	if b := h.board(); *b.Limit != 7 || b.LimitVia != "look" {
		t.Fatalf("seed: %+v", b)
	}
	if got := lookLimitOf(t, h.must("", "look")); got != "7" {
		t.Fatalf("no flag after the seed: %s", got)
	}
	h.must("", "look", "--limit", "7")
	if n := len(h.logLines()); n != 1 {
		t.Fatalf("an unchanged seed logged again: %d lines", n)
	}
	// While look seeds it, the planner's flag follows its memory.
	h.must("", "look", "--limit", "9")
	if b := h.board(); *b.Limit != 9 {
		t.Fatalf("follow: %+v", b)
	}
	// Once rules set it, the flag is ignored for good.
	h.must("", "rules", "set", "limit", "3")
	out := h.must("", "look", "--limit", "7")
	if lookLimitOf(t, out) != "3" || !strings.Contains(out, "alert: --limit 7 ignored: limit 3 set by planner with rules; drop --limit") {
		t.Fatalf("after rules set:\n%s", out)
	}
	if b := h.board(); *b.Limit != 3 || b.LimitVia != "rules" {
		t.Fatalf("board moved: %+v", b)
	}
}

// A person's --limit 0, to look without handing out, sets nothing.
func TestLookPersonLimitIsOneOff(t *testing.T) {
	h := newRulesHarness(t, "planner")
	h.must("", "look", "--limit", "5")
	before, _ := os.ReadFile(filepath.Join(h.dir, boardFile))
	out := h.must("", "look", "--limit", "0", "--actor", "paul")
	if lookLimitOf(t, out) != "0" || !strings.Contains(out, "alert: --limit 0 used once; standing limit 5") {
		t.Fatalf("one-off:\n%s", out)
	}
	if after, _ := os.ReadFile(filepath.Join(h.dir, boardFile)); string(after) != string(before) {
		t.Fatalf("a one-off wrote board.json: %s", after)
	}
	if code, _ := h.try("", "look", "--limit", "2", "--actor", "reviewer"); code != 2 {
		t.Fatal("a reviewer's --limit passed")
	}
}

func TestJudgeStdinSeedsRules(t *testing.T) {
	h := newRulesHarness(t, "planner")
	out := h.must("api/: senior only\nRuns at once: up to 20, passed to each look as `--limit`.\n", "look", "--limit", "2", "--judge")
	if !strings.Contains(out, "alert: dropped the runs-at-once memory line: the limit lives in rules") {
		t.Fatalf("no drop alert:\n%s", out)
	}
	via, lines := h.rules()
	if via != "look" || !slices.Equal(lines, []string{"api/: senior only (set by planner, " + today() + ")"}) {
		t.Fatalf("seeded %s %q", via, lines)
	}
	h.must("api/: senior only\nnew one\n", "look", "--judge")
	if _, lines := h.rules(); len(lines) != 2 || lines[0] != "api/: senior only (set by planner, "+today()+")" {
		t.Fatalf("follow: %q", lines)
	}
	h.must("set by rules\n", "rules", "text")
	out = h.must("something else\n", "look", "--judge")
	if !strings.Contains(out, "alert: judge stdin ignored: rules set with rules text; drop the heredoc") {
		t.Fatalf("no ignore alert:\n%s", out)
	}
	if _, lines := h.rules(); !slices.Equal(lines, []string{"set by rules (set by planner, " + today() + ")"}) {
		t.Fatalf("stdin changed rules.md after rules text: %q", lines)
	}
}

// TestLookBoardSeedRace: looks that seed the limit race rules set limit.
// The file stays whole, and once a rules write lands no look write
// follows it.
func TestLookBoardSeedRace(t *testing.T) {
	h := newRulesHarness(t, "planner")
	var wg sync.WaitGroup
	errs := make(chan string, 100)
	done := make(chan struct{})
	for i := range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if code, out := h.try("", "look", "--limit", strconv.Itoa(i+1)); code != 0 {
				errs <- out
			}
		}()
		go func() {
			defer wg.Done()
			if code, out := h.try("", "rules", "set", "limit", strconv.Itoa(100+i), "--actor", fmt.Sprintf("person%d", i)); code != 0 {
				errs <- out
			}
		}()
	}
	var rg sync.WaitGroup
	for range 10 {
		rg.Add(1)
		go func() {
			defer rg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				raw, err := os.ReadFile(filepath.Join(h.dir, boardFile))
				if err == nil && !json.Valid(raw) {
					errs <- fmt.Sprintf("half a board.json: %q", raw)
				}
			}
		}()
	}
	wg.Wait()
	close(done)
	rg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	ruled := false
	for _, l := range h.logLines() {
		person := strings.Contains(l, " person")
		if ruled && !person {
			t.Fatalf("a look wrote after rules set the limit: %q", h.logLines())
		}
		ruled = ruled || person
	}
	if b := h.board(); b.LimitVia != "rules" || *b.Limit < 100 {
		t.Fatalf("board %+v", b)
	}
}

func TestDoctorWarnsMissingBoardAndRules(t *testing.T) {
	h := newRulesHarness(t, "planner")
	check := func() (bool, string) {
		var doc struct {
			Checks []struct {
				Name, Detail string
				Warn         bool
			}
		}
		h.ok(&doc, "doctor")
		for _, c := range doc.Checks {
			if c.Name == "rules" {
				return c.Warn, c.Detail
			}
		}
		t.Fatal("no rules check")
		return false, ""
	}
	if warn, d := check(); !warn || !strings.Contains(d, "no limit in board.json") || !strings.Contains(d, "no rules.md") {
		t.Fatalf("fresh: %v %s", warn, d)
	}
	h.must("", "rules", "set", "limit", "3")
	h.must("", "rules", "text")
	if warn, d := check(); warn {
		t.Fatalf("both set: %s", d)
	}
}

// rulesSupervisor stands in for a run's supervisor: it marks each run it
// gets as running, with no process, so startRun returns at once, and
// returns the runs folder. A test that calls it must not run in parallel.
func rulesSupervisor(h *harness) string {
	h.t.Helper()
	dir := filepath.Join(h.dir, "runs")
	old := startSupervisor
	h.t.Cleanup(func() { startSupervisor = old })
	startSupervisor = func(sup *exec.Cmd) error {
		r, err := runs.Load(dir, sup.Args[2])
		if err != nil {
			return err
		}
		r.State = runs.Running
		return runs.Save(dir, r)
	}
	return dir
}

// startedRun is the run a job printed it started, with its prompt as lines.
func startedRun(h *harness, dir, stdin string, args ...string) (runs.Record, []string) {
	h.t.Helper()
	var s struct{ Run runs.Record }
	code, out, errs := h.run(stdin, append([]string{"job", "--json"}, args...)...)
	if code != 0 {
		h.t.Fatalf("job %v: exit %d\n%s%s", args, code, out, errs)
	}
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		h.t.Fatalf("job %v: %v: %s", args, err, out)
	}
	r, err := runs.Load(dir, s.Run.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return r, promptOf(h, dir, r.ID)
}

func promptOf(h *harness, dir, id string) []string {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, id+".in"))
	if err != nil {
		h.t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// With no rules.md, look --judge starts no judge and frees the rows it
// claimed; an empty rules.md gives the judge "none" as its rules.
func TestJudgeNoRulesFileStartsNoJudge(t *testing.T) {
	h := newRulesHarness(t, "planner")
	dir := rulesSupervisor(h)
	h.ok(nil, "create", "--title", "x")
	h.ok(nil, "transition", "T-1", "--to", "blocked")
	if code, out := h.try("Escalation: build at abc: which key?\nReturn-role: engineer", "comment", "T-1", "--body-file", "-"); code != 0 {
		t.Fatal(out)
	}
	code, out := h.try("", "look", "--limit", "2", "--judge")
	if code == 0 || strings.Contains(out, "judging:") || !strings.Contains(out, "judge: no maestro/rules.md") {
		t.Fatalf("no rules.md: exit %d\n%s", code, out)
	}
	if fileExists(filepath.Join(h.dir, "handouts", "T-1.judge")) {
		t.Fatal("a judge row stayed claimed with no judge")
	}
	if ids, _ := filepath.Glob(filepath.Join(dir, "R-*.in")); len(ids) != 0 {
		t.Fatalf("a judge run started with no rules.md: %q", ids)
	}
	os.WriteFile(filepath.Join(h.dir, rulesFile), []byte("# via: rules\n"), 0o644)
	var j []string
	for _, l := range strings.Split(h.must("", "look", "--limit", "2", "--judge"), "\n") {
		if strings.HasPrefix(l, "judging: ") {
			j = append(j, l)
		}
	}
	if len(j) != 1 {
		t.Fatalf("empty rules.md: %q", j)
	}
	s := promptOf(h, dir, strings.Fields(strings.TrimPrefix(j[0], "judging: "))[0])
	if len(s) < 2 || s[len(s)-2] != "Standing rules:" || s[len(s)-1] != "none" {
		t.Fatalf("judge prompt %q", s)
	}
}

// A QA run waits in a slot group of 6 by default, of qa_slots once rules
// set it, and of --slots over both.
func TestQASlotsOrder(t *testing.T) {
	h := newRulesHarness(t, "qa-lead")
	dir := rulesSupervisor(h)
	for _, id := range []string{"T-1", "T-2", "T-3"} {
		h.ok(nil, "create", "--title", "x")
		h.ok(nil, "transition", id, "--to", "in_progress")
		h.ok(nil, "transition", id, "--to", "in_review")
	}
	max := func(r runs.Record) string {
		i := slices.Index(r.Command, "slot")
		if i < 0 {
			t.Fatalf("no slot: %q", r.Command)
		}
		return flagValue(r.Command[i:], "--max")
	}
	if r, _ := startedRun(h, dir, "", "qa", "T-1"); max(r) != "6" {
		t.Fatalf("default: %q", r.Command)
	}
	h.must("", "rules", "set", "qa_slots", "4")
	if r, _ := startedRun(h, dir, "", "qa", "T-2"); max(r) != "4" {
		t.Fatalf("qa_slots: %q", r.Command)
	}
	if r, _ := startedRun(h, dir, "", "qa", "T-3", "--slots", "2"); max(r) != "2" {
		t.Fatalf("flag: %q", r.Command)
	}
}

// The plan and rule prompts end with the standing rules, once there are any.
func TestPlanAndRulePromptsCarryRules(t *testing.T) {
	h := newRulesHarness(t, "planner")
	dir := rulesSupervisor(h)
	h.ok(nil, "create", "--title", "x")
	if _, s := startedRun(h, dir, "goal g: ship it\n", "plan"); slices.Contains(s, "Standing rules:") {
		t.Fatalf("no rules.md, yet the plan prompt has rules: %q", s)
	}
	h.must("api/: senior only\n", "rules", "text")
	rule := "api/: senior only (set by planner, " + today() + ")"
	if _, s := startedRun(h, dir, "goal g: ship it\n", "plan"); !slices.Equal(s, []string{"/maestro-planner goal g: ship it", "Standing rules:", rule}) {
		t.Fatalf("plan prompt %q", s)
	}
	if _, s := startedRun(h, dir, "drop it\n", "rule", "T-1"); !slices.Equal(s, []string{"/maestro-planner rule T-1: drop it", "Standing rules:", rule}) {
		t.Fatalf("rule prompt %q", s)
	}
}

// rules add refuses a rule that names nothing that lifts it, and writes
// none of a list that holds one; text, the whole list, takes any.
func TestRulesAddNeedsLift(t *testing.T) {
	h := newRulesHarness(t, "planner")
	h.must("old rule\n", "rules", "text")
	for _, in := range []string{"api/: senior only\n", "a. Lifts when: never\nb: no lift\n", "uplifts the team\n"} {
		code, out := h.try(in, "rules", "add")
		if code != 2 || !strings.Contains(out, "says nothing lifts it") {
			t.Fatalf("add %q: exit %d %s", in, code, out)
		}
	}
	if _, got := h.rules(); len(got) != 1 || len(h.logLines()) != 1 {
		t.Fatalf("a refused add wrote: %q, %d log lines", got, len(h.logLines()))
	}
	for _, in := range []string{"a. Lifts when T-521 merges.\n", "b. Lifts: never.\n", "c. lifts when the owner says\n"} {
		h.must(in, "rules", "add")
	}
	if _, got := h.rules(); len(got) != 4 {
		t.Fatalf("after adds: %q", got)
	}
}

// Past 20 rules or 8 KB, a write warns and still writes.
func TestRulesBudgetWarns(t *testing.T) {
	h := newRulesHarness(t, "planner")
	var list strings.Builder
	for i := range ruleBudget {
		fmt.Fprintf(&list, "rule %d\n", i)
	}
	if out := h.must(list.String(), "rules", "text"); strings.Contains(out, "warn:") {
		t.Fatalf("%d rules warned:\n%s", ruleBudget, out)
	}
	out := h.must("one more. Lifts when: never\n", "rules", "add")
	if !strings.Contains(out, "warn: 21 rules") || !strings.Contains(out, "job retro") {
		t.Fatalf("21 rules:\n%s", out)
	}
	if _, got := h.rules(); len(got) != ruleBudget+1 {
		t.Fatalf("the warned add wrote no rule: %d", len(got))
	}
	var res struct{ Warn string }
	h.ok(&res, "rules", "drop", "1", "--why", "lifted")
	if res.Warn != "" {
		t.Fatalf("back to 20: warn %q", res.Warn)
	}
	big := strings.Repeat("x", 9000) + ". Lifts when: never\n"
	if out := h.must(big, "rules", "add"); !strings.Contains(out, "warn: 21 rules") {
		t.Fatalf("9 KB:\n%s", out)
	}
	h.must("", "rules", "drop", "21", "--why", "too long")
	h.must("", "rules", "drop", "1", "--why", "x")
	if out := h.must(big, "rules", "add"); !strings.Contains(out, "warn: 20 rules, 9.") {
		t.Fatalf("20 rules, 9 KB:\n%s", out)
	}
}

// rules.log keeps what changed and why beside each change's hashes; a
// drop must say why.
func TestRulesLogKeepsWhy(t *testing.T) {
	h := newRulesHarness(t, "planner")
	h.must("", "rules", "set", "limit", "2", "--why", "inodes at 12%")
	h.must("first. Lifts when T-9 merges\n", "rules", "add", "--why", "T-9 leaks temp dirs")
	if code, out := h.try("", "rules", "drop", "1"); code != 2 || !strings.Contains(out, "say why with --why") {
		t.Fatalf("drop with no why: exit %d %s", code, out)
	}
	if code, out := h.try("", "rules", "drop", "1", "--why", "a\x07b"); code != 2 {
		t.Fatalf("a why with a control character: exit %d %s", code, out)
	}
	h.must("", "rules", "drop", "1", "--why", "T-9\n merged")
	h.env["MAESTRO_TICKET_ACTOR"] = "chief-of-staff"
	h.must("", "rules", "set", "qa_slots", "3")
	got := h.logLines()
	rule := `"first. Lifts when T-9 merges (set by planner, ` + today() + `)"`
	for i, want := range []string{
		` limit none -> 2 why: inodes at 12%$`,
		` text none -> [0-9a-f]{12} added: ` + regexp.QuoteMeta(rule) + ` why: T-9 leaks temp dirs$`,
		` text [0-9a-f]{12} -> none dropped: ` + regexp.QuoteMeta(rule) + ` why: T-9 merged$`,
		` chief-of-staff qa_slots none -> 3$`,
	} {
		if i >= len(got) || !regexp.MustCompile(want).MatchString(got[i]) {
			t.Fatalf("log line %d: want %s, log:\n%s", i, want, strings.Join(got, "\n"))
		}
	}
}

// job retro runs /maestro-retro on Opus with the standing rules after it,
// on no ticket.
func TestRetroJobCarriesRules(t *testing.T) {
	h := newRulesHarness(t, "chief-of-staff")
	dir := rulesSupervisor(h)
	h.must("api/: senior only. Lifts when: never\n", "rules", "add")
	r, s := startedRun(h, dir, "", "retro")
	rule := "api/: senior only. Lifts when: never (set by chief-of-staff, " + today() + ")"
	if !slices.Equal(s, []string{"/maestro-retro", "Standing rules:", rule}) {
		t.Fatalf("retro prompt %q", s)
	}
	if r.Ticket != "" || flagValue(r.Command, "--model") != modelOpus {
		t.Fatalf("retro run: ticket %q, command %q", r.Ticket, r.Command)
	}
	if code, out := h.try("", "job", "retro", "T-1"); code != 2 || !strings.Contains(out, "takes no ticket id") {
		t.Fatalf("retro with an id: exit %d %s", code, out)
	}
}
