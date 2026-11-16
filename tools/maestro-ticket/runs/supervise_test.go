//go:build unix

package runs

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// superviseEnd runs a record's command under Supervise and returns the
// record OnEnd got.
func superviseEnd(t *testing.T, dir string, rec Record, o Options) Record {
	t.Helper()
	rec.Launcher, rec.Dir = "planner", dir
	r, err := Create(dir, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got Record
	o.OnEnd = func(r Record) { got = r }
	if err := Supervise(context.Background(), dir, r.ID, o); err != nil {
		t.Fatal(err)
	}
	if got.End == nil {
		t.Fatalf("OnEnd got no end facts: %+v", got)
	}
	return got
}

// The end facts reach OnEnd and nothing else: not the saved record, not
// its JSON.
func TestEndFactsNotInRecordJSON(t *testing.T) {
	dir := t.TempDir()
	got := superviseEnd(t, dir, Record{Limit: "10s", Command: []string{"true"}}, Options{})
	plain := got
	plain.End = nil
	with, _ := json.Marshal(got)
	without, _ := json.Marshal(plain)
	if !bytes.Equal(with, without) {
		t.Fatalf("End shows in the JSON:\n%s\n%s", with, without)
	}
	b, err := os.ReadFile(path(dir, got.ID, ".json"))
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(b, &keys); err != nil {
		t.Fatal(err)
	}
	for k := range keys {
		if k == "End" || strings.Contains(strings.ToLower(k), "wait") || strings.Contains(strings.ToLower(k), "stop") || strings.Contains(strings.ToLower(k), "escaped") {
			t.Fatalf("the saved record holds %s:\n%s", k, b)
		}
	}
	if l, _ := Load(dir, got.ID); l.End != nil {
		t.Fatal("a loaded record has end facts")
	}
}

// wait_ms is Credit's figure once the command ends, read before the
// marks go: a wait that ended counts to its end; a cap shows as capped.
func TestWaitMsCredit(t *testing.T) {
	dir := t.TempDir()
	r, err := Create(dir, Record{Launcher: "qa-lead", Limit: "10s", Command: []string{"sleep", "1"}, Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, err := StartWait(dir, r.ID, time.Now(), true)
	if err != nil || w == nil {
		t.Fatalf("StartWait: %v, %v", w, err)
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		w.End(time.Now())
	}()
	var got Record
	if err := Supervise(context.Background(), dir, r.ID, Options{OnEnd: func(r Record) { got = r }}); err != nil {
		t.Fatal(err)
	}
	if e := got.End; e == nil || e.WaitMs < 300 || e.WaitMs > 900 || e.WaitCapped || e.Stop != StopNone {
		t.Fatalf("end facts %+v; want about 500 ms of wait, not capped, no stop", e)
	}
	if n := len(waits(dir, r.ID)); n != 0 {
		t.Fatalf("%d wait marks left", n)
	}

	old := maxSlotCredit
	maxSlotCredit = time.Second
	t.Cleanup(func() { maxSlotCredit = old })
	r, err = Create(dir, Record{Launcher: "qa-lead", Limit: "1s", Command: []string{"sleep", "30"}, Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	live, err := StartWait(dir, r.ID, time.Now(), true)
	if err != nil || live == nil {
		t.Fatalf("StartWait: %v, %v", live, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // never gets its slot
		for ctx.Err() == nil {
			live.Touch(time.Now())
			time.Sleep(200 * time.Millisecond)
		}
	}()
	if err := Supervise(context.Background(), dir, r.ID, Options{OnEnd: func(r Record) { got = r }}); err != nil {
		t.Fatal(err)
	}
	if e := got.End; e == nil || e.WaitMs != 1000 || !e.WaitCapped || got.State != TimedOut {
		t.Fatalf("%s, end facts %+v; want the 1s cap, capped", got.State, e)
	}
}

// How the supervisor ended the command: on its own, SIGTERM, or SIGKILL
// once the grace ran out; and how many of its processes had left its
// group when the stop came.
func TestStopKind(t *testing.T) {
	old := killGrace
	killGrace = 500 * time.Millisecond
	t.Cleanup(func() { killGrace = old })
	dir := t.TempDir()
	for _, c := range []struct {
		name    string
		cmd     []string
		stop    string
		escaped int
	}{
		{"own", []string{"true"}, StopNone, 0},
		{"term", []string{"sleep", "30"}, StopTerm, 0},
		// An ignored signal stays ignored through exec, so sleep ignores
		// it too.
		{"kill", []string{"sh", "-c", "trap '' TERM; sleep 30"}, StopKill, 0},
	} {
		got := superviseEnd(t, dir, Record{Limit: "1s", Command: c.cmd}, Options{})
		if e := got.End; e.Stop != c.stop || e.Escaped != c.escaped {
			t.Errorf("%s: end facts %+v; want stop %s, %d escaped", c.name, e, c.stop, c.escaped)
		}
	}
	if runtime.GOOS != "linux" {
		return // finding what left the group needs /proc
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid")
	}
	got := superviseEnd(t, dir, Record{Limit: "1s", Command: []string{"sh", "-c", "setsid sleep 30 & setsid sleep 30 & sleep 30"}}, Options{})
	if e := got.End; e.Stop != StopTerm || e.Escaped != 2 {
		t.Fatalf("end facts %+v; want term, 2 escaped", e)
	}
	if runAlive(got.ID) {
		t.Fatal("an escaped process outlived its run")
	}
}

// Prune takes a run's side file, and a temp copy a killed launch left,
// with the rest of its files.
func TestPruneRemovesSideFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	r, _ := Create(dir, Record{Launcher: "planner", Limit: "2h", Command: []string{"true"}, StartedAt: now}, nil)
	if err := os.WriteFile(path(dir, r.ID, ".stats"), []byte(`{"why":"fixed"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "."+r.ID+".stats.tmp-123")
	if err := os.WriteFile(tmp, []byte(`{"wh`), 0o644); err != nil {
		t.Fatal(err)
	}
	Ack(dir, r.ID, "planner", now)
	Prune(dir, time.Hour, now.Add(30*24*time.Hour))
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.Contains(e.Name(), r.ID) {
			t.Fatalf("pruned run left %s", e.Name())
		}
	}
}
