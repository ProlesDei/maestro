package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/sqlitestore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

type alertsJSON struct {
	Restarted bool
	Died      []struct {
		ID      int64
		Actor   string
		Ticket  string
		Outcome string
		Freed   string
	}
	Expired   []struct{ Ticket, Holder string }
	ETAMissed []struct {
		Goal string
		Open []string
	} `json:"eta_missed"`
	Revived []struct {
		Ticket, Holder string
		Revivals       int
	}
	Stalled []struct{ Ticket, Status string }
}

// clockAt pins the harness's clock and returns a way to move it.
func clockAt(h *harness, t time.Time) func(time.Duration) {
	now := t
	h.now = func() time.Time { return now }
	return func(d time.Duration) { now = now.Add(d) }
}

func (h *harness) store() *sqlitestore.Store {
	h.t.Helper()
	s, err := sqlitestore.Open(context.Background(), filepath.Join(h.dir, "tickets.db"), sqlitestore.Options{Now: h.now})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { s.Close() })
	return s
}

// T-123 and T-167 went to review and sat 3 hours with no one on them,
// since nothing told the planner. The planner's look now shows them.
func TestAlertsShowStalledHandoffs(t *testing.T) {
	h := newHarness(t)
	advance := clockAt(h, time.Now())
	if code, out, _ := h.run("", "alerts"); code != 0 || out != "alerts: none\n" {
		t.Fatalf("empty store: %d %q", code, out)
	}
	h.ok(nil, "create", "--title", "a")
	h.ok(nil, "create", "--title", "b")
	for _, id := range []string{"T-1", "T-2"} {
		h.ok(nil, "transition", id, "--to", "in_progress")
		h.ok(nil, "transition", id, "--to", "in_review")
	}
	h.ok(nil, "claim", "T-2", "--holder", "reviewer/r", "--status", "in_review", "--ttl", "2h")
	advance(19 * time.Minute)
	var r alertsJSON
	h.ok(&r, "alerts")
	if len(r.Stalled) != 0 {
		t.Fatalf("stalled before 20m: %+v", r.Stalled)
	}
	advance(2 * time.Minute)
	h.ok(&r, "alerts")
	if len(r.Stalled) != 1 || r.Stalled[0].Ticket != "T-1" {
		t.Fatalf("stalled = %+v; want only T-1, since T-2 has a reviewer on it", r.Stalled)
	}
	if code, out, _ := h.run("", "alerts", "--stall", "1h"); code != 0 || out != "alerts: none\n" {
		t.Fatalf("--stall 1h: %d %q", code, out)
	}
	if _, out, _ := h.run("", "alerts"); !strings.Contains(out, "stalled: T-1 in_review 21m with no one on it") {
		t.Fatalf("human: %q", out)
	}
}

// T-181's review lease lapsed twice and came back with no word.
func TestAlertsShowRevivedAndExpiredLeases(t *testing.T) {
	h := newHarness(t)
	advance := clockAt(h, time.Now())
	h.ok(nil, "create", "--title", "a")
	h.ok(nil, "create", "--title", "b")
	var c tk
	h.ok(&c, "claim", "T-1", "--holder", "reviewer/r", "--ttl", "5m")
	h.ok(nil, "claim", "T-2", "--holder", "senior-engineer/s", "--ttl", "5m")
	advance(10 * time.Minute)
	h.ok(nil, "heartbeat", "T-1", "--holder", "reviewer/r", "--token", "1", "--ttl", "5m")
	var r alertsJSON
	h.ok(&r, "alerts")
	if len(r.Revived) != 1 || r.Revived[0].Ticket != "T-1" || r.Revived[0].Revivals != 1 {
		t.Fatalf("revived = %+v", r.Revived)
	}
	if len(r.Expired) != 1 || r.Expired[0].Ticket != "T-2" || r.Expired[0].Holder != "senior-engineer/s" {
		t.Fatalf("expired = %+v", r.Expired)
	}
	// The look freed it, so the next look doesn't say it again.
	h.ok(&r, "alerts")
	if len(r.Expired) != 0 || len(r.Revived) != 1 {
		t.Fatalf("second look = %+v", r)
	}
	if _, out, _ := h.run("", "list"); !strings.Contains(out, "reviewer/r (revived 1x)") {
		t.Fatalf("list hides the revival: %q", out)
	}
}

// The box rebooted at 6:17 PM; finding which tickets the dead runs
// touched took a manual look. The first look after names each one and
// frees its claim.
func TestAlertsEndDeadRunsAndFreeTheirClaims(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "dead run's")
	h.ok(nil, "create", "--title", "live run's")
	h.ok(nil, "create", "--title", "other bot's")
	h.ok(nil, "claim", "T-1", "--holder", "senior-engineer/a")
	h.ok(nil, "claim", "T-2", "--holder", "senior-engineer/b")
	h.ok(nil, "claim", "T-3", "--holder", "qa-lead/c")
	s := h.store()
	ctx := context.Background()
	dead, live := startSleeper(t), startSleeper(t)
	start := func(actor string, id ticket.ID, pid int) {
		t.Helper()
		if _, err := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: actor, Ticket: id, PID: pid, Stamp: procStamp(pid)}); err != nil {
			t.Fatal(err)
		}
	}
	start("senior-engineer", 1, dead.Process.Pid)
	start("senior-engineer", 2, live.Process.Pid)
	start("senior-engineer", 3, dead.Process.Pid) // T-3's claim isn't this bot's
	dead.Process.Kill()
	dead.Wait()
	var r alertsJSON
	h.ok(&r, "alerts")
	if len(r.Died) != 2 || r.Died[0].Ticket != "T-1" || r.Died[0].Freed != "senior-engineer/a" ||
		r.Died[1].Ticket != "T-3" || r.Died[1].Freed != "" || r.Died[0].Outcome != "died" || r.Restarted {
		t.Fatalf("died = %+v, restarted %v", r.Died, r.Restarted)
	}
	var got tk
	h.ok(&got, "get", "T-1")
	if got.Claim != nil {
		t.Fatalf("T-1 still claimed: %+v", got.Claim)
	}
	for _, id := range []string{"T-2", "T-3"} {
		if h.ok(&got, "get", id); got.Claim == nil {
			t.Fatalf("%s lost its claim", id)
		}
	}
	var exp struct {
		Tickets []struct {
			LeaseExpiries int `json:"lease_expiries"`
		}
	}
	h.ok(&exp, "export")
	if exp.Tickets[0].LeaseExpiries != 1 {
		t.Fatalf("T-1's release didn't count as an expiry: %+v", exp.Tickets)
	}
	h.ok(&r, "alerts")
	if len(r.Died) != 0 {
		t.Fatalf("a dead run was reported twice: %+v", r.Died)
	}
}

func TestAlertsSeeARestart(t *testing.T) {
	if procStamp(os.Getpid()) == "" {
		t.Skip("no process stamps on this system")
	}
	h := newHarness(t)
	h.ok(nil, "create", "--title", "x")
	s := h.store()
	parts := strings.SplitN(procStamp(os.Getpid()), "|", 3)
	// Same pid, same namespace, another boot: this very process, before a
	// restart, would look alive by pid alone.
	stamp := "another-boot|" + parts[1] + "|" + parts[2]
	if _, err := s.StartLaunch(context.Background(), ticket.StartLaunchRequest{Actor: "planner", PID: os.Getpid(), Stamp: stamp}); err != nil {
		t.Fatal(err)
	}
	var r alertsJSON
	h.ok(&r, "alerts")
	if !r.Restarted || len(r.Died) != 1 {
		t.Fatalf("alerts = %+v", r)
	}
	// A run seen from another pid namespace (a container) can't be judged
	// here: it stays open.
	other := parts[0] + "|pid:[1]|" + parts[2]
	if _, err := s.StartLaunch(context.Background(), ticket.StartLaunchRequest{Actor: "planner", PID: 999999, Stamp: other}); err != nil {
		t.Fatal(err)
	}
	h.ok(&r, "alerts")
	if len(r.Died) != 0 {
		t.Fatalf("another namespace's run died: %+v", r.Died)
	}
}

// log-run records each run as it starts, so one that never gives a reply
// still shows, and how it ended.
func TestLogRunRecordsTheLaunch(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "a")
	if code, _, _ := h.run(reply("s1", 1, "m"), "log-run", "--actor", "junior-engineer", "--ticket", "T-1"); code != 0 {
		t.Fatalf("log-run exit %d", code)
	}
	if code, _, _ := h.run("", "log-run", "--actor", "junior-engineer", "--ticket", "T-9"); code != 2 {
		t.Fatalf("empty reply exit %d", code)
	}
	var exp struct {
		Launches []struct {
			Actor, Ticket, Outcome, Session string
			PID                             int
		}
	}
	h.ok(&exp, "export")
	if len(exp.Launches) != 2 {
		t.Fatalf("launches = %+v", exp.Launches)
	}
	a, b := exp.Launches[0], exp.Launches[1]
	if a.Actor != "junior-engineer" || a.Ticket != "T-1" || a.Outcome != "result" || a.Session != "s1" || a.PID != os.Getpid() {
		t.Fatalf("first = %+v", a)
	}
	if b.Outcome != "no_result" || b.Ticket != "" {
		t.Fatalf("second = %+v", b)
	}
}

// The chief of staff told the owner 9:15 PM; nothing merged and no one
// noticed until the owner asked at 9:40.
func TestETAMissedShowsInAlerts(t *testing.T) {
	h := newHarness(t)
	advance := clockAt(h, time.Date(2026, 11, 12, 20, 0, 0, 0, time.Local))
	h.ok(nil, "create", "--title", "a", "--goal", "demo")
	h.ok(nil, "create", "--title", "b", "--goal", "demo")
	if code := h.fails(2, "eta", "--goal", "demo", "--by", "19:00"); code != "usage" {
		t.Fatalf("a past time: %s", code)
	}
	h.fails(2, "eta", "--goal", "demo")
	h.ok(nil, "eta", "--goal", "demo", "--by", "21:15")
	var list struct {
		ETAs []struct{ Goal, State string } `json:"etas"`
	}
	h.ok(&list, "eta")
	if len(list.ETAs) != 1 || list.ETAs[0].State != "open" {
		t.Fatalf("etas = %+v", list)
	}
	advance(80 * time.Minute)
	var r alertsJSON
	h.ok(&r, "alerts")
	if len(r.ETAMissed) != 1 || r.ETAMissed[0].Goal != "demo" || len(r.ETAMissed[0].Open) != 2 {
		t.Fatalf("eta missed = %+v", r.ETAMissed)
	}
	if _, out, _ := h.run("", "alerts"); !strings.Contains(out, "eta missed: demo due Nov 12 21:15, 5m ago; open: T-1 todo, T-2 todo") {
		t.Fatalf("human: %q", out)
	}
	// A new promise for one ticket replaces the old one.
	h.ok(nil, "eta", "--goal", "demo", "--by", "30m", "--tickets", "T-1")
	advance(time.Hour)
	h.ok(nil, "transition", "T-1", "--to", "in_progress")
	h.ok(nil, "transition", "T-1", "--to", "in_review")
	h.ok(nil, "transition", "T-1", "--to", "done", "--actor-role", "qa-lead")
	h.ok(&r, "alerts")
	if len(r.ETAMissed) != 0 {
		t.Fatalf("met ETA reported: %+v", r.ETAMissed)
	}
	// A promised ticket that was cancelled was never delivered.
	h.ok(nil, "eta", "--goal", "demo", "--by", "30m", "--tickets", "T-2")
	h.ok(nil, "transition", "T-2", "--to", "cancelled")
	advance(time.Hour)
	h.ok(&r, "alerts")
	if len(r.ETAMissed) != 1 || r.ETAMissed[0].Open[0] != "T-2" {
		t.Fatalf("cancelled promise = %+v", r.ETAMissed)
	}
	h.ok(nil, "eta", "--goal", "demo", "--clear")
	h.ok(&list, "eta")
	if len(list.ETAs) != 0 {
		t.Fatalf("cleared ETA still listed: %+v", list)
	}
}

// A run that ends with no reply frees the claim its bot left, as the team
// rules had each bot do by hand; another bot's claim stays.
func TestLogRunFreesTheClaimOfARunWithNoResult(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.ok(nil, "create", "--title", "b")
	h.ok(nil, "claim", "T-1", "--holder", "junior-engineer/x")
	h.ok(nil, "claim", "T-2", "--holder", "senior-engineer/y")
	code, _, errs := h.run("", "log-run", "--actor", "junior-engineer", "--ticket", "T-1")
	if code != 2 || !strings.Contains(errs, "freed junior-engineer/x's claim on T-1") {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	if code, _, _ := h.run("", "log-run", "--actor", "junior-engineer", "--ticket", "T-2"); code != 2 {
		t.Fatalf("exit %d", code)
	}
	var a, b tk
	h.ok(&a, "get", "T-1")
	h.ok(&b, "get", "T-2")
	if a.Claim != nil || b.Claim == nil {
		t.Fatalf("T-1 claim %+v, T-2 claim %+v", a.Claim, b.Claim)
	}
	// A run with a reply frees nothing: its session let go, or still holds
	// the ticket on purpose (a question waits on it).
	h.ok(nil, "claim", "T-1", "--holder", "junior-engineer/z")
	if code, _, _ := h.run(reply("s1", 1, "m"), "log-run", "--actor", "junior-engineer", "--ticket", "T-1"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if h.ok(&a, "get", "T-1"); a.Claim == nil {
		t.Fatal("a run with a reply lost its claim")
	}
}
