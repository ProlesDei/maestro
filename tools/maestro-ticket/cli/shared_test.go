package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
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

// sharedBoard makes a MongoDB board for t and returns a URI naming its
// database, or skips with no test replica set.
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
	makeBoard(t, u.String())
	return u.String()
}

// makeBoard starts a board in the database uri names, as a move in does:
// no other command makes one.
func makeBoard(t *testing.T, uri string) {
	t.Helper()
	s, err := mongostore.Open(context.Background(), uri, mongostore.Options{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
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

// refuseBodies makes the board at uri turn down any mark in space that
// holds a body: a create, which holds none, lands; a write fails, as one
// does when the store goes away mid-call.
func refuseBodies(t *testing.T, uri, space string) {
	t.Helper()
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	c, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect(context.Background())
	v := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "space", Value: bson.D{{Key: "$ne", Value: space}}}},
		bson.D{{Key: "body", Value: ""}},
	}}}
	cmd := bson.D{{Key: "collMod", Value: "marks"}, {Key: "validator", Value: v}}
	if err := c.Database(strings.TrimPrefix(u.Path, "/")).RunCommand(context.Background(), cmd).Err(); err != nil {
		t.Fatal(err)
	}
}

// A pickup whose handout mark the board turns down keeps the send and
// says why: one dropped there is a handoff lost, and chain would ack its
// run unseen.
func TestSharedPickupKeepsSendOnMarkError(t *testing.T) {
	uri := sharedBoard(t)
	h := onBoard(t, uri, "planner")
	h.ok(nil, "create", "--title", "x")
	refuseBodies(t, uri, handoutMarks)
	c := &call{env: Env{Getenv: func(k string) string { return h.env[k] }, Dir: h.dir}}
	defer c.closeBoard()
	co, err := c.coord()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	got, err := co.pickupSends("R-aaaaaaaa", now.Add(-time.Hour), []send{{"reviewer", "review T-1"}}, now)
	if len(got) != 1 || err == nil {
		t.Fatalf("sends %v, error %v; want the review kept and the error", got, err)
	}
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

// A board this machine can't read is no halt: a start fails with the
// board's error, not stopped. A STOP file here still stops it.
func TestBoardErrorIsNoHalt(t *testing.T) {
	h := onBoard(t, "mongodb://127.0.0.1:1/x?serverSelectionTimeoutMS=200&connectTimeoutMS=200", "planner")
	c := &call{env: Env{Getenv: func(k string) string { return h.env[k] }, Dir: h.dir}}
	defer c.closeBoard()
	if err := c.mayStart(); err == nil || errors.Is(err, ticket.ErrStopped) {
		t.Fatalf("a start on a board it can't reach: %v", err)
	}
	os.WriteFile(filepath.Join(h.dir, "STOP"), nil, 0o644)
	if err := c.mayStart(); !errors.Is(err, ticket.ErrStopped) {
		t.Fatalf("a start with STOP here: %v", err)
	}
}

// A heartbeat on a shared store copies the halt to this machine's STOP
// file, so a session already going sees stop --all; the lift removes it.
func TestHeartbeatSeesTheHalt(t *testing.T) {
	uri := sharedBoard(t)
	a, b := onBoard(t, uri, "planner"), onBoard(t, uri, "planner")
	a.ok(nil, "create", "--title", "x")
	var got tk
	a.ok(&got, "claim", "T-1", "--holder", "jse/s1")
	beat := func() { a.ok(nil, "heartbeat", "T-1", "--holder", "jse/s1", "--token", fmt.Sprint(got.Claim.Token)) }
	stop := filepath.Join(a.dir, "STOP")
	b.ok(nil, "stop", "--all")
	beat()
	if bs, err := os.ReadFile(stop); err != nil || string(bs) != haltBody {
		t.Fatalf("A's STOP after a heartbeat under the halt: %q %v", bs, err)
	}
	b.ok(nil, "stop", "--all", "--off")
	beat()
	if _, err := os.Stat(stop); !os.IsNotExist(err) {
		t.Fatalf("A's copy stayed after the lift: %v", err)
	}
}

// A machine that copied the halt and then ran nothing that reads it
// still claims once the halt lifts: the claim checks the board before it
// obeys its copy. A STOP made by hand still refuses.
func TestClaimClearsALiftedHaltCopy(t *testing.T) {
	uri := sharedBoard(t)
	a, b := onBoard(t, uri, "planner"), onBoard(t, uri, "planner")
	a.ok(nil, "create", "--title", "x")
	a.ok(nil, "create", "--title", "y")
	var got tk
	a.ok(&got, "claim", "T-1", "--holder", "jse/s1")
	tok := fmt.Sprint(got.Claim.Token)
	stop := filepath.Join(a.dir, "STOP")
	b.ok(nil, "stop", "--all")
	a.ok(nil, "heartbeat", "T-1", "--holder", "jse/s1", "--token", tok)
	a.ok(nil, "release", "T-1", "--holder", "jse/s1", "--token", tok)
	if bs, err := os.ReadFile(stop); err != nil || string(bs) != haltBody {
		t.Fatalf("A's STOP under the halt: %q %v", bs, err)
	}
	b.ok(nil, "stop", "--all", "--off")
	a.ok(nil, "claim", "T-2", "--holder", "jse/s2")
	if _, err := os.Stat(stop); !os.IsNotExist(err) {
		t.Fatalf("A's copy stayed after its claim: %v", err)
	}
	os.WriteFile(stop, []byte("by hand\n"), 0o644)
	if code := a.fails(4, "claim", "T-1", "--holder", "jse/s3"); code != "stopped" {
		t.Fatalf("a claim under a STOP made by hand: %s", code)
	}
}

// Another machine's run that a usage limit stopped holds its ticket
// until the limit is a stall past lifting, as this machine's own do.
func TestLookHoldsALimitedRunElsewhere(t *testing.T) {
	uri := sharedBoard(t)
	a, b := onBoard(t, uri, "planner"), onBoard(t, uri, "planner")
	a.ok(nil, "create", "--title", "x")
	ha, err := HostID(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := mongostore.Open(ctx, uri, mongostore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l, err := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "senior-engineer", Ticket: 1, PID: os.Getpid(), Host: ha, Job: "build", Limit: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: l.ID, Outcome: ticket.LaunchResult, Lifts: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// It ended long ago, so only the limit holds it.
	c, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect(ctx)
	u, _ := url.Parse(uri)
	if _, err := c.Database(strings.TrimPrefix(u.Path, "/")).Collection("launches").UpdateByID(ctx, l.ID,
		bson.D{{Key: "$set", Value: bson.D{{Key: "ended_at", Value: bson.NewDateTimeFromTime(time.Now().Add(-5 * time.Hour))}}}}); err != nil {
		t.Fatal(err)
	}
	for _, s := range lookAt(t, b).Send {
		if strings.Contains(s.Line, "T-1") {
			t.Fatalf("B handed out T-1, which A's limited run holds: %+v", s)
		}
	}
}

// blockedReview makes T-1 on h's board blocked on a review's Escalation:,
// which a look hands the judge once no run holds it.
func blockedReview(t *testing.T, h *harness) {
	t.Helper()
	h.ok(nil, "create", "--title", "a")
	h.ok(nil, "transition", "T-1", "--to", "in_progress")
	h.ok(nil, "transition", "T-1", "--to", "in_review")
	if code, out, errs := h.run("Escalation: review at abc1234: which schema?\nReturn-role: engineer", "comment", "T-1", "--body-file", "-"); code != 0 {
		t.Fatalf("comment: %d %s %s", code, out, errs)
	}
	h.ok(nil, "transition", "T-1", "--to", "blocked")
}

// A run that ended on A and was left to the planner holds no ticket from
// B's look: the launch says whose its end is.
func TestLookTakesARunLeftToThePlannerElsewhere(t *testing.T) {
	uri := sharedBoard(t)
	a, b := onBoard(t, uri, "reviewer"), onBoard(t, uri, "planner")
	blockedReview(t, a)
	ha, err := HostID(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := mongostore.Open(ctx, uri, mongostore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l, err := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "reviewer", Ticket: 1, PID: os.Getpid(), Host: ha, Job: "review", Limit: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: l.ID, Outcome: ticket.LaunchResult}); err != nil {
		t.Fatal(err)
	}
	if out := lookAt(t, b); len(out.Judge) != 0 {
		t.Fatalf("B judged a ticket A's run still holds: %+v", out.Judge)
	}
	if err := s.NotifyLaunch(ctx, l.ID, "planner"); err != nil {
		t.Fatal(err)
	}
	if out := lookAt(t, b); len(out.Judge) != 1 || out.Judge[0].ID != "T-1" {
		t.Fatalf("a run left to the planner held T-1 from B: %+v", out)
	}
}

// A chain that leaves a run's end to the planner says so on its launch.
func TestChainNotifiesTheLaunch(t *testing.T) {
	uri := sharedBoard(t)
	a := onBoard(t, uri, "reviewer")
	blockedReview(t, a)
	for _, bot := range []string{"reviewer", "planner"} {
		if code, _, errs := a.run("http://127.0.0.1:1/x\n", "notify", bot, "--url-file", "-"); code != 0 {
			t.Fatal(errs)
		}
	}
	c := &call{actor: "reviewer", env: Env{Getenv: func(k string) string { return a.env[k] }, Dir: a.dir, Stderr: os.Stderr}}
	defer c.closeBoard()
	dir, err := c.besideStore("runs")
	if err != nil {
		t.Fatal(err)
	}
	ha, err := HostID(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := mongostore.Open(ctx, uri, mongostore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l, err := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "reviewer", Ticket: 1, PID: os.Getpid(), Host: ha, Job: "review", Limit: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dir, 0o755)
	r, err := runs.Create(dir, runs.Record{Launcher: "reviewer", Limit: "1h", Command: []string{"true"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r.State, r.Ticket, r.Job, r.StartedAt, r.EndedAt, r.LaunchID = runs.Done, "T-1", "review", now.Add(-time.Minute), &now, l.ID
	runs.Save(dir, r)
	c.chain(ctx, dir, r)
	if got, _ := runs.Load(dir, r.ID); got.Notify != "planner" {
		t.Fatalf("the run wasn't left to the planner: %+v", got)
	}
	ls, err := s.LaunchesSince(ctx, now.Add(-time.Hour))
	if err != nil || len(ls) != 1 || ls[0].Notify != "planner" {
		t.Fatalf("launches: %+v, %v", ls, err)
	}
}
