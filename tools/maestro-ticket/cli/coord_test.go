package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The folder forms the older tests call, each on the files under dir.
func handoutsAt(dir string) coord { return coord{s: fileCoord{handouts: dir}} }

func handoutPath(dir, id, bot string) string { return filepath.Join(dir, handoutName(id, bot)) }

func claimHandout(dir, id, bot, body string, d time.Duration, now time.Time) bool {
	return handoutsAt(dir).claimHandout(id, bot, body, d, now)
}

func claimJudge(dir, id, fp string, now time.Time) bool {
	return handoutsAt(dir).claimJudge(id, fp, now)
}

func pruneHandouts(dir string, now time.Time) { handoutsAt(dir).pruneHandouts(now) }

func pickupSends(dir, runID string, since time.Time, sends []send, now time.Time) []send {
	out, _ := handoutsAt(dir).pickupSends(runID, since, sends, now)
	return out
}

func pausePath(dir string) string { return filepath.Join(dir, pauseFlag) }

func readPause(dir string, now time.Time) (pause, bool) {
	return coord{s: fileCoord{dir: dir}}.readPause(now)
}

func setPause(dir string, p pause, now time.Time) bool {
	return coord{s: fileCoord{dir: dir}}.setPause(p, now)
}

// The file coord keeps today's paths: handouts/, runs/ and the store's
// folder.
func TestCoordFilePaths(t *testing.T) {
	dir := t.TempDir()
	co := coord{s: fileCoord{dir: dir, handouts: filepath.Join(dir, "handouts"), runs: filepath.Join(dir, "runs")}}
	if !co.claimHandout("T-1", "reviewer", "review T-1", time.Minute, time.Now()) {
		t.Fatal("no handout")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "handouts", "T-1.reviewer")); err != nil || string(b) != "review T-1\n" {
		t.Fatalf("handout file: %q %v", b, err)
	}
	commit, _, err := co.takeActive(filepath.Join(dir, "runs"), "T-1", "build", "x", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	commit("R-aaaaaaaa")
	if b, err := os.ReadFile(filepath.Join(dir, "runs", "T-1.build.active")); err != nil || string(b) != "R-aaaaaaaa" {
		t.Fatalf("active file: %q %v", b, err)
	}
	co.setPause(pause{Until: time.Now().Add(time.Hour), Why: "usage limit"}, time.Now())
	if _, err := os.Stat(filepath.Join(dir, "paused.json")); err != nil {
		t.Fatal(err)
	}
	if err := co.s.clearFlag(pauseFlag); err != nil {
		t.Fatal(err)
	}
	if _, err := co.s.flag(pauseFlag); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a cleared flag reads %v", err)
	}
}

// Of takers that all find a mark stale, one moves it aside, and only the
// mark it read: one written over it since stays.
func TestCoordTakeOver(t *testing.T) {
	dir := t.TempDir()
	co := handoutsAt(dir)
	co.s.create(handoutMarks, "T-1.qa-lead", "a\n")
	seen, err := co.s.read(handoutMarks, "T-1.qa-lead")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	took := make(chan bool, 20)
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); took <- co.moveStale(handoutMarks, "T-1.qa-lead", seen) }()
	}
	wg.Wait()
	close(took)
	if _, err := co.s.read(handoutMarks, "T-1.qa-lead"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the stale mark stayed: %v", err)
	}
	co.s.write(handoutMarks, "T-1.qa-lead", "b\n")
	if !co.moveStale(handoutMarks, "T-1.qa-lead", seen) {
		t.Fatal("no lock taken")
	}
	if m, err := co.s.read(handoutMarks, "T-1.qa-lead"); err != nil || m.body != "b\n" {
		t.Fatalf("a newer mark was moved: %+v %v", m, err)
	}
	// A dead taker's lock is cleared, and the next try takes it.
	lock := filepath.Join(dir, "T-1.qa-lead.takeover")
	os.WriteFile(lock, nil, 0o644)
	old := time.Now().Add(-time.Minute)
	os.Chtimes(lock, old, old)
	if took, cleared := co.s.takeOver(handoutMarks, "T-1.qa-lead", func(mark) bool { return false }); took || !cleared {
		t.Fatalf("dead lock: took %v cleared %v", took, cleared)
	}
	if took, _ := co.s.takeOver(handoutMarks, "T-1.qa-lead", func(mark) bool { return false }); !took {
		t.Fatal("the next try didn't take the lock")
	}
}
