//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// lookHarness runs look as the planner bot does, with a stand-in claude
// for the jobs look starts.
type lookHarness struct{ *jobHarness }

func newLookHarness(t *testing.T) *lookHarness {
	return &lookHarness{newJobHarness(t, "planner")}
}

func (l *lookHarness) look(stdin string, args ...string) []string {
	l.t.Helper()
	out, err := l.cmd(stdin, append([]string{"look"}, args...)...).Output()
	if err != nil {
		l.t.Fatalf("look %v: %v\n%s", args, err, out)
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

func (l *lookHarness) move(id string, to ...string) {
	l.t.Helper()
	for _, st := range to {
		args := []string{"transition", id, "--to", st}
		if st == "done" {
			args = append(args, "--actor-role", "qa-lead")
		}
		l.h.ok(nil, args...)
	}
}

func (l *lookHarness) comment(id, body string) {
	l.t.Helper()
	if code, out, errs := l.h.run(body, "comment", id, "--body-file", "-"); code != 0 {
		l.t.Fatalf("comment %s: %d %s %s", id, code, out, errs)
	}
}

func (l *lookHarness) claim(id, holder string) {
	l.t.Helper()
	l.h.ok(nil, "claim", id, "--holder", holder, "--status", "todo,in_progress,in_review,blocked,backlog")
}

func (l *lookHarness) claimed(id, bot string) {
	l.t.Helper()
	var cl struct{ Claim struct{ Token int64 } }
	l.h.ok(&cl, "claim", id, "--holder", bot+"/s1", "--status", "todo,in_progress,in_review")
	l.h.ok(nil, "release", id, "--holder", bot+"/s1", "--token", strconv.FormatInt(cl.Claim.Token, 10))
}

func with(lines []string, prefix string) []string {
	out := []string{}
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func TestLookRows(t *testing.T) {
	l := newLookHarness(t)
	mk := func(args ...string) { l.h.ok(nil, append([]string{"create", "--title", "x"}, args...)...) }
	for range 13 {
		mk()
	}
	l.h.ok(nil, "update", "T-7", "--tier", "junior")
	l.move("T-1", "in_progress", "in_review")                                             // waiting for review
	l.move("T-2", "in_progress", "in_review")                                             // waiting for QA
	l.comment("T-2", "Review: GO at "+sha)                                                //
	l.claimed("T-3", "junior-engineer")                                                   //
	l.move("T-3", "in_progress", "in_review", "in_progress")                              // sent back once
	l.comment("T-3", "Kickback: at abc1234")                                              //
	l.move("T-4", "in_progress", "in_review", "in_progress", "in_review", "in_progress")  // three times
	l.move("T-4", "in_review", "in_progress")                                             //
	l.comment("T-4", "Kickback: at abc1231\nF1")                                          //
	l.comment("T-4", "Kickback: at abc1232\nF2")                                          //
	l.move("T-5", "blocked")                                                              // a block
	l.comment("T-5", "Escalation: build at abc: which cache key?\nReturn-role: engineer") //
	l.move("T-6", "blocked")                                                              // sent up
	l.comment("T-6", "Sent up: chief of staff: pay for Postmark?")                        //
	l.h.ok(nil, "transition", "T-10", "--to", "backlog", "--parked", "hold")              // held
	l.move("T-11", "blocked")                                                             // answered, not moved back
	l.comment("T-11", "Answer: split it")                                                 //
	l.move("T-12", "in_progress", "in_review")                                            // split, not closed
	l.comment("T-12", "Split into T-1")                                                   //
	l.claim("T-13", "senior-engineer/s9")                                                 // busy

	got := l.look("", "--limit", "3", "--report")
	sends := with(got, "send ")
	want := []string{
		"send reviewer: review T-1",
		"send qa-lead: qa T-2",
		"send junior-engineer: build T-3",
		"send junior-engineer: build T-7",
		"send senior-engineer: build T-8",
	}
	for _, w := range want {
		if !slices.Contains(sends, w) {
			t.Errorf("no %q in\n%s", w, strings.Join(got, "\n"))
		}
	}
	if slices.Contains(sends, "send senior-engineer: build T-9") {
		t.Errorf("past the limit: %s", strings.Join(sends, "\n"))
	}
	cos := with(got, "send chief-of-staff: T-4 What:")
	if len(cos) != 1 || !strings.Contains(cos[0], "sent back 3 times") || !strings.Contains(cos[0], "Question: split, rewrite or drop T-4?") {
		t.Errorf("TRIES 3 card: %q", cos)
	}
	if w := with(got, "wrote: T-4 blocked at TRIES 3, sent up"); len(w) != 1 {
		t.Errorf("TRIES 3 writes: %q", got)
	}
	var d struct {
		Status   string
		Comments []struct{ Body string }
	}
	l.h.ok(&d, "get", "T-4", "--full")
	if d.Status != "blocked" || !strings.HasPrefix(d.Comments[len(d.Comments)-2].Body, "Escalation: build at TRIES 3: sent back each time for:\n- Kickback: at abc1231\n- Kickback: at abc1232") || d.Comments[len(d.Comments)-1].Body != "Sent up: chief of staff: split, rewrite or drop T-4?" {
		t.Errorf("T-4: %+v", d)
	}
	if j := with(got, "judge: "); !slices.Contains(j, "judge: T-5 blocked: Escalation: build at abc: which cache key?") {
		t.Errorf("judge rows: %q", j)
	}
	started := with(got, "started: ")
	slices.Sort(started)
	ri := slices.IndexFunc(started, func(x string) bool { return strings.HasSuffix(x, " rule T-11") })
	if len(started) != 2 || ri < 0 || !slices.ContainsFunc(started, func(x string) bool { return strings.HasSuffix(x, " finish T-12") }) {
		t.Fatalf("starts: %q", started)
	}
	ruleRun := strings.Fields(strings.TrimPrefix(started[ri], "started: "))[0]
	j := l.waitEnded(ruleRun, 20*time.Second)
	if got := l.seen(j.ID); got.stdin[0] != "/maestro-planner rule T-11: again" || got.flag("--model") != "opus" {
		t.Errorf("rule again: %q %q", got.stdin, got.args)
	}
	for _, w := range []string{"runs: 0/3", "held: T-10", "sent up: T-6"} {
		if !slices.Contains(got, w) {
			t.Errorf("report: no %q in %q", w, got)
		}
	}
	if c := with(got, "counts: "); len(c) != 1 || !strings.Contains(c[0], "busy 1") {
		t.Errorf("counts: %q", c)
	}

	// The next look sends none of it again, starts nothing and writes
	// nothing: each is on its way.
	again := l.look("", "--limit", "3")
	for _, p := range []string{"send ", "started: ", "wrote: "} {
		if x := with(again, p); len(x) != 0 {
			t.Errorf("second look: %q", x)
		}
	}
	if len(with(again, "send senior-engineer: build T-9")) != 0 {
		t.Errorf("a handed-out build holds its slot")
	}

	// STOP: hand out nothing.
	os.WriteFile(filepath.Join(l.h.dir, "STOP"), nil, 0o644)
	os.RemoveAll(filepath.Join(l.h.dir, "handouts"))
	stopped := l.look("", "--limit", "3")
	if stopped[0] != "stopped: maestro/STOP is set: look hands out and starts nothing" || len(with(stopped, "send ")) != 0 || len(with(stopped, "judge: ")) != 0 {
		t.Errorf("STOP: %q", stopped)
	}
}

// Tickets no engineer builds go by role: a Design: ticket to the designer,
// a live run to the QA lead, and neither takes a work slot.
func TestLookByRole(t *testing.T) {
	l := newLookHarness(t)
	l.h.ok(nil, "create", "--title", "Design: demo", "--type", "scout", "--role", "designer", "--owner", "planner")
	l.h.ok(nil, "create", "--title", "Live run: demo", "--type", "scout", "--role", "qa-lead", "--owner", "qa-lead")
	l.h.ok(nil, "create", "--title", "a")
	got := with(l.look("", "--limit", "1"), "send ")
	if !slices.Equal(got, []string{"send designer: design T-1", "send qa-lead: live T-2", "send senior-engineer: build T-3"}) {
		t.Fatalf("by role: %q", got)
	}
	_, r := l.job("", "design", "T-1")
	if got := l.seen(r.ID); got.stdin[0] != "/maestro-designer design T-1" || got.flag("--model") != "opus" {
		t.Fatalf("design job: %q %q", got.stdin, got.args)
	}
}

// A follow-up marked urgent goes to the judge; the rest wait for their goal.
func TestLookUrgentFollowUp(t *testing.T) {
	l := newLookHarness(t)
	l.h.ok(nil, "create", "--title", "Follow-ups: api", "--status", "backlog", "--parked", "deferred", "--goal", "demo")
	l.h.ok(nil, "create", "--title", "b", "--goal", "demo")
	l.comment("T-1", "From T-9 review (R-x):\n- urgent: tokens never expire")
	if j := with(l.look("", "--limit", "1"), "judge: "); len(j) != 1 || !strings.HasPrefix(j[0], "judge: T-1 urgent: a follow-up marked urgent") {
		t.Fatalf("urgent: %q", j)
	}
	l.comment("T-1", "Taken: T-3")
	if j := with(l.look("", "--limit", "1"), "judge: "); len(j) != 0 {
		t.Fatalf("taken, and its goal still moving: %q", j)
	}
}

func TestLookJudge(t *testing.T) {
	l := newLookHarness(t)
	l.h.ok(nil, "create", "--title", "x")
	l.move("T-1", "blocked")
	l.comment("T-1", "Escalation: build at abc: which key?\nReturn-role: engineer")
	got := l.look("api/ tickets: senior only. Set by the owner, Oct 31.\n", "--limit", "2", "--judge")
	j := with(got, "judging: ")
	if len(j) != 1 {
		t.Fatalf("judge: %q", got)
	}
	run := strings.Fields(strings.TrimPrefix(j[0], "judging: "))[0]
	r := l.waitEnded(strings.TrimSuffix(run, ","), 20*time.Second)
	seen := l.seen(r.ID)
	want := []string{"/maestro-planner judge T-1", "Rows:", "judge: T-1 blocked: Escalation: build at abc: which key?", "Standing rules:", "api/ tickets: senior only. Set by the owner, Oct 31."}
	if !slices.Equal(seen.stdin, want) || seen.flag("--model") != "opus" {
		t.Fatalf("judge prompt: %q %q", seen.stdin, seen.args)
	}
	if again := l.look("", "--limit", "2", "--judge"); len(with(again, "judg")) != 0 {
		t.Fatalf("judged again while the first runs: %q", again)
	}
}

func TestLookQuietAndSmall(t *testing.T) {
	l := newLookHarness(t)
	if got := l.look("", "--limit", "4"); len(got) != 1 || !strings.HasPrefix(got[0], "nothing to do; open 0;") {
		t.Fatalf("empty board: %q", got)
	}
	// 200 tickets, 140 open, as the planner's look read 25.7K chars of.
	for i := range 200 {
		l.h.ok(nil, "create", "--title", "Add the thing number "+strconv.Itoa(i+1)+" to the checkout flow page", "--goal", "checkout", "--areas", "src/checkout/part"+strconv.Itoa(i%12))
	}
	for i := 1; i <= 110; i++ {
		l.move("T-"+strconv.Itoa(i), "in_progress")
	}
	for i := 1; i <= 85; i++ {
		l.move("T-"+strconv.Itoa(i), "in_review")
	}
	for i := 1; i <= 60; i++ {
		l.move("T-"+strconv.Itoa(i), "done")
	}
	for i := 86; i <= 95; i++ {
		l.move("T-"+strconv.Itoa(i), "blocked")
		l.comment("T-"+strconv.Itoa(i), "Escalation: build at TRIES 3: tests fail\nReturn-role: engineer")
	}
	for i := 161; i <= 200; i++ {
		l.h.ok(nil, "transition", "T-"+strconv.Itoa(i), "--to", "backlog", "--parked", "deferred")
	}
	out, err := l.cmd("", "look", "--limit", "20").Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 5000 {
		t.Fatalf("look printed %d chars on a 140-ticket board; want under 5,000:\n%s", len(out), out)
	}
	t.Logf("look on 140 open tickets: %d chars", len(out))
}

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

// A judge run's tickets are busy while it runs; a failed live run is
// planned again; answered tickets back in review go to the judge as
// reruns, judged once per state.
func TestLookJudgeRows(t *testing.T) {
	l := newLookHarness(t)
	l.h.ok(nil, "create", "--title", "x")
	l.move("T-1", "blocked")
	l.comment("T-1", "Escalation: build at abc: which key?\nReturn-role: engineer")
	slow := filepath.Join(l.bin, "claude")
	os.WriteFile(slow, []byte(strings.Replace(fakeClaude, "printf '{", "sleep 3; printf '{", 1)), 0o755)
	got := l.look("", "--limit", "2", "--judge", "--stall", "1s")
	if len(with(got, "judging: ")) != 1 || len(with(got, "judge: ")) != 0 {
		t.Fatalf("judge: %q", got)
	}
	time.Sleep(1500 * time.Millisecond) // past --stall, the judge still runs
	if again := l.look("", "--limit", "2", "--judge", "--stall", "1s"); len(with(again, "judg")) != 0 {
		t.Fatalf("judged again while the first runs: %q", again)
	}
	os.WriteFile(slow, []byte(fakeClaude), 0o755)

	l.h.ok(nil, "create", "--title", "Live run: demo", "--type", "scout", "--role", "qa-lead", "--owner", "qa-lead")
	l.h.ok(nil, "transition", "T-2", "--to", "backlog", "--parked", "hold")
	l.comment("T-2", "Hold: set by qa-lead: live run failed at abc; lifts when each fix is a dep of this ticket")
	started := with(l.look("", "--limit", "2"), "started: ")
	if len(started) != 1 || !strings.HasSuffix(started[0], " plan T-2") {
		t.Fatalf("a failed live run: %q", started)
	}
	r := l.waitEnded(strings.Fields(strings.TrimPrefix(started[0], "started: "))[0], 20*time.Second)
	if got := l.seen(r.ID); got.stdin[0] != "/maestro-planner fix T-2" {
		t.Fatalf("plan fix: %q", got.stdin)
	}

	l.h.ok(nil, "create", "--title", "y")
	l.move("T-3", "in_progress", "in_review", "in_progress")
	l.comment("T-3", "Kickback: at abc1234\nF1")
	l.move("T-3", "in_review")
	l.comment("T-3", "Answer: F1 falls: the test covers it")
	rows := with(l.look("", "--limit", "0"), "judge: T-3")
	if len(rows) != 1 || !strings.HasPrefix(rows[0], "judge: T-3 rerun: answered, back in review") {
		t.Fatalf("rerun: %q", rows)
	}
}

func TestResumeUsesThisBinary(t *testing.T) {
	exe, _ := os.Executable()
	cmd := []string{"env", "A=1", "/old/path/" + filepath.Base(exe), "slot", "--name", "qa", "--max", "6", "--", "claude", "-p", "--session-id", "S"}
	got := resumeCommand(cmd, "S", false)
	if got[2] != exe {
		t.Fatalf("slot binary: %q", got)
	}
}

// Three blocked tickets that never change are judged once, cause row and
// all; a TRIES 3 card goes up once between two looks.
func TestLookRoundTwo(t *testing.T) {
	l := newLookHarness(t)
	for i := 1; i <= 3; i++ {
		id := "T-" + strconv.Itoa(i)
		l.h.ok(nil, "create", "--title", "x")
		l.move(id, "blocked")
		l.comment(id, "Escalation: build at abc: which key?\nReturn-role: engineer")
	}
	first := with(l.look("", "--limit", "2", "--judge"), "judging: ")
	if len(first) != 1 || !strings.HasSuffix(first[0], "on 4 rows") {
		t.Fatalf("first: %q", first)
	}
	l.waitEnded(strings.Fields(strings.TrimPrefix(first[0], "judging: "))[0], 20*time.Second)
	for range 3 {
		if again := with(l.look("", "--limit", "2", "--judge"), "judg"); len(again) != 0 {
			t.Fatalf("judged again with nothing changed: %q", again)
		}
	}
	if _, ok := urgentSince(ticket.Detail{Comments: []ticket.Comment{{Body: "From T-1: urgent: x"}, {Body: "From T-2: logs"}}}); !ok {
		t.Fatal("a later note hid the urgent one")
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

// A sent-back ticket a fix's dep holds waits; a usage-limited run's
// ticket stays its own.
func TestLookHoldsByDepsAndLimits(t *testing.T) {
	l := newLookHarness(t)
	l.h.ok(nil, "create", "--title", "fix")
	l.h.ok(nil, "create", "--title", "a")
	l.claimed("T-2", "junior-engineer")
	l.move("T-2", "in_progress", "in_review", "in_progress")
	l.comment("T-2", "Kickback: at abc1234")
	l.h.ok(nil, "update", "T-2", "--add-deps", "T-1")
	if s := with(l.look("", "--limit", "5"), "send junior-engineer: build T-2"); len(s) != 0 {
		t.Fatalf("handed out past its dep: %q", s)
	}
	dir := filepath.Join(l.h.dir, "runs")
	r, _ := runs.Create(dir, runs.Record{Launcher: "senior-engineer", Limit: "2h", Command: []string{"true"}}, nil)
	ended := time.Now().Add(-time.Hour)
	r.State, r.Ticket, r.Job, r.EndedAt, r.IsError, r.Reply = runs.Failed, "T-1", "build", &ended, true, "Claude AI usage limit reached|1"
	runs.Save(dir, r)
	if s := with(l.look("", "--limit", "5"), "send senior-engineer: build T-1"); len(s) != 0 {
		t.Fatalf("a limited run's ticket handed out afresh: %q", s)
	}
}

// A held ticket that never changes is judged once, not every look.
func TestLookJudgesAHoldOnce(t *testing.T) {
	l := newLookHarness(t)
	l.h.ok(nil, "create", "--title", "x")
	l.h.ok(nil, "transition", "T-1", "--to", "backlog", "--parked", "hold")
	first := with(l.look("", "--limit", "1", "--judge"), "judging: ")
	if len(first) != 1 {
		t.Fatalf("first: %q", first)
	}
	l.waitEnded(strings.Fields(strings.TrimPrefix(first[0], "judging: "))[0], 20*time.Second)
	for range 3 {
		if again := with(l.look("", "--limit", "1", "--judge"), "judg"); len(again) != 0 {
			t.Fatalf("a hold judged again: %q", again)
		}
	}
}

// pickup drops a send a look made lately, and shows its own again.
func TestPickupSendsDedupe(t *testing.T) {
	dir, now := t.TempDir(), time.Now()
	claimHandout(dir, "T-1", "qa-lead", "qa T-1", time.Minute, now) // a look's
	got := pickupSends(dir, "R-aaaaaaaa", []send{{"qa-lead", "qa T-1"}, {"reviewer", "review T-2"}, {"planner", "slot free T-3"}}, now)
	if len(got) != 2 || got[0].Bot != "reviewer" || got[1].Bot != "planner" {
		t.Fatalf("first pickup: %+v", got)
	}
	again := pickupSends(dir, "R-aaaaaaaa", []send{{"reviewer", "review T-2"}}, now)
	if len(again) != 1 {
		t.Fatalf("a second pickup of the run drops its own send: %+v", again)
	}
	if other := pickupSends(dir, "R-bbbbbbbb", []send{{"reviewer", "review T-2"}}, now); len(other) != 0 {
		t.Fatalf("another run's pickup sends it again: %+v", other)
	}
}
