package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// On SQLite, stop --all sets and lifts this machine's STOP file, and
// never removes one made by hand.
func TestStopAllOnSQLite(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "a")
	stop := filepath.Join(h.dir, "STOP")
	var out haltOut
	h.ok(&out, "stop", "--all")
	if b, err := os.ReadFile(stop); !out.Halted || out.Shared || !out.Changed || out.Stop != stop || err != nil || string(b) != haltBody {
		t.Fatalf("stop --all: %+v %q %v", out, b, err)
	}
	if code := h.fails(4, "claim", "--next", "--holder", "jse/s1"); code != "stopped" {
		t.Fatalf("claim under the halt: %s", code)
	}
	h.ok(&out, "stop", "--all", "--off")
	if _, err := os.Stat(stop); out.Halted || !out.Changed || out.Kept || !os.IsNotExist(err) {
		t.Fatalf("stop --all --off: %+v %v", out, err)
	}
	// A STOP made by hand: stop --all leaves it as it is, and the lift
	// keeps it.
	os.WriteFile(stop, []byte("by hand\n"), 0o644)
	h.ok(&out, "stop", "--all")
	if out.Changed {
		t.Fatalf("stop --all over a hand-made STOP: %+v", out)
	}
	_, text, _ := h.run("", "stop", "--all", "--off")
	if b, _ := os.ReadFile(stop); string(b) != "by hand\n" || !strings.Contains(text, "stays") {
		t.Fatalf("the lift touched a hand-made STOP: %q %s", b, text)
	}
	for _, args := range [][]string{{"stop", "--off"}, {"stop", "--all", "R-aaaaaaaa"}, {"stop", "--off", "R-aaaaaaaa"}} {
		if code := h.fails(2, args...); code != "usage" {
			t.Fatalf("%v: %s", args, code)
		}
	}
}

// Another machine's .active mark holds though no record of its run is
// here, until the run is overdue; this machine's own mark says whose it
// is.
func TestActiveMarkFromAnotherMachine(t *testing.T) {
	dir := t.TempDir()
	rdir := filepath.Join(dir, "runs")
	co := coord{s: fileCoord{dir: dir, handouts: filepath.Join(dir, "handouts"), runs: rdir}, host: "aaaa"}
	name := activeName("T-1", "build")
	b, _ := json.Marshal(activeMark{Run: "R-bbbbbbbb", Host: "bbbb", By: "senior-engineer", Limit: "1h"})
	if err := co.s.write(runMarks, name, string(b)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := co.takeActive(rdir, "T-1", "build", "junior-engineer", time.Hour); !errors.Is(err, ticket.ErrClaimed) || !strings.Contains(err.Error(), "another machine") {
		t.Fatalf("another machine's live run: %v", err)
	}
	var going alreadyGoing
	if _, _, err := co.takeActive(rdir, "T-1", "build", "senior-engineer", time.Hour); !errors.As(err, &going) || going.run != "R-bbbbbbbb" {
		t.Fatalf("the same bot's run elsewhere: %v", err)
	}
	// Past its limit, the most slot wait, the kill grace and an hour.
	old := time.Now().Add(-11 * time.Hour)
	os.Chtimes(filepath.Join(rdir, name), old, old)
	commit, _, err := co.takeActive(rdir, "T-1", "build", "junior-engineer", 45*time.Minute)
	if err != nil {
		t.Fatalf("an overdue run elsewhere still holds: %v", err)
	}
	commit("R-cccccccc")
	m, _ := co.s.read(runMarks, name)
	a, ok := readActive(m.body)
	if !ok || a.Run != "R-cccccccc" || a.Host != "aaaa" || a.By != "junior-engineer" || a.Limit != "45m" || a.Started.IsZero() {
		t.Fatalf("mark: %q", m.body)
	}
	co.dropActive(runs.Record{ID: "R-cccccccc", Ticket: "T-1", Job: "build"})
	if _, err := co.s.read(runMarks, name); !os.IsNotExist(err) {
		t.Fatalf("the run's end left its mark: %v", err)
	}
}

// alerts and log-run judge only this machine's launches; another
// machine's go on until overdue.
func TestLaunchOverLeavesAnotherMachinesRuns(t *testing.T) {
	now := time.Now()
	l := ticket.Launch{Host: "bbbb", PID: 999999, Stamp: "another-boot|x|1", StartedAt: now.Add(-2 * time.Hour), Limit: "1h"}
	if launchOver(l, "aaaa", now) {
		t.Fatal("a run elsewhere, two hours in, is over")
	}
	if !launchOver(l, "bbbb", now) {
		t.Fatal("its own machine can't judge it")
	}
	l.StartedAt = now.Add(-11 * time.Hour)
	if !launchOver(l, "aaaa", now) {
		t.Fatal("an overdue run elsewhere still goes")
	}
	// No limit: the most any job's may be.
	l.Limit = ""
	if launchOver(l, "aaaa", now) {
		t.Fatal("a run elsewhere with no limit, 11 hours in, is over")
	}
	l.StartedAt = now.Add(-40 * time.Hour)
	if !launchOver(l, "aaaa", now) {
		t.Fatal("a run elsewhere 40 hours in still goes")
	}
	// No host: this machine's, as on SQLite.
	l.Host, l.StartedAt = "", now
	if !launchOver(l, "", now) || !launchOver(l, "aaaa", now) {
		t.Fatal("a launch with no host isn't judged here")
	}
}

// sharedBoard makes a MongoDB database for t and returns a URI naming
// it, or skips with no test replica set.
func sharedBoard(t *testing.T) string {
	t.Helper()
	uri := os.Getenv("MAESTRO_TICKET_TEST_MONGO")
	if uri == "" {
		t.Skip("set MAESTRO_TICKET_TEST_MONGO to a replica set URI to run the two-machine tests")
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	rand.Read(b)
	db := "p3coord_cli_" + hex.EncodeToString(b)
	u.Path = "/" + db
	t.Cleanup(func() {
		c, err := mongo.Connect(options.Client().ApplyURI(uri))
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Disconnect(context.Background())
		c.Database(db).Drop(context.Background())
	})
	return u.String()
}

// onBoard is a harness with a local folder of its own, on the board at
// uri: one machine.
func onBoard(t *testing.T, uri, actor string) *harness {
	h := newHarness(t)
	h.env["MAESTRO_TICKET_ACTOR"] = actor
	writeURI(t, h.dir, uri)
	return h
}

func lookAt(t *testing.T, h *harness) lookOut {
	t.Helper()
	var out lookOut
	h.ok(&out, "look", "--limit", "10")
	return out
}

// On a shared store, of takers that all find a mark stale one moves it,
// and only the copy it read; a mark's time is its age by the server, on
// this machine's clock; a run's .resumed mark stays in files here.
func TestSharedCoordMarks(t *testing.T) {
	h := onBoard(t, sharedBoard(t), "planner")
	c := &call{env: Env{Getenv: func(k string) string { return h.env[k] }, Dir: h.dir}}
	defer c.closeBoard()
	co, err := c.coord()
	if err != nil {
		t.Fatal(err)
	}
	if err := co.s.create(handoutMarks, "T-1.qa-lead", "a\n"); err != nil {
		t.Fatal(err)
	}
	seen, err := co.s.read(handoutMarks, "T-1.qa-lead")
	if err != nil || seen.body != "a\n" || time.Since(seen.at) > time.Minute || time.Since(seen.at) < 0 {
		t.Fatalf("read: %+v %v", seen, err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); co.moveStale(handoutMarks, "T-1.qa-lead", seen) }()
	}
	wg.Wait()
	if _, err := co.s.read(handoutMarks, "T-1.qa-lead"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the stale mark stayed: %v", err)
	}
	co.s.write(handoutMarks, "T-1.qa-lead", "b\n")
	co.moveStale(handoutMarks, "T-1.qa-lead", seen)
	if m, err := co.s.read(handoutMarks, "T-1.qa-lead"); err != nil || m.body != "b\n" {
		t.Fatalf("a newer mark was moved: %+v %v", m, err)
	}
	if ms, err := co.s.list(handoutMarks); err != nil || len(ms) != 1 || ms[0].name != "T-1.qa-lead" {
		t.Fatalf("list: %+v %v", ms, err)
	}
	commit, _, err := co.takeMark("R-aaaaaaaa.resumed", func(id string) string { return id }, func(string, mark) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	commit("R-bbbbbbbb")
	if b, err := os.ReadFile(filepath.Join(h.dir, "runs", "R-aaaaaaaa.resumed")); err != nil || string(b) != "R-bbbbbbbb" {
		t.Fatalf("resumed mark: %q %v", b, err)
	}
}

// Two machines on one board: looks hand each job out once, a pause or a
// halt set on one holds on the other, and neither ends the other's runs.
func TestTwoMachinesShareCoordination(t *testing.T) {
	uri := sharedBoard(t)
	a, b := onBoard(t, uri, "planner"), onBoard(t, uri, "planner")
	for range 4 {
		a.ok(nil, "create", "--title", "x")
	}
	ha, err := HostID(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if hb, _ := HostID(b.dir); hb == ha {
		t.Fatal("the machines share a host id")
	}

	// Looks at once, from both: one handout per ticket.
	var mu sync.Mutex
	sent := map[string]int{}
	var wg sync.WaitGroup
	for _, h := range []*harness{a, b, a, b, a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, out, errs := h.run("", "look", "--limit", "10", "--json")
			var o lookOut
			if code != 0 || json.Unmarshal([]byte(out), &o) != nil {
				t.Errorf("look: exit %d %s %s", code, out, errs)
				return
			}
			mu.Lock()
			for _, s := range o.Send {
				sent[s.Bot+": "+s.Line]++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(sent) != 4 {
		t.Fatalf("sends: %v; want a build for each of 4 tickets", sent)
	}
	for line, n := range sent {
		if n != 1 || !strings.Contains(line, "build T-") {
			t.Fatalf("sends: %v; want each once", sent)
		}
	}
	if _, err := os.Stat(filepath.Join(a.dir, "handouts")); !os.IsNotExist(err) {
		t.Fatalf("handouts went to files: %v", err)
	}

	// A pause set on A holds on B.
	ca := &call{env: Env{Getenv: func(k string) string { return a.env[k] }, Dir: a.dir}}
	coA, err := ca.coord()
	if err != nil || coA.host != ha {
		t.Fatalf("coord: %+v %v", coA, err)
	}
	if !coA.setPause(pause{Until: time.Now().Add(time.Hour), Why: "usage limit"}, time.Now()) {
		t.Fatal("no pause set")
	}
	if o := lookAt(t, b); !strings.Contains(o.Paused, "usage limit") {
		t.Fatalf("B's look: paused %q", o.Paused)
	}
	if _, err := os.Stat(filepath.Join(a.dir, pauseFlag)); !os.IsNotExist(err) {
		t.Fatalf("the pause went to a file: %v", err)
	}
	coA.s.clearFlag(pauseFlag)
	ca.closeBoard()

	// A's run on T-5, from before a restart by its stamp: B can't see its
	// process, so B's alerts leave it and its claim alone, and B's look
	// counts it; A's own alerts end it and free the claim.
	a.ok(nil, "create", "--title", "a's build")
	a.ok(nil, "create", "--title", "a's other build")
	a.ok(nil, "claim", "T-5", "--holder", "senior-engineer/a")
	s, err := mongostore.Open(context.Background(), uri, mongostore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	parts := strings.SplitN(procStamp(os.Getpid())+"||", "|", 3)
	before := "another-boot|" + parts[1] + "|" + parts[2]
	for _, id := range []ticket.ID{5, 6} {
		if _, err := s.StartLaunch(context.Background(), ticket.StartLaunchRequest{Actor: "senior-engineer", Ticket: id, PID: os.Getpid(),
			Stamp: before, Host: ha, Job: "build", Limit: "1h"}); err != nil {
			t.Fatal(err)
		}
	}
	var r alertsJSON
	if b.ok(&r, "alerts"); len(r.Died) != 0 || r.Restarted {
		t.Fatalf("B's alerts ended A's runs: %+v", r)
	}
	var got tk
	if b.ok(&got, "get", "T-5"); got.Claim == nil {
		t.Fatal("B freed A's claim")
	}
	for _, s := range lookAt(t, b).Send {
		if strings.Contains(s.Line, "T-6") {
			t.Fatalf("B handed out T-6, which A's run builds: %+v", s)
		}
	}
	if procStamp(os.Getpid()) != "" {
		if a.ok(&r, "alerts"); len(r.Died) != 2 || !r.Restarted {
			t.Fatalf("A's alerts: %+v", r)
		}
		if a.ok(&got, "get", "T-5"); got.Claim != nil {
			t.Fatal("A didn't free its dead run's claim")
		}
	}

	// stop --all on B halts claims on A; A's look copies the halt to its
	// STOP file, and the lift removes only that copy.
	var h haltOut
	if b.ok(&h, "stop", "--all"); !h.Shared || !h.Changed {
		t.Fatalf("stop --all: %+v", h)
	}
	if code := a.fails(4, "claim", "--next", "--holder", "jse/s1"); code != "stopped" {
		t.Fatalf("A's claim under B's halt: %s", code)
	}
	if o := lookAt(t, a); !o.Stopped || len(o.Send) != 0 {
		t.Fatalf("A's look under the halt: %+v", o)
	}
	if bs, err := os.ReadFile(filepath.Join(a.dir, "STOP")); err != nil || string(bs) != haltBody {
		t.Fatalf("A's STOP: %q %v", bs, err)
	}
	b.ok(&h, "stop", "--all", "--off")
	if _, err := os.Stat(filepath.Join(b.dir, "STOP")); !os.IsNotExist(err) {
		t.Fatalf("B's STOP stayed: %v", err)
	}
	if o := lookAt(t, a); o.Stopped {
		t.Fatal("A still stopped after the lift")
	}
	if _, err := os.Stat(filepath.Join(a.dir, "STOP")); !os.IsNotExist(err) {
		t.Fatalf("A's copy stayed: %v", err)
	}
	a.ok(nil, "claim", "--next", "--holder", "jse/s1")
	// A STOP made by hand on A outlives a halt and its lift.
	os.WriteFile(filepath.Join(a.dir, "STOP"), nil, 0o644)
	b.ok(nil, "stop", "--all")
	b.ok(nil, "stop", "--all", "--off")
	if o := lookAt(t, a); !o.Stopped {
		t.Fatal("A's own STOP was lifted from B")
	}
}
