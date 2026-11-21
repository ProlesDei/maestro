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

// A reading's rise teaches a job's cost only when one run explains it.
func TestLearnEstimate(t *testing.T) {
	from := time.Now().UTC().Add(-time.Hour)
	to := from.Add(time.Hour)
	end := from.Add(30 * time.Minute)
	run := func(alias string, start time.Time, end *time.Time) runs.Record {
		return runs.Record{Profile: alias, Harness: "claude", Job: "review", State: runs.Done, StartedAt: start, EndedAt: end, PID: 1}
	}
	e := estimates{}
	learnEstimate(e, "max", "weekly", from, to, 6, []runs.Record{run("max", from.Add(time.Minute), &end)})
	if e["claude/review/weekly"] != 6 {
		t.Fatalf("first sample: %v", e)
	}
	learnEstimate(e, "max", "weekly", from, to, 2, []runs.Record{run("max", from.Add(time.Minute), &end)})
	if got := e["claude/review/weekly"]; got != 4.8 {
		t.Fatalf("moving average: %v; want 4.8", got)
	}
	before := e["claude/review/weekly"]
	// Two runs, one still running, or one started before the first reading: no sample.
	learnEstimate(e, "max", "weekly", from, to, 30, []runs.Record{run("max", from.Add(time.Minute), &end), run("max", from.Add(2*time.Minute), &end)})
	learnEstimate(e, "max", "weekly", from, to, 30, []runs.Record{run("max", from.Add(time.Minute), nil)})
	learnEstimate(e, "max", "weekly", from, to, 30, []runs.Record{run("max", from.Add(-time.Minute), &end)})
	if e["claude/review/weekly"] != before {
		t.Fatalf("learned from a reading no single run explains: %v", e)
	}
	dir := t.TempDir()
	if got := estimateFor(dir, "claude", "review"); got != defaultEstimate {
		t.Fatalf("no readings: %v", got)
	}
}
