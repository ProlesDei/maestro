//go:build unix

package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// Every sub runs every job: Claude on the job's own model, Codex on Astra
// for Opus and Sol for Sonnet, all at high.
func TestPoolModel(t *testing.T) {
	for _, c := range []struct{ harness, model, want string }{
		{"claude", "opus", "opus"}, {"claude", "sonnet", "sonnet"}, {"claude", codexSonnet, "sonnet"}, {"claude", "", "opus"},
		{"codex", "opus", codexOpus}, {"codex", "sonnet", codexSonnet}, {"codex", codexOpus, codexOpus}, {"codex", "", codexOpus},
	} {
		if got := poolModel(c.harness, c.model); got.Model != c.want || got.Effort != effortHigh {
			t.Errorf("%s on %q: %+v; want %s high", c.harness, c.model, got, c.want)
		}
	}
	native := []string{"maestro-ticket", "native-worker", "--profile", "pro", "--job", "build", "--model", codexSonnet, "--effort", effortHigh}
	if got := resumeCommand(native, "S1", poolModel("codex", modelOpus).Model); flagValue(got, "--model") != codexOpus || !poolAllows("codex", flagValue(got, "--model"), flagValue(got, "--effort")) {
		t.Fatalf("a Codex junior's --opus resume: %q", got)
	}
	if !poolAllows("codex", codexSonnet, "high") || poolAllows("codex", "opus", "high") || poolAllows("claude", "opus", "medium") {
		t.Fatal("poolAllows lets through a model or effort the pool doesn't run")
	}
}

// Code serves the worker roles, the chief is woken, and anyone else, such
// as a person, reads pickup.
func TestServing(t *testing.T) {
	for _, bot := range runs.CodeRoles {
		if h := runs.Serving(bot); !h.Code || h.CanRing() {
			t.Errorf("%s: %+v", bot, h)
		}
	}
	if h := runs.Serving(runs.ChiefRole); h.Code || h.CanRing() || !h.Served() {
		t.Errorf("chief: %+v", h)
	}
	if h := (&call{}).hook(runs.ChiefRole); !h.CanRing() {
		t.Error("the chief's hook can't ring")
	}
	if runs.Serving("person").Served() {
		t.Error("a person is served")
	}
}

// add puts a login in the pool with the 4% reserve unless told otherwise;
// a second alias for the same login, or a reused alias, refuses.
func TestPoolAddRemove(t *testing.T) {
	h := newRouteHarness(t)
	c := h.inProc("person")
	st := profileStatus{Alias: "max", Harness: "claude", Plan: "max", identity: "id-a"}
	if err := c.addProfile(st, "", defaultReserve); err != nil {
		t.Fatal(err)
	}
	dup := st
	dup.Alias = "max2"
	if err := c.addProfile(dup, "", defaultReserve); err == nil {
		t.Fatal("same login under a second alias")
	}
	other := profileStatus{Alias: "max", Harness: "claude", Plan: "max", identity: "id-b"}
	if err := c.addProfile(other, "", defaultReserve); err == nil {
		t.Fatal("alias taken by another login")
	}
	pro := profileStatus{Alias: "pro", Harness: "codex", Plan: "pro", identity: "id-c"}
	if err := c.addProfile(pro, "", 10); err != nil {
		t.Fatal(err)
	}
	ps, err := loadBindings(filepath.Join(h.dir, "profiles.json"))
	if err != nil || ps["max"].Reserve != 4 || ps["pro"].Reserve != 10 || len(ps) != 2 {
		t.Fatalf("pool %+v %v", ps, err)
	}
	if err := c.removeProfile("max"); err != nil {
		t.Fatal(err)
	}
	if err := c.removeProfile("max"); err == nil {
		t.Fatal("removed a sub twice")
	}
	if ps, _ = loadBindings(filepath.Join(h.dir, "profiles.json")); len(ps) != 1 {
		t.Fatalf("after remove: %+v", ps)
	}
}

// A paced job on a Codex sub runs the Codex model for the job's tier.
func TestPacedCodexModel(t *testing.T) {
	h := newRouteHarness(t)
	c := h.inProc("junior-engineer")
	loc, err := c.location()
	if err != nil {
		t.Fatal(err)
	}
	loc.boardID = "scratch"
	c.loc.v = loc
	root := h.dir
	if err := writeWholeMode(filepath.Join(root, "profiles.json"), []byte(`{"pro":{"harness":"codex","reserve_percent":4}}`), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := saveObservation(root, "pro", map[string]paceWindow{"weekly": {Used: 10, Reset: now.Add(time.Hour), Read: now, Source: "trial"}}); err != nil {
		t.Fatal(err)
	}
	for model, want := range map[string]string{"sonnet": codexSonnet, "opus": codexOpus} {
		r, err := c.createPacedRun(filepath.Join(root, "runs"), runs.Record{Job: "build", Launcher: "junior-engineer", StartedAt: now, Limit: "1h", Command: claudeCommand("S1", model, effortHigh, false)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := flagValue(r.Command, "--model"); r.Profile != "pro" || got != want || flagValue(r.Command, "--effort") != effortHigh || r.Estimate != defaultEstimate {
			t.Fatalf("%s build: profile %s, model %s, estimate %v", model, r.Profile, got, r.Estimate)
		}
	}
}

// A run's own first and last readings teach its job's cost, shared with
// the runs on its sub that ran beside it.
func TestLearnEstimate(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour)
	reset := start.Add(5 * time.Hour)
	r := runs.Record{ID: "R-aaaaaaaa", Profile: "max", Job: "review", State: runs.Running, StartedAt: start, PID: 1}
	var span meterSpan
	span.see(map[string]paceWindow{"five_hour": {Used: 10, Reset: reset, Read: start.Add(time.Minute)}})
	span.see(map[string]paceWindow{"five_hour": {Used: 13, Reset: reset, Read: start.Add(20 * time.Minute)}})
	span.see(map[string]paceWindow{"five_hour": {Used: 16, Reset: reset, Read: start.Add(40 * time.Minute)}})
	e := estimates{}
	learnEstimate(e, r, "claude", span, []runs.Record{r}, start.Add(time.Hour))
	if e["claude/review/five_hour"] != 6 {
		t.Fatalf("alone: %v; want the 6 points it saw rise", e)
	}
	// A second run on the sub at the same time takes half the next rise;
	// a run on another sub, or one that ended before, takes none.
	ended := start.Add(-time.Minute)
	list := []runs.Record{r,
		{ID: "R-bbbbbbbb", Profile: "max", State: runs.Running, StartedAt: start.Add(5 * time.Minute), PID: 2},
		{ID: "R-cccccccc", Profile: "pro", State: runs.Running, StartedAt: start, PID: 3},
		{ID: "R-dddddddd", Profile: "max", State: runs.Done, StartedAt: start.Add(-time.Hour), EndedAt: &ended, PID: 4},
	}
	learnEstimate(e, r, "claude", span, list, start.Add(time.Hour))
	if got := e["claude/review/five_hour"]; got != 5.1 {
		t.Fatalf("shared: %v; want 0.7*6 + 0.3*3 = 5.1", got)
	}
	// One reading, or a reset between them, teaches nothing.
	var one meterSpan
	one.see(map[string]paceWindow{"weekly": {Used: 10, Reset: reset, Read: start}})
	var reset2 meterSpan
	reset2.see(map[string]paceWindow{"weekly": {Used: 90, Reset: reset, Read: start}})
	reset2.see(map[string]paceWindow{"weekly": {Used: 1, Reset: reset.Add(time.Hour), Read: start.Add(time.Minute)}})
	learnEstimate(e, r, "claude", one, []runs.Record{r}, start.Add(time.Hour))
	learnEstimate(e, r, "claude", reset2, []runs.Record{r}, start.Add(time.Hour))
	if _, ok := e["claude/review/weekly"]; ok {
		t.Fatalf("learned from no span: %v", e)
	}
	dir := t.TempDir()
	if got := estimateFor(dir, "claude", "review"); got != defaultEstimate {
		t.Fatalf("no readings: %v", got)
	}
	if err := writeWholeMode(filepath.Join(dir, estimatesFile), []byte(`{"claude/review/five_hour":5.1,"claude/review/weekly":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := estimateFor(dir, "claude", "review"); got != 5.1 {
		t.Fatalf("learned: %v; want the costliest window", got)
	}
}

// A native worker's end writes what it learned beside the store, where
// the next admission reads it.
func TestLearnSpanWrites(t *testing.T) {
	h := newRouteHarness(t)
	c := h.inProc("reviewer")
	start := time.Now().UTC().Add(-time.Hour)
	reset := start.Add(5 * time.Hour)
	c.span.see(map[string]paceWindow{"five_hour": {Used: 10, Reset: reset, Read: start.Add(time.Minute)}})
	c.span.see(map[string]paceWindow{"five_hour": {Used: 18, Reset: reset, Read: start.Add(30 * time.Minute)}})
	r := runs.Record{ID: "R-aaaaaaaa", Profile: "pro", Job: "review", StartedAt: start}
	if err := c.learnSpan(h.dir, r, "codex"); err != nil {
		t.Fatal(err)
	}
	if got := estimateFor(h.dir, "codex", "review"); got != 8 {
		t.Fatalf("estimate after the run: %v; want 8", got)
	}
}
