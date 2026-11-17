//go:build unix

package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// boardCall is a call on h's machine, as actor.
func boardCall(t *testing.T, h *harness, actor string) (*call, coord) {
	t.Helper()
	c := &call{env: Env{Getenv: func(k string) string { return h.env[k] }, Dir: h.dir}, actor: actor}
	t.Cleanup(c.closeBoard)
	co, err := c.coord()
	if err != nil {
		t.Fatal(err)
	}
	return c, co
}

// Two machines start one ticket's build at once: of all their takes of
// its .active mark, one wins, and its mark says whose run it is.
func TestTwoMachinesTakeOneActiveMark(t *testing.T) {
	uri := sharedBoard(t)
	var cos [2]coord
	var dirs [2]string
	for i := range cos {
		h := onBoard(t, uri, "senior-engineer")
		_, cos[i] = boardCall(t, h, "senior-engineer")
		dirs[i] = filepath.Join(h.dir, "runs")
	}
	if cos[0].host == "" || cos[0].host == cos[1].host {
		t.Fatalf("hosts %q and %q: want two machines", cos[0].host, cos[1].host)
	}
	var mu sync.Mutex
	var won []func(string) error
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			commit, _, err := cos[i%2].takeActive(dirs[i%2], "T-1", "build", "senior-engineer", time.Hour)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won = append(won, commit)
			case !errors.Is(err, ticket.ErrClaimed):
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(won) != 1 {
		t.Fatalf("%d takes won; want 1", len(won))
	}
	if err := won[0]("R-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	m, err := cos[0].s.read(runMarks, activeName("T-1", "build"))
	if a, ok := readActive(m.body); err != nil || !ok || a.Run != "R-aaaaaaaa" || a.Host == "" {
		t.Fatalf("mark %q: %v", m.body, err)
	}
}

// A start whose .active mark the board won't give its run's id fails, and
// no run goes: an empty mark lets any machine take the job over in 30s.
func TestSharedActiveMarkWriteFails(t *testing.T) {
	uri := sharedBoard(t)
	h := onBoard(t, uri, "senior-engineer")
	h.ok(nil, "create", "--title", "x")
	refuseBodies(t, uri, runMarks)
	c, co := boardCall(t, h, "senior-engineer")
	dir := filepath.Join(h.dir, "runs")
	commit, _, err := co.takeActive(dir, "T-1", "build", c.actor, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// startRun checks the mark before it starts the supervisor, so a
	// refused mark starts no process.
	_, err = c.startRun(runs.Record{Ticket: "T-1", Job: "build", Limit: "1h", Command: []string{"true"}}, nil, nil, nil, commit)
	if err == nil || !strings.Contains(err.Error(), "mark the run") {
		t.Fatalf("the start went on with an empty mark: %v", err)
	}
	list, err := runs.List(dir, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("%d runs; want the one failed start", len(list))
	}
	if r := list[0]; !r.Ended() || r.AckedAt == nil {
		t.Fatalf("run %s is %s, acked %v: a failed start leaves nothing going or to pick up", r.ID, r.State, r.AckedAt)
	}
}
