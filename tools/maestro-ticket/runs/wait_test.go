//go:build unix

package runs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mark writes a wait mark for id that started at from and was last
// touched at to.
func mark(t *testing.T, dir, id, kind string, from, to time.Time) {
	t.Helper()
	name := filepath.Join(dir, fmt.Sprintf("%s.%s.%d-%d", id, kind, from.UnixNano(), 1))
	if err := os.WriteFile(name, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(name, to, to); err != nil {
		t.Fatal(err)
	}
}

// Overlapping waits count once; a live wait counts to now; one whose
// waiter stopped touching it (killed) counts to its last touch; time
// before the run began counts not at all; another run's waits aren't this
// one's.
func TestSlotWait(t *testing.T) {
	dir := t.TempDir()
	id := "R-aaaaaaaa"
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	now := at(60)
	mark(t, dir, id, "waited", at(10), at(20))     // 10m
	mark(t, dir, id, "waited", at(15), at(25))     // overlaps: +5m
	mark(t, dir, id, "wait", at(30), at(35))       // dead at 35: +5m
	mark(t, dir, id, "wait", at(50), now)          // live: +10m
	mark(t, dir, id, "waited", at(-10), at(2))     // before the start: +2m
	mark(t, dir, "R-bbbbbbbb", "wait", at(0), now) // another run's
	if got, want := SlotWait(dir, id, t0, now), 32*time.Minute; got != want {
		t.Fatalf("SlotWait = %s; want %s", got, want)
	}
	removeWaits(dir, id)
	if got := SlotWait(dir, id, t0, now); got != 0 {
		t.Fatalf("after removeWaits: %s", got)
	}
	if left, _ := os.ReadDir(dir); len(left) != 1 {
		t.Fatalf("removeWaits left %d files; want only the other run's", len(left))
	}
}

// StartWait marks nothing for a run this folder doesn't hold, and End
// closes the mark it made.
func TestStartWaitNeedsTheRun(t *testing.T) {
	dir := t.TempDir()
	if w, err := StartWait(dir, "R-aaaaaaaa", time.Now()); w != nil || err != nil {
		t.Fatalf("no record: %v, %v", w, err)
	}
	if w, err := StartWait(dir, "../../x", time.Now()); w != nil || err != nil {
		t.Fatalf("bad id: %v, %v", w, err)
	}
	r, err := Create(dir, Record{Launcher: "qa-lead", Limit: "1h"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-2 * time.Second)
	w, err := StartWait(dir, r.ID, start)
	if err != nil || w == nil {
		t.Fatalf("StartWait: %v, %v", w, err)
	}
	w.End(start.Add(time.Second))
	if got := SlotWait(dir, r.ID, start.Add(-time.Minute), time.Now()); got != time.Second {
		t.Fatalf("SlotWait after End = %s; want 1s", got)
	}
}

// The time limit leaves out slot waits, but only up to the cap, and the
// result says what it left out.
func TestSuperviseLeavesOutSlotWaits(t *testing.T) {
	old := maxSlotCredit
	maxSlotCredit = 2 * time.Second
	t.Cleanup(func() { maxSlotCredit = old })
	dir := t.TempDir()
	r, err := Create(dir, Record{Launcher: "qa-lead", Limit: "1s", Command: []string{"sleep", "30"}, Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, err := StartWait(dir, r.ID, time.Now())
	if err != nil || w == nil {
		t.Fatalf("StartWait: %v, %v", w, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // a waiter that never gets its slot
		for ctx.Err() == nil {
			w.Touch(time.Now())
			time.Sleep(200 * time.Millisecond)
		}
	}()
	begun := time.Now()
	if err := Supervise(context.Background(), dir, r.ID, Options{}); err != nil {
		t.Fatal(err)
	}
	took := time.Since(begun)
	got, err := Load(dir, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TimedOut || !strings.Contains(got.Error, "the 1s time limit ended it, which left out 2s waiting for a slot (the most it leaves out)") {
		t.Fatalf("record: %s: %s", got.State, got.Error)
	}
	if took < 3*time.Second || took > 10*time.Second {
		t.Fatalf("took %s; want the 1s limit plus the 2s cap", took)
	}
	if n := len(waits(dir, r.ID)); n != 0 {
		t.Fatalf("%d wait marks left after the run", n)
	}
}
