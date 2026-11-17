//go:build unix

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// Two machines start one ticket's build at once: one run starts, on one
// of them, and its launch says whose it is.
func TestTwoMachinesStartOneRun(t *testing.T) {
	uri := sharedBoard(t)
	var ms [2]*jobHarness
	for i := range ms {
		ms[i] = newJobHarness(t, "senior-engineer")
		writeURI(t, ms[i].h.dir, uri)
		slow := strings.Replace(fakeClaude, "printf '{", "sleep 3; printf '{", 1)
		if err := os.WriteFile(filepath.Join(ms[i].bin, "claude"), []byte(slow), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ms[0].h.ok(nil, "create", "--title", "x")
	outs := make([]string, 4)
	var wg sync.WaitGroup
	for i := range outs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, _ := ms[i%2].cmd("", "job", "build", "T-1").CombinedOutput()
			outs[i] = string(b)
		}()
	}
	wg.Wait()
	var started []runs.Record
	var on *jobHarness
	for _, m := range ms {
		list, err := runs.List(filepath.Join(m.h.dir, "runs"), time.Now(), true)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) > 0 {
			on = m
		}
		started = append(started, list...)
	}
	if len(started) != 1 {
		t.Fatalf("%d runs started; want 1\n%s", len(started), strings.Join(outs, "\n"))
	}
	for _, o := range outs {
		if !strings.Contains(o, started[0].ID) && !strings.Contains(o, "claimed") && !strings.Contains(o, "under way") {
			t.Fatalf("a start neither ran, went on, nor was refused: %s", o)
		}
	}
	host, _ := HostID(on.h.dir)
	s, err := mongostore.Open(context.Background(), uri, mongostore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		open, err := s.OpenLaunches(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(open) == 1 {
			if l := open[0]; l.Host != host || l.Job != "build" || l.Limit == "" || l.Ticket != 1 {
				t.Fatalf("launch: %+v; want host %s", l, host)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("open launches: %+v", open)
		}
		time.Sleep(100 * time.Millisecond)
	}
	on.waitEnded(started[0].ID, 20*time.Second)
}

// A start whose .active mark the board won't give its run's id fails, and
// no run goes: an empty mark lets any machine take the job over in 30s.
func TestSharedActiveMarkWriteFails(t *testing.T) {
	uri := sharedBoard(t)
	m := newJobHarness(t, "senior-engineer")
	writeURI(t, m.h.dir, uri)
	m.h.ok(nil, "create", "--title", "x")
	refuseBodies(t, uri, runMarks)
	if out, err := m.cmd("", "job", "build", "T-1").CombinedOutput(); err == nil || !strings.Contains(string(out), "mark the run") {
		t.Fatalf("the start went on with an empty mark: %v %s", err, out)
	}
	list, err := runs.List(filepath.Join(m.h.dir, "runs"), time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list {
		if !r.Ended() || r.AckedAt == nil {
			t.Fatalf("run %s is %s, acked %v: a failed start leaves nothing going or to pick up", r.ID, r.State, r.AckedAt)
		}
	}
}
