//go:build unix

package cli

import (
	"encoding/json"
	"fmt"
	"os"
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
	h.must("three (set by owner, 2026-01-01)\n", "rules", "add")
	_, got := h.rules()
	if !slices.Equal(got[:2], before) || got[2] != "three (set by planner, "+today()+")" {
		t.Fatalf("after add: %q", got)
	}
	h.must("", "rules", "drop", "1")
	_, got2 := h.rules()
	if !slices.Equal(got2, got[1:]) {
		t.Fatalf("after drop: %q", got2)
	}
	if code, _ := h.try("", "rules", "drop", "9"); code != 3 {
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
