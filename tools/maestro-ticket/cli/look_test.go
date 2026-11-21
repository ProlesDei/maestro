//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Of looks at once, one takes each handout; a stale one is taken again.
func TestClaimHandout(t *testing.T) {
	dir, now := t.TempDir(), time.Now()
	var wg sync.WaitGroup
	won := make(chan bool, 40)
	for range 40 {
		wg.Add(1)
		go func() { defer wg.Done(); won <- claimHandout(dir, "T-1", "reviewer", "review T-1", time.Minute, now) }()
	}
	wg.Wait()
	close(won)
	n := 0
	for w := range won {
		if w {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d of 40 claims won; want 1", n)
	}
	if claimHandout(dir, "T-1", "reviewer", "review T-1", time.Minute, now) {
		t.Fatal("a fresh handout was taken again")
	}
	if !claimHandout(dir, "T-1", "reviewer", "review T-1", time.Minute, now.Add(2*time.Minute)) {
		t.Fatal("a stale handout wasn't taken again")
	}
	if !claimJudge(dir, "T-2", "blocked v3 1", now) || claimJudge(dir, "T-2", "blocked v3 1", now) {
		t.Fatal("judge claim: want one take per state")
	}
	if !claimJudge(dir, "T-2", "blocked v4 2", now) {
		t.Fatal("a changed ticket is judged again")
	}
}

func TestResumeUsesThisBinary(t *testing.T) {
	exe, _ := os.Executable()
	cmd := []string{"env", "A=1", "/old/path/" + filepath.Base(exe), "slot", "--name", "qa", "--max", "6", "--", "claude", "-p", "--session-id", "S"}
	got := resumeCommand(cmd, "S", "")
	if got[2] != exe {
		t.Fatalf("slot binary: %q", got)
	}
}

func TestPruneKeepsEtaMarks(t *testing.T) {
	dir, now := t.TempDir(), time.Now()
	for _, n := range []string{"eta-demo-1.chief-of-staff", "T-1.reviewer"} {
		p := filepath.Join(dir, n)
		os.WriteFile(p, nil, 0o644)
		old := now.Add(-48 * time.Hour)
		os.Chtimes(p, old, old)
	}
	pruneHandouts(dir, now)
	if _, err := os.Stat(filepath.Join(dir, "eta-demo-1.chief-of-staff")); err != nil {
		t.Fatal("a missed date's mark went after a day")
	}
	if _, err := os.Stat(filepath.Join(dir, "T-1.reviewer")); err == nil {
		t.Fatal("a day-old handout stayed")
	}
}

// pickup drops a send a look made lately, and shows its own again.
func TestPickupSendsDedupe(t *testing.T) {
	dir, now := t.TempDir(), time.Now()
	claimHandout(dir, "T-1", "qa-lead", "qa T-1", time.Minute, now) // a look's
	got := pickupSends(dir, "R-aaaaaaaa", now.Add(-time.Hour), []send{{"qa-lead", "qa T-1"}, {"reviewer", "review T-2"}, {"planner", "slot free T-3"}}, now)
	if len(got) != 2 || got[0].Bot != "reviewer" || got[1].Bot != "planner" {
		t.Fatalf("first pickup: %+v", got)
	}
	again := pickupSends(dir, "R-aaaaaaaa", now.Add(-time.Hour), []send{{"reviewer", "review T-2"}}, now)
	if len(again) != 1 {
		t.Fatalf("a second pickup of the run drops its own send: %+v", again)
	}
	if other := pickupSends(dir, "R-bbbbbbbb", now.Add(-time.Hour), []send{{"reviewer", "review T-2"}}, now); len(other) != 0 {
		t.Fatalf("another run's pickup sends it again: %+v", other)
	}
}

// A run that began after a handout was made sends the next step: a second
// review after a quick kickback isn't held as the first one's.
func TestPickupSendsNextCycle(t *testing.T) {
	dir, now := t.TempDir(), time.Now()
	if s := pickupSends(dir, "R-build111", now.Add(-time.Hour), []send{{"reviewer", "review T-1"}}, now); len(s) != 1 {
		t.Fatalf("first build's pickup: %+v", s)
	}
	later := now.Add(15 * time.Minute)
	if s := pickupSends(dir, "R-build222", now.Add(5*time.Minute), []send{{"reviewer", "review T-1"}}, later); len(s) != 1 {
		t.Fatalf("second build's pickup dropped its review: %+v", s)
	}
	if s := pickupSends(dir, "R-build111", now.Add(-time.Hour), []send{{"reviewer", "review T-1"}}, later); len(s) != 0 {
		t.Fatalf("the first build's pickup sends its review again: %+v", s)
	}
}
