//go:build unix

package runs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
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
	if w, err := StartWait(dir, "R-aaaaaaaa", time.Now(), false); w != nil || err != nil {
		t.Fatalf("no record: %v, %v", w, err)
	}
	if w, err := StartWait(dir, "../../x", time.Now(), false); w != nil || err != nil {
		t.Fatalf("bad id: %v, %v", w, err)
	}
	r, err := Create(dir, Record{Launcher: "qa-lead", Limit: "1h"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-2 * time.Second)
	w, err := StartWait(dir, r.ID, start, false)
	if err != nil || w == nil {
		t.Fatalf("StartWait: %v, %v", w, err)
	}
	w.End(start.Add(time.Second))
	if got := SlotWait(dir, r.ID, start.Add(-time.Minute), time.Now()); got != time.Second {
		t.Fatalf("SlotWait after End = %s; want 1s", got)
	}
}

// A run that stopped on a limit rings its bot once more as it lifts.
func TestSuperviseRingsAgain(t *testing.T) {
	dir := t.TempDir()
	r, err := Create(dir, Record{Launcher: "planner", Limit: "10s", Command: []string{"true"}, Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var rings atomic.Int32
	count := Hook{Wake: func(context.Context, Notification) error { rings.Add(1); return nil }}
	begun := time.Now()
	err = Supervise(context.Background(), dir, r.ID, Options{
		Rings:     []time.Duration{0, 300 * time.Millisecond, 600 * time.Millisecond}, // only the first, then the lift
		Hook:      func(string) (Hook, error) { return count, nil },
		RingAgain: func(Record) time.Time { return begun.Add(1500 * time.Millisecond) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := rings.Load(); n != 2 || time.Since(begun) < 1500*time.Millisecond {
		t.Fatalf("%d rings after %s; want the first and one more at the lift", n, time.Since(begun))
	}
}

// A session's wait to start counts up to maxSlotCredit; a wait inside the
// session only up to twice the limit, so a session that holds its context
// in a queue ends at three times its limit.
func TestCreditCapsWaitsInsideTheSession(t *testing.T) {
	dir := t.TempDir()
	id := "R-aaaaaaaa"
	t0 := time.Now().Add(-10 * time.Hour).Truncate(time.Second)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	now := at(600)
	start := filepath.Join(dir, fmt.Sprintf("%s.waited.%d-1%s", id, at(0).UnixNano(), startMark))
	if err := os.WriteFile(start, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(start, at(180), at(180)); err != nil { // 3h to start
		t.Fatal(err)
	}
	mark(t, dir, id, "waited", at(200), at(400)) // 200m inside
	got, capped := Credit(dir, id, t0, now, time.Hour)
	if want := 3*time.Hour + 2*time.Hour; got != want || !capped {
		t.Fatalf("Credit = %s, capped %v; want %s, capped", got, capped, want)
	}
	if got, capped := Credit(dir, id, t0, now, 4*time.Hour); got != 3*time.Hour+200*time.Minute || capped {
		t.Fatalf("4h limit: Credit = %s, capped %v; want all of it", got, capped)
	}
}

// A waiter's touch after a freeze ends its mark at the last touch and
// starts a new one, so the frozen hours earn the run no credit.
func TestTouchSplitsAtAFreeze(t *testing.T) {
	dir := t.TempDir()
	r, err := Create(dir, Record{Launcher: "qa-lead", Limit: "1h"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().Add(-5 * time.Hour).Truncate(time.Second)
	w, err := StartWait(dir, r.ID, t0, false)
	if err != nil || w == nil {
		t.Fatalf("StartWait: %v, %v", w, err)
	}
	w.Touch(t0.Add(time.Second))
	w.Touch(t0.Add(2 * time.Second))
	woke := t0.Add(4*time.Hour + 2*time.Second)
	w.Touch(woke)
	w.Touch(woke.Add(time.Second))
	if got, want := SlotWait(dir, r.ID, t0.Add(-time.Minute), woke.Add(time.Second)), 3*time.Second; got != want {
		t.Fatalf("SlotWait = %s; want %s: the 2s before the freeze and the 1s after", got, want)
	}
	if n := len(waits(dir, r.ID)); n != 2 {
		t.Fatalf("%d marks; want the one the freeze ended and a new one", n)
	}
}

// A wait that ends right after a freeze, with no touch between, ends at
// its last touch, so the freeze earns no credit either.
func TestEndAfterAFreeze(t *testing.T) {
	dir := t.TempDir()
	r, err := Create(dir, Record{Launcher: "qa-lead", Limit: "1h"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().Add(-5 * time.Hour).Truncate(time.Second)
	w, err := StartWait(dir, r.ID, t0, true)
	if err != nil || w == nil {
		t.Fatalf("StartWait: %v, %v", w, err)
	}
	w.Touch(t0.Add(time.Second))
	if !w.End(t0.Add(4 * time.Hour)) {
		t.Fatal("End found no mark")
	}
	if got, _ := Credit(dir, r.ID, t0.Add(-time.Minute), t0.Add(4*time.Hour), time.Hour); got != time.Second {
		t.Fatalf("Credit = %s; want the 1s before the freeze", got)
	}
}

// AfterRing runs once, right after the first ring and before the rest,
// with that ring's error; with a webhook it can't look up, once with that
// error; with no webhook, once after the end.
func TestSuperviseAfterRing(t *testing.T) {
	dir := t.TempDir()
	var rings, status atomic.Int32
	hook := Hook{Wake: func(context.Context, Notification) error {
		rings.Add(1)
		if status.Load() != 200 {
			return errors.New("down")
		}
		return nil
	}}
	for _, kind := range []string{"rang", "ok", "broken", "none"} {
		r, err := Create(dir, Record{Launcher: "planner", Limit: "10s", Command: []string{"true"}, Dir: dir}, nil)
		if err != nil {
			t.Fatal(err)
		}
		rings.Store(0)
		var calls []string
		o := Options{
			Rings: []time.Duration{0, 200 * time.Millisecond},
			AfterRing: func(r Record, err error) {
				calls = append(calls, fmt.Sprintf("rings %d, rang %v, %s", rings.Load(), r.Rang, RingErrCode(err)))
			},
		}
		want := "rings 0, rang false, none"
		status.Store(503)
		switch kind {
		case "rang":
			o.Hook = func(string) (Hook, error) { return hook, nil }
			status.Store(200)
			want = "rings 1, rang true, none"
		case "ok":
			o.Hook = func(string) (Hook, error) { return hook, nil }
			want = "rings 1, rang false, other"
		case "broken":
			o.Hook = func(string) (Hook, error) { return Hook{}, errors.New("bad notify file") }
			want = "rings 0, rang false, other"
		}
		if err := Supervise(context.Background(), dir, r.ID, o); err != nil {
			t.Fatal(err)
		}
		if len(calls) != 1 || calls[0] != want {
			t.Fatalf("hook %s: AfterRing calls %q; want one: %s", kind, calls, want)
		}
	}
}
