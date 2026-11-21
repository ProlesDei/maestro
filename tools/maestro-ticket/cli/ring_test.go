//go:build unix

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/sqlitestore"
)

// storedRun writes an ended job run of bot whose end stored rt.
func storedRun(t *testing.T, dir, bot string, rt *routed) runs.Record {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	r, err := runs.Create(dir, runs.Record{Launcher: bot, Limit: "1h", Command: []string{"true"}, Job: "build", Ticket: "T-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r.State, r.StartedAt, r.EndedAt, r.Rang = runs.Done, now, &now, true // oldest first, as made
	if rt != nil {
		rt.Run = r.ID
		if rt.Send == nil {
			rt.Send = []send{}
		}
		r.Routed, _ = json.Marshal(rt)
	}
	if err := runs.Save(dir, r); err != nil {
		t.Fatal(err)
	}
	return r
}

// A run still settling shows in pickup as running, ending, with no route
// or ack, and in no ring.
func TestPickupShowsSettlingAsRunning(t *testing.T) {
	h := newRouteHarness(t)
	dir := filepath.Join(h.dir, "runs")
	r := storedRun(t, dir, "planner", &routed{Send: []send{{"planner", "merged T-1"}}})
	r.Settling = true
	runs.Save(dir, r)
	// Its supervisor holds the lock, as it does while it routes.
	lock, _ := os.OpenFile(filepath.Join(dir, r.ID+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	defer lock.Close()
	h.env["MAESTRO_TICKET_ACTOR"] = "planner"
	_, out, _ := h.run("", "pickup")
	if out != "no run of yours ended; running: "+r.ID+" (T-1, ending)\n" {
		t.Fatalf("pickup: %q", out)
	}
	if n, _ := ringDigest(filesCoord(dir), dir, "planner", time.Now()); len(n.Runs) != 0 || n.More != 0 {
		t.Fatalf("ring of a settling run: %+v", n)
	}
	if _, out, _ := h.run("", "ack", storedRun(t, dir, "planner", nil).ID); strings.Contains(out, "more") {
		t.Fatalf("ack counted the settling run: %s", out)
	}
}

// ack counts what still waits for the bot, the ones it acked aside: a
// settling run and a run a pause holds don't count.
func TestAckPrintsMore(t *testing.T) {
	h := newRouteHarness(t)
	dir := filepath.Join(h.dir, "runs")
	h.env["MAESTRO_TICKET_ACTOR"] = "planner"
	a := storedRun(t, dir, "planner", nil)
	b := storedRun(t, dir, "planner", nil)
	c := storedRun(t, dir, "planner", nil)
	s := storedRun(t, dir, "planner", nil)
	s.Settling = true
	runs.Save(dir, s)
	setPause(h.dir, pause{Until: time.Now().Add(time.Hour)}, time.Now())
	storedRun(t, dir, "planner", &routed{Outcome: outUsageLimit, Do: []string{"maestro-ticket resume R-abcdefgh --go-on"}})
	_, out, _ := h.run("", "ack", a.ID, b.ID)
	if !strings.HasSuffix(out, "\n1 more unacked for you: run pickup\n") {
		t.Fatalf("ack: %q", out)
	}
	var res struct{ More int }
	h.ok(&res, "ack", c.ID)
	if res.More != 0 {
		t.Fatalf("more after the last: %d", res.More)
	}
}

// While STOP or a pause holds, a run with a do: line waits: pickup shows
// one line for all, a ring leaves them out, ack counts none of them, and
// nothing says run pickup, so no bot reruns a refused command in a loop.
// Once it lifts, pickup shows them again.
func TestDoWaitsWhileHeld(t *testing.T) {
	h := newRouteHarness(t)
	dir := filepath.Join(h.dir, "runs")
	h.env["MAESTRO_TICKET_ACTOR"] = "senior-engineer"
	for range 25 {
		storedRun(t, dir, "senior-engineer", &routed{Outcome: outTimedOut, Do: []string{"maestro-ticket job again R-abcdefgh"}})
	}
	plain := storedRun(t, dir, "senior-engineer", &routed{Outcome: outDone, Send: []send{{"planner", "slot free T-1"}}})
	for _, hold := range []func() func(){
		func() func() {
			p := filepath.Join(h.dir, sqlitestore.StopFile)
			os.WriteFile(p, nil, 0o644)
			return func() { os.Remove(p) }
		},
		func() func() {
			setPause(h.dir, pause{Until: time.Now().Add(time.Hour), Why: "API outage"}, time.Now())
			return func() { os.Remove(pausePath(h.dir)) }
		},
	} {
		lift := hold()
		_, out, _ := h.run("", "pickup")
		if !strings.Contains(out, "25 runs wait to go on while ") || strings.Contains(out, "run pickup") || strings.Contains(out, "do: ") ||
			!strings.HasSuffix(out, "maestro-ticket ack "+plain.ID+"\n") {
			t.Fatalf("pickup while held: %s", out)
		}
		n, covered := ringDigest(filesCoord(dir), dir, "senior-engineer", time.Now())
		if n.More != 0 || !slices.Equal(n.Runs, []string{plain.ID}) || !slices.Equal(covered, []string{plain.ID}) {
			t.Fatalf("ring while held: %+v", n)
		}
		// As the bot's ack of the plain run counts what is left.
		cc := *h.c
		cc.actor, cc.args = "senior-engineer", []string{plain.ID}
		if m := cc.moreUnacked(dir, time.Now()); m != 0 {
			t.Fatalf("ack counts %d more while held", m)
		}
		lift()
	}
	// With only those left, a ring has nothing to say, so none wakes the bot.
	stop := filepath.Join(h.dir, sqlitestore.StopFile)
	os.WriteFile(stop, nil, 0o644)
	runs.Ack(dir, plain.ID, "senior-engineer", time.Now())
	if n, covered := ringDigest(filesCoord(dir), dir, "senior-engineer", time.Now()); len(n.Runs)+len(n.Lines)+n.More+len(covered) != 0 {
		t.Fatalf("ring of held runs only: %+v", n)
	}
	os.Remove(stop)
	_, out, _ := h.run("", "pickup")
	if strings.Contains(out, "wait to go on") || strings.Count(out, "do: maestro-ticket job again") != 20 || !strings.Contains(out, "and 5 more ended") {
		t.Fatalf("pickup once lifted: %s", out)
	}
}

// A run's end stores its route whatever its outcome, so its ring carries
// its lines; only a done end starts the next step.
func TestRoutedStoredForNonDone(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	dir := filepath.Join(h.dir, "runs")
	for _, st := range []string{runs.Failed, runs.TimedOut} {
		r := h.endedRun("review", "reviewer", "")
		r.State = st
		runs.Save(dir, r)
		h.c.chain(context.Background(), dir, r)
		got, _ := runs.Load(dir, r.ID)
		var rt routed
		if json.Unmarshal(got.Routed, &rt) != nil || rt.Outcome != st || len(got.Chained) != 0 || runs.Acked(dir, r.ID) {
			t.Fatalf("%s: %+v", st, got)
		}
	}
}
