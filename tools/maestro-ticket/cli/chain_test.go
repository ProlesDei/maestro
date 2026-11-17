//go:build unix

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// endedRun saves a run of job on T-1 that ended done, as its supervisor
// would before it chains, with reply as its reply.
func (h *routeHarness) endedRun(job, launcher, reply string) runs.Record {
	h.t.Helper()
	dir := filepath.Join(h.dir, "runs")
	os.MkdirAll(dir, 0o755)
	r, err := runs.Create(dir, runs.Record{Launcher: launcher, Limit: "1h", Command: sonnetCmd()}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	now := time.Now()
	r.State, r.Ticket, r.Job, r.StartedAt, r.EndedAt, r.Reply = runs.Done, "T-1", job, now.Add(-time.Minute), &now, reply
	r.IsError = strings.Contains(reply, "limit")
	if err := runs.Save(dir, r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *routeHarness) hook(bot string) {
	h.t.Helper()
	if code, _, errs := h.run("http://127.0.0.1:1/x\n", "notify", bot, "--url-file", "-"); code != 0 {
		h.t.Fatal(errs)
	}
}

// Only a fixed job for a worker bot chains, and none past maxHops.
func TestChainNextStep(t *testing.T) {
	for _, c := range []struct {
		bot, line string
		hops      int
		want      string
	}{
		{"reviewer", "review T-1", 0, "review T-1"},
		{"qa-lead", "qa T-12", maxHops - 1, "qa T-12"},
		{"senior-engineer", "build T-3", 2, "build T-3"},
		{"reviewer", "review T-1", maxHops, ""},
		{"planner", "slot free T-1", 0, ""},
		{"chief-of-staff", "design T-1: card", 0, ""},
		{"reviewer", "review T-1 --approved abc1234", 0, ""},
		{"qa-lead", "live T-1", 0, ""},
		{"designer", "build T-1", 0, ""},
	} {
		got := ""
		if k, id, ok := nextStep(runs.Record{Hops: c.hops}, send{c.bot, c.line}); ok {
			got = k.name + " " + id
		}
		if got != c.want {
			t.Errorf("%s: %s at %d hops: %q; want %q", c.bot, c.line, c.hops, got, c.want)
		}
	}
}

// A review a look handed out lately is on its way: the end starts none,
// and with nothing left to do, acks the run.
func TestChainLeavesALooksHandout(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a", "--tier", "junior")
	h.move("T-1", "in_progress", "in_review")
	h.hook("reviewer")
	dir := filepath.Join(h.dir, "runs")
	h.c.actor = "junior-engineer"

	// A launcher with no webhook, such as a person, reads its own pickup:
	// the run stays for it, routed.
	if !claimHandout(filepath.Join(h.dir, "handouts"), "T-1", "reviewer", "review T-1", time.Hour, time.Now()) {
		t.Fatal("no handout")
	}
	r := h.endedRun("build", "junior-engineer", "build: done")
	h.c.chain(context.Background(), dir, r)
	if got, _ := runs.Load(dir, r.ID); len(got.Routed) == 0 || len(got.Chained) != 0 || runs.Acked(dir, r.ID) {
		t.Fatalf("a launcher with no webhook: %+v acked %v", got, runs.Acked(dir, r.ID))
	}
	h.hook("junior-engineer")

	r = h.endedRun("build", "junior-engineer", "build: done")
	h.c.chain(context.Background(), dir, r)
	got, _ := runs.Load(dir, r.ID)
	if len(got.Chained) != 0 || !runs.Acked(dir, r.ID) {
		t.Fatalf("a look's handout: %+v acked %v", got, runs.Acked(dir, r.ID))
	}
	if list, _ := runs.List(dir, time.Now(), true); len(list) != 2 {
		t.Fatalf("%d runs; want the two ended", len(list))
	}
}

// A run that hit the usage limit starts nothing, but its end routes it
// and keeps the route: the pause is set once, and the planner hears of it
// from the run's ring and pickup, which show that route.
func TestChainStoresALimitsRoute(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	h.hook("reviewer")
	h.hook("planner")
	dir := filepath.Join(h.dir, "runs")
	r := h.endedRun("review", "reviewer", "Claude AI usage limit reached|"+strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	h.c.chain(context.Background(), dir, r)
	got, _ := runs.Load(dir, r.ID)
	var rt routed
	if json.Unmarshal(got.Routed, &rt) != nil || got.Notify != "" || len(got.Chained) != 0 || runs.Acked(dir, r.ID) {
		t.Fatalf("chained a limit: %+v", got)
	}
	if !fileExists(pausePath(h.dir)) || len(sends(rt)) != 1 || !strings.HasPrefix(sends(rt)[0], "send planner: usage_limit T-1") {
		t.Fatalf("stored route: %+v", rt)
	}
}

// Only sends may go unseen: a command, a reply or a failed write stays
// for a bot.
func TestChainLeftForBot(t *testing.T) {
	for _, c := range []struct {
		rt   routed
		want bool
	}{
		{routed{Send: []send{{"planner", "merged T-1"}}}, false},
		{routed{Wrote: []string{"released T-1 for the planner"}}, false},
		{routed{Wrote: []string{"follow-ups not filed: T-9 is held"}}, true},
		{routed{Wrote: []string{"not parked for the answer: locked"}}, true},
		{routed{Do: []string{"maestro-ticket resume R-aaaaaaaa --opus"}}, true},
		{routed{Reply: "the answer"}, true},
	} {
		if got := leftForBot(c.rt); got != c.want {
			t.Errorf("%+v: %v; want %v", c.rt, got, c.want)
		}
	}
}

// A run whose route left a command for its bot is the bot's: the chain
// neither acks it nor leaves it to the planner.
func TestChainLeavesACommandToTheBot(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	h.hook("reviewer")
	h.hook("planner")
	dir := filepath.Join(h.dir, "runs")
	r := h.endedRun("review", "reviewer", "review: stopped: tier 2 needs the full model")
	h.c.chain(context.Background(), dir, r)
	got, _ := runs.Load(dir, r.ID)
	if got.Notify != "" || runs.Acked(dir, r.ID) {
		t.Fatalf("a do: line: %+v acked %v", got, runs.Acked(dir, r.ID))
	}
	var rt routed
	if json.Unmarshal(got.Routed, &rt) != nil || len(rt.Do) != 1 || !strings.Contains(rt.Do[0], "job again "+r.ID+" --full") {
		t.Fatalf("stored route: %+v", rt)
	}
}

// A stored route's follow-ups that failed to file are tried again, as
// routing the run again on each pickup did.
func TestChainRefilesFollowUps(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a", "--areas", "api")
	h.move("T-1", "in_progress", "in_review")
	r := h.endedRun("review", "reviewer", "Verdict: GO\n## Follow-ups\n- logs print emails\n## Outcome")
	rt := routed{Send: []send{}, Wrote: []string{"follow-ups not filed: store busy"}}
	h.c.refileFollowUps(context.Background(), h.s, r, &rt)
	if len(rt.Wrote) != 1 || !strings.HasPrefix(rt.Wrote[0], "1 follow-ups from T-1 on T-2") {
		t.Fatalf("wrote: %q", rt.Wrote)
	}
}
