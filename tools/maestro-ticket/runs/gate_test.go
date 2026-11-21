//go:build unix

package runs

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// hookServer counts the rings it gets and keeps each body.
type hookServer struct {
	mu     sync.Mutex
	bodies []Notification
	at     []time.Time
	on     func(Notification)
}

func newHookServer(t *testing.T) (*hookServer, Hook) {
	k := &hookServer{}
	return k, Hook{Wake: func(_ context.Context, n Notification) error {
		k.mu.Lock()
		k.bodies = append(k.bodies, n)
		k.at = append(k.at, time.Now())
		on := k.on
		k.mu.Unlock()
		if on != nil {
			on(n)
		}
		return nil
	}}
}

func (k *hookServer) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.bodies)
}

// since counts the rings that came at or after t.
func (k *hookServer) since(t time.Time) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, at := range k.at {
		if !at.Before(t) {
			n++
		}
	}
	return n
}

func (k *hookServer) last() Notification {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.bodies[len(k.bodies)-1]
}

// endedRuns writes n ended, settled runs of bot, ended at end.
func endedRuns(t *testing.T, dir, bot string, n int, end time.Time) []string {
	t.Helper()
	ids := []string{}
	for range n {
		r, err := Create(dir, Record{Launcher: bot, Limit: "1h", Command: []string{"true"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.State, r.EndedAt, r.Rang = Done, &end, true // rung: no orphan
		if err := Save(dir, r); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	return ids
}

func plainDigest(dir, bot string) DigestFunc {
	return func(now time.Time) (Notification, []string) {
		recs, _ := Unacked(dir, bot, now)
		n := NoticeFor(bot, recs)
		return n, n.Runs
	}
}

func setWindow(t *testing.T, d time.Duration) {
	was := GateWindow
	GateWindow = d
	t.Cleanup(func() { GateWindow = was })
}

// 300 ringers at once, on 300 results of one bot, within a minute: one
// ring, which names 10 and counts 290. At the window's end the 290 it
// only counted ring once more; nothing rings again in that window; and
// once all are acked, nothing rings at all.
func TestRingGate300(t *testing.T) {
	setWindow(t, 4*time.Minute)
	dir := t.TempDir()
	gates := filepath.Join(dir, "notify")
	t0 := time.Now().UTC().Round(time.Millisecond)
	ids := endedRuns(t, dir, "reviewer", 300, t0)
	k, h := newHookServer(t)
	build := plainDigest(dir, "reviewer")
	var covered sync.Map
	sweep := func(at time.Time, spread time.Duration) {
		var wg sync.WaitGroup
		for i, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				now := at.Add(spread * time.Duration(i) / time.Duration(len(ids)))
				g, err := RingBot(context.Background(), dir, gates, "reviewer", []string{id}, h, now, build)
				if err != nil {
					t.Error(err)
				}
				if g.Covers(id) {
					covered.Store(id, true)
				}
			}()
		}
		wg.Wait()
	}
	sweep(t0, time.Minute)
	if n := k.count(); n != 1 {
		t.Fatalf("%d rings in the first window; want 1", n)
	}
	if b := k.last(); len(b.Runs) != MaxRingRuns || b.More != 290 {
		t.Fatalf("first ring names %d, counts %d more; want 10 and 290", len(b.Runs), b.More)
	}
	n := 0
	covered.Range(func(any, any) bool { n++; return true })
	if n != MaxRingRuns {
		t.Fatalf("%d runs covered; want the 10 it named", n)
	}
	sweep(t0.Add(4*time.Minute), time.Minute)
	if n := k.count(); n != 2 {
		t.Fatalf("%d rings by the second window; want 2", n)
	}
	sweep(t0.Add(5*time.Minute), time.Minute)
	if n := k.count(); n != 2 {
		t.Fatalf("%d rings within the second window; want 2", n)
	}
	for _, id := range ids {
		Ack(dir, id, "reviewer", t0)
	}
	sweep(t0.Add(10*time.Minute), 0)
	if n := k.count(); n != 2 {
		t.Fatalf("%d rings once all were acked; want 2", n)
	}
}

// An ack of what the last ring listed opens the gate at once.
func TestRingGateOpensOnAck(t *testing.T) {
	setWindow(t, time.Hour)
	dir := t.TempDir()
	now := time.Now()
	a := endedRuns(t, dir, "planner", 1, now)
	k, h := newHookServer(t)
	build := plainDigest(dir, "planner")
	RingBot(context.Background(), dir, dir, "planner", a, h, now, build)
	b := endedRuns(t, dir, "planner", 1, now)
	if g, _ := RingBot(context.Background(), dir, dir, "planner", b, h, now, build); !g.Held() || k.count() != 1 {
		t.Fatalf("a second result in the window rang: %+v, %d rings", g, k.count())
	}
	Ack(dir, a[0], "planner", now)
	if g, _ := RingBot(context.Background(), dir, dir, "planner", b, h, now, build); !g.Rang || !g.Covers(b[0]) || k.count() != 2 {
		t.Fatalf("after the ack: %+v, %d rings", g, k.count())
	}
}

// A ring that failed holds the gate, so 300 ringers don't each wait on a
// webhook that is down, and covers nothing.
func TestRingGateFailedCoversNothing(t *testing.T) {
	setWindow(t, time.Hour)
	dir := t.TempDir()
	now := time.Now()
	ids := endedRuns(t, dir, "planner", 2, now)
	var tries atomic.Int32
	h := Hook{Wake: func(context.Context, Notification) error { tries.Add(1); return errors.New("down") }}
	build := plainDigest(dir, "planner")
	if g, err := RingBot(context.Background(), dir, dir, "planner", ids[:1], h, now, build); err == nil || !g.Tried || g.Rang || len(g.Covered) != 0 {
		t.Fatalf("failed ring: %+v %v", g, err)
	}
	if g, _ := RingBot(context.Background(), dir, dir, "planner", ids, h, now, build); !g.Held() || len(g.Covered) != 0 || tries.Load() != 1 {
		t.Fatalf("after a failed ring: %+v, %d tries", g, tries.Load())
	}
}

// A run that ends a little after a ring is held back, not lost: its
// supervisor rings it once the gate opens.
func TestRingGateSkippedNotLost(t *testing.T) {
	setWindow(t, 600*time.Millisecond)
	dir := t.TempDir()
	k, h := newHookServer(t)
	a := endedRuns(t, dir, "planner", 1, time.Now())
	first := time.Now().UTC()
	RingBot(context.Background(), dir, dir, "planner", a, h, first, plainDigest(dir, "planner"))
	r, err := Create(dir, Record{Launcher: "planner", Limit: "10s", Command: []string{"true"}, Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = Supervise(context.Background(), dir, r.ID, Options{
		Rings: []time.Duration{0},
		Hook:  func(string) (Hook, error) { return h, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if k.count() != 2 || !slices.Contains(k.last().Runs, r.ID) {
		t.Fatalf("%d rings; want the first and one for the held run: %+v", k.count(), k.bodies)
	}
	if got, _ := Load(dir, r.ID); !got.Rang || time.Since(first) < GateWindow {
		t.Fatalf("rang %v after %s; want rung after the window", got.Rang, time.Since(first))
	}
}

// 50 runs a usage limit stopped wait for the lift, every one of them
// listed: the lift rings their bot once, not 50 times.
func TestRingAgainOncePerBot(t *testing.T) {
	setWindow(t, 300*time.Millisecond)
	dir := t.TempDir()
	k, h := newHookServer(t)
	lift := time.Now().Add(1500 * time.Millisecond)
	k.on = func(Notification) {
		if time.Now().After(lift) { // the bot goes on with every run at once
			recs, _ := Unacked(dir, "qa-lead", time.Now())
			for _, r := range recs {
				Ack(dir, r.ID, "qa-lead", time.Now())
			}
		}
	}
	// A digest that lists every run, as the waiting line does.
	all := func(bot string, now time.Time) (Notification, []string) {
		recs, _ := Unacked(dir, bot, now)
		n := NoticeFor(bot, recs)
		ids := []string{}
		for _, r := range recs {
			ids = append(ids, r.ID)
		}
		return n, ids
	}
	var wg sync.WaitGroup
	for range 50 {
		r, err := Create(dir, Record{Launcher: "qa-lead", Limit: "10s", Command: []string{"true"}, Dir: dir}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := Supervise(context.Background(), dir, r.ID, Options{
				Rings:     []time.Duration{0, time.Minute},
				Hook:      func(string) (Hook, error) { return h, nil },
				Digest:    all,
				RingAgain: func(Record) time.Time { return lift },
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if recs, _ := Unacked(dir, "qa-lead", time.Now()); len(recs) != 0 {
		t.Fatalf("%d runs left unacked", len(recs))
	}
	if n := k.since(lift); n != 1 {
		t.Fatalf("%d rings after the lift, of %d; want 1", n, k.count())
	}
}

// A supervisor's end is settling while OnEnd runs: no reader lists it,
// and it settles before the first ring.
func TestSuperviseSettles(t *testing.T) {
	dir := t.TempDir()
	r, err := Create(dir, Record{Launcher: "planner", Limit: "10s", Command: []string{"true"}, Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	k, h := newHookServer(t)
	var during Record
	var listed []Record
	err = Supervise(context.Background(), dir, r.ID, Options{
		Rings: []time.Duration{0},
		Hook:  func(string) (Hook, error) { return h, nil },
		OnEnd: func(Record) {
			during, _ = Load(dir, r.ID)
			listed, _ = Unacked(dir, "planner", time.Now())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := Load(dir, r.ID)
	if !during.Settling || len(listed) != 0 || got.Settling || k.count() != 1 || !slices.Contains(k.last().Runs, r.ID) {
		t.Fatalf("during OnEnd settling %v, listed %d; after settling %v, %d rings", during.Settling, len(listed), got.Settling, k.count())
	}
}

// A supervisor stopped, as at a shutdown, leaves its end settling and
// rings no one: the first reader after routes and chains it, so nothing
// it would start dies with the machine.
func TestSuperviseStoppedStaysSettling(t *testing.T) {
	dir := t.TempDir()
	r, err := Create(dir, Record{Launcher: "planner", Limit: "10s", Command: []string{"sh", "-c", "exec tail -f /dev/null"}, Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	k, h := newHookServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := false
	err = Supervise(ctx, dir, r.ID, Options{
		Rings:   []time.Duration{0},
		Hook:    func(string) (Hook, error) { return h, nil },
		OnStart: func(Record) int64 { cancel(); return 0 },
		OnEnd:   func(Record) { ended = true },
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := Load(dir, r.ID)
	if !ended || !got.LauncherStopped() || !got.Settling || k.count() != 0 {
		t.Fatalf("OnEnd %v, stopped %v, settling %v, %d rings", ended, got.LauncherStopped(), got.Settling, k.count())
	}
}
