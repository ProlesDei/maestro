//go:build unix

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// launcher runs maestro-ticket as its own process, as a bot does, so the
// supervisor it starts is a real detached process.
type launcher struct {
	t     *testing.T
	h     *harness
	exe   string
	actor string
}

func newLauncher(t *testing.T, actor string) *launcher {
	t.Helper()
	needPgrep(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.env["MAESTRO_TICKET_ACTOR"] = actor
	h.ok(nil, "doctor") // makes the store
	l := &launcher{t: t, h: h, exe: exe, actor: actor}
	t.Cleanup(l.waitSupervisors)
	return l
}

func (l *launcher) cmd(stdin string, args ...string) *exec.Cmd {
	cmd := exec.Command(l.exe, args...)
	cmd.Dir = l.h.dir
	cmd.Env = append(os.Environ(), childEnv+"=1", "MAESTRO_TICKET_STORE="+l.h.env["MAESTRO_TICKET_STORE"], "MAESTRO_TICKET_ACTOR="+l.actor)
	cmd.Stdin = strings.NewReader(stdin)
	return cmd
}

// launch starts a run and returns its record as launch printed it.
func (l *launcher) launch(stdin string, args ...string) runs.Record {
	l.t.Helper()
	out, err := l.cmd(stdin, append([]string{"launch", "--json"}, args...)...).Output()
	if err != nil {
		l.t.Fatalf("launch %v: %v\n%s", args, err, out)
	}
	var r runs.Record
	if err := json.Unmarshal(out, &r); err != nil {
		l.t.Fatalf("launch: %v: %s", err, out)
	}
	return r
}

func (l *launcher) get(id string) runs.Record {
	l.t.Helper()
	var r runs.Record
	l.h.ok(&r, "runs", id)
	return r
}

// waitEnded waits for a run to end and returns it.
func (l *launcher) waitEnded(id string, within time.Duration) runs.Record {
	l.t.Helper()
	deadline := time.Now().Add(within)
	for {
		r := l.get(id)
		if r.Ended() {
			return r
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("run %s still %s after %s", id, r.State, within)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type pickupResult struct {
	Ended   []runs.Record
	Running []runs.Record
}

func (l *launcher) pickup() pickupResult {
	l.t.Helper()
	var p pickupResult
	l.h.ok(&p, "pickup")
	return p
}

// ackAll acks every run pickup shows.
func (l *launcher) ackAll() {
	l.t.Helper()
	for _, r := range l.pickup().Ended {
		l.h.ok(nil, "ack", r.ID)
	}
}

// waitSupervisors waits for every supervisor on this store to exit, so the
// temp dir can go.
func (l *launcher) waitSupervisors() {
	store := l.h.env["MAESTRO_TICKET_STORE"]
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if exec.Command("pgrep", "-f", "supervise R-.* --store "+store).Run() != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	exec.Command("pkill", "-f", "supervise R-.* --store "+store).Run()
	l.t.Error("a supervisor outlived the test")
}

// hook is a webhook that counts its rings and keeps the auth each sent.
type hook struct {
	mu    sync.Mutex
	rings []runs.Notification
	auths []string
	ch    chan struct{}
}

func newHook(t *testing.T) (*hook, string) {
	k := &hook{ch: make(chan struct{}, 100)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n runs.Notification
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &n)
		k.mu.Lock()
		k.rings = append(k.rings, n)
		k.auths = append(k.auths, r.Header.Get("Authorization"))
		k.mu.Unlock()
		k.ch <- struct{}{}
	}))
	t.Cleanup(srv.Close)
	return k, srv.URL + "/hook/secret-token"
}

func (k *hook) lastAuth() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.auths[len(k.auths)-1]
}

func (k *hook) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.rings)
}

func (k *hook) wait(t *testing.T, within time.Duration) runs.Notification {
	t.Helper()
	select {
	case <-k.ch:
	case <-time.After(within):
		t.Fatalf("no ring within %s", within)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.rings[len(k.rings)-1]
}

const doneReply = `{"type":"result","subtype":"success","is_error":false,"result":"build: done: T-1 at abc","session_id":"sess-1","total_cost_usd":0.25,"modelUsage":{}}`

// A bot launches a run, gets its id back at once, and is rung, with its
// key, when it ends. pickup shows the result until the bot acks it; only
// one ack counts, however many come at once; and the ack stops the rings.
// The run's cost is logged against its ticket.
func TestLaunchRingsUntilAckedOnce(t *testing.T) {
	l := newLauncher(t, "planner")
	k, url := newHook(t)
	code, out, errs := l.h.run(url, "notify", "planner", "--url-file", "-")
	if code != 0 || strings.Contains(out+errs, "secret-token") {
		t.Fatalf("notify: exit %d, printed the URL or failed:\n%s%s", code, out, errs)
	}
	code, out, errs = l.h.run("sender-key-1\n", "notify", "planner", "--key-file", "-")
	if code != 0 || strings.Contains(out+errs, "sender-key-1") || !strings.Contains(out, "with its key") {
		t.Fatalf("notify --key-file: exit %d, printed the key or failed:\n%s%s", code, out, errs)
	}
	l.h.ok(nil, "create", "--title", "a")

	start := time.Now()
	r := l.launch("the prompt\n", "--ticket", "t-1", "--rings", "0s,1s,30s", "--",
		"sh", "-c", `read p; echo "got $p" >&2; sleep 3; echo "$0"`, doneReply)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("launch blocked for %s", took)
	}
	if r.State != runs.Running || r.Ticket != "T-1" || r.Launcher != "planner" || r.Limit != "2h" || r.Host == "" {
		t.Fatalf("launch: %+v", r)
	}
	if p := l.pickup(); len(p.Ended) != 0 || len(p.Running) != 1 {
		t.Fatalf("pickup while running: %+v", p)
	}
	l.h.fails(4, "ack", r.ID) // not ended yet

	n := k.wait(t, 10*time.Second)
	if n.Run != r.ID || n.Ticket != "T-1" || n.Launcher != "planner" || n.State != runs.Done || !strings.Contains(n.Text, "maestro-ticket pickup") {
		t.Fatalf("ring: %+v", n)
	}
	if a := k.lastAuth(); a != "Bearer sender-key-1" {
		t.Fatalf("ring auth: %q", a)
	}
	k.wait(t, 5*time.Second) // not acked: it rings again

	// pickup shows it every time until it is acked.
	for range 2 {
		p := l.pickup()
		if len(p.Ended) != 1 {
			t.Fatalf("pickup: %+v", p)
		}
		e := p.Ended[0]
		if e.ID != r.ID || e.State != runs.Done || *e.Exit != 0 || e.Reply != "build: done: T-1 at abc" || e.SessionID != "sess-1" {
			t.Fatalf("result: %+v", e)
		}
	}
	var mu sync.Mutex
	won := 0
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var a struct{ Acks []struct{ Acked bool } }
			l.h.ok(&a, "ack", r.ID)
			mu.Lock()
			if a.Acks[0].Acked {
				won++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("20 acks: %d counted; want 1", won)
	}
	if p := l.pickup(); len(p.Ended) != 0 {
		t.Fatalf("pickup after ack: %+v", p)
	}
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(l.h.env["MAESTRO_TICKET_STORE"]), "runs", r.ID+".err"))
	if string(b) != "got the prompt\n" {
		t.Fatalf("the command's stdin: %q", b)
	}
	// Acked: the 30s ring never comes, and the supervisor exits.
	rings := k.count()
	l.waitSupervisors() // it sees the ack within seconds, not at the 30s ring
	if k.count() != rings {
		t.Fatal("rang after the ack")
	}
	var costs struct {
		Rows []struct {
			Key string
			USD float64
		}
	}
	l.h.ok(&costs, "costs", "--by", "ticket")
	if len(costs.Rows) != 1 || costs.Rows[0].Key != "T-1" || costs.Rows[0].USD != 0.25 {
		t.Fatalf("costs: %+v", costs)
	}

	code, out, _ = l.h.run("", "pickup")
	if code != 0 || !strings.Contains(out, "no run of yours ended or running") {
		t.Fatalf("human pickup: %d %q", code, out)
	}
	code, out, _ = l.h.run("", "ack", r.ID)
	if code != 0 || !strings.Contains(out, "already acked by planner") {
		t.Fatalf("second ack: %d %q", code, out)
	}
}

// The time limit stops the whole process group and records timed_out.
// The run's claim on its ticket goes, counted as a lease expiry.
func TestLaunchTimeLimit(t *testing.T) {
	l := newLauncher(t, "qa-lead")
	l.h.ok(nil, "create", "--title", "a", "--role", "qa-lead")
	l.h.ok(nil, "claim", "T-1", "--holder", "qa-lead/s1", "--ttl", "1h")
	r := l.launch("", "--ticket", "T-1", "--limit", "1s", "--", "sh", "-c", "sleep 300 & echo $! > child; wait")
	e := l.waitEnded(r.ID, 15*time.Second)
	if e.State != runs.TimedOut || *e.Exit != 128+int(syscall.SIGTERM) || !strings.Contains(e.Error, "1s time limit") {
		t.Fatalf("timed out run: %+v", e)
	}
	b, err := os.ReadFile(filepath.Join(l.h.dir, "child"))
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	fmt.Sscan(string(b), &pid)
	time.Sleep(200 * time.Millisecond)
	if syscall.Kill(pid, 0) == nil && !zombie(pid) {
		t.Fatalf("the run's child %d outlived its limit", pid)
	}
	var p pickupResult
	l.h.ok(&p, "pickup")
	if len(p.Ended) != 1 || p.Ended[0].State != runs.TimedOut {
		t.Fatalf("pickup: %+v", p)
	}
	l.waitSupervisors()
	var tk struct {
		Claim         *struct{ Holder string }
		LeaseExpiries int `json:"lease_expiries"`
	}
	l.h.ok(&tk, "get", "T-1")
	if tk.Claim != nil || tk.LeaseExpiries != 1 {
		t.Fatalf("ticket after a timed-out run: %+v", tk)
	}
}

// A run that crashes is failed with its exit code. One whose supervisor
// is killed, as a restart kills it, is died: on Linux the command goes
// with it, and the first pickup or doctor, whoever runs it, rings its
// launcher once, saves it as died and frees its ticket. doctor warns while
// a result waits for an ack, without failing.
func TestLaunchCrashAndDeath(t *testing.T) {
	l := newLauncher(t, "reviewer")
	crash := l.launch("", "--", "sh", "-c", "echo boom >&2; kill -9 $$")
	e := l.waitEnded(crash.ID, 10*time.Second)
	if e.State != runs.Failed || *e.Exit != 137 || !strings.Contains(e.Error, "boom") {
		t.Fatalf("crashed run: %+v", e)
	}

	k, url := newHook(t)
	l.h.env["MAESTRO_TICKET_ACTOR"] = "junior-engineer"
	l.h.run(url, "notify", "junior-engineer", "--url-file", "-")
	l.h.ok(nil, "create", "--title", "a", "--role", "engineer")
	l.h.ok(nil, "claim", "T-1", "--holder", "junior-engineer/s1", "--ttl", "1h")
	jr := &launcher{t: t, h: l.h, exe: l.exe, actor: "junior-engineer"}
	gone := jr.launch("", "--ticket", "T-1", "--", "sleep", "300")
	if err := exec.Command("pkill", "-KILL", "-f", "supervise "+gone.ID).Run(); err != nil {
		t.Fatalf("kill supervisor: %v", err)
	}
	if d := jr.waitEnded(gone.ID, 10*time.Second); d.State != runs.Died {
		t.Fatalf("run whose supervisor died: %+v", d)
	}
	if runtime.GOOS == "linux" {
		time.Sleep(300 * time.Millisecond)
		if syscall.Kill(gone.PID, 0) == nil && !zombie(gone.PID) {
			t.Fatalf("command %d outlived its supervisor", gone.PID)
		}
	}

	// Another bot's pickup is the first look: it rings the launcher.
	l.h.env["MAESTRO_TICKET_ACTOR"] = "reviewer"
	if p := l.pickup(); len(p.Ended) != 1 || p.Ended[0].ID != crash.ID {
		t.Fatalf("reviewer's pickup: %+v", p)
	}
	if n := k.wait(t, 5*time.Second); n.Run != gone.ID || n.State != runs.Died || n.Launcher != "junior-engineer" {
		t.Fatalf("orphan ring: %+v", n)
	}
	l.pickup()
	var doc struct {
		OK     bool
		Checks []struct {
			Name, Detail string
			OK, Warn     bool
		}
	}
	l.h.ok(&doc, "doctor") // warns; doesn't fail
	if k.count() != 1 {
		t.Fatalf("orphan rang %d times; want 1", k.count())
	}
	l.h.ok(&doc, "doctor")
	last := doc.Checks[len(doc.Checks)-1]
	if !doc.OK || last.Name != "runs" || !last.Warn || !strings.Contains(last.Detail, gone.ID+" (junior-engineer, died)") {
		t.Fatalf("doctor with a died run unacked: %+v", doc)
	}
	var tk struct {
		Claim         *struct{ Holder string }
		LeaseExpiries int `json:"lease_expiries"`
	}
	l.h.ok(&tk, "get", "T-1")
	if tk.Claim != nil || tk.LeaseExpiries != 1 {
		t.Fatalf("ticket of a died run: %+v", tk)
	}

	l.h.fails(2, "ack", gone.ID) // only its launcher acks it
	l.h.env["MAESTRO_TICKET_ACTOR"] = "junior-engineer"
	p := jr.pickup()
	if len(p.Ended) != 1 || p.Ended[0].State != runs.Died || !strings.Contains(p.Ended[0].Error, "machine restarted") {
		t.Fatalf("launcher's pickup: %+v", p)
	}
	jr.ackAll()
	l.h.env["MAESTRO_TICKET_ACTOR"] = "reviewer"
	l.ackAll()
	doc.Checks = nil // json reuses the old elements, and omits warn when false
	l.h.ok(&doc, "doctor")
	if last := doc.Checks[len(doc.Checks)-1]; last.Warn {
		t.Fatalf("doctor after acks: %+v", last)
	}
}

func TestLaunchRefusals(t *testing.T) {
	l := newLauncher(t, "senior-engineer")
	for _, args := range [][]string{
		{"launch"},
		{"launch", "--", "timeout", "-s", "KILL", "2h", "claude", "-p"},
		{"launch", "--", "timeout", "2h"},
		{"launch", "--limit", "0s", "--", "true"},
		{"launch", "--rings", "5m,1m", "--", "true"},
		{"launch", "--ticket", "nope", "--", "true"},
	} {
		if code, _, errs := l.h.run("", args...); code != 2 || !strings.Contains(errs, "maestro-ticket: ") {
			t.Fatalf("%v: exit %d, %q; want a usage error", args, code, errs)
		}
	}
	delete(l.h.env, "MAESTRO_TICKET_ACTOR")
	l.h.fails(2, "launch", "--", "true")
	l.h.fails(2, "pickup")
	l.h.env["MAESTRO_TICKET_ACTOR"] = "senior-engineer"

	// A command that can't start fails launch, and only there.
	out, err := l.cmd("", "launch", "--", "/no/such/claude", "-p").CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 || !strings.Contains(string(out), "no such file") {
		t.Fatalf("launch of a missing command: %v\n%s", err, out)
	}
	if p := l.pickup(); len(p.Ended) != 0 || len(p.Running) != 0 {
		t.Fatalf("pickup reported a start failure again: %+v", p)
	}
	l.h.fails(2, "ack")
	l.h.fails(3, "ack", "R-aaaaaaaa")
}

// NAME=value words before the command set its environment, as a shell's
// would.
func TestLaunchEnvPrefix(t *testing.T) {
	l := newLauncher(t, "planner")
	r := l.launch("", "--", "MAESTRO_X=lean", "sh", "-c", `echo "x=$MAESTRO_X"`)
	if r.Command[0] != "env" {
		t.Fatalf("command: %v", r.Command)
	}
	if e := l.waitEnded(r.ID, 10*time.Second); e.State != runs.Done || e.Reply != "x=lean" {
		t.Fatalf("run: %+v", e)
	}
	l.ackAll()
}

// A claude -p started outside launch in a worktree of the repo shows in
// runs and makes doctor warn; one launch started doesn't.
func TestUntrackedSessions(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc")
	}
	l := newLauncher(t, "planner")
	repo := filepath.Join(l.h.dir, "repo")
	os.Mkdir(repo, 0o755)
	git(t, repo, "init", "-q")
	l.h.dir = repo
	// A copy of sh named claude stands in for Claude Code.
	sh, _ := exec.LookPath("sh")
	b, err := os.ReadFile(sh)
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(fake, b, 0o755); err != nil {
		t.Fatal(err)
	}
	direct := exec.Command(fake, "-c", "sleep 300; true", "-p")
	direct.Dir = repo
	if err := direct.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { direct.Process.Kill(); direct.Wait() }()
	launched := l.launch("", "--", fake, "-c", "sleep 300; true", "-p")
	defer exec.Command("kill", fmt.Sprint(launched.PID)).Run()

	var res struct {
		Runs      []runs.Record
		Untracked []runs.Session
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		l.h.ok(&res, "runs")
		if len(res.Untracked) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(res.Untracked) != 1 || res.Untracked[0].PID != direct.Process.Pid || len(res.Runs) != 1 {
		t.Fatalf("runs: %+v (direct pid %d, launched pid %d)", res, direct.Process.Pid, launched.PID)
	}
	code, out, _ := l.h.run("", "doctor")
	if code != 0 || !strings.Contains(out, "warn  runs") || !strings.Contains(out, "started outside maestro-ticket launch") {
		t.Fatalf("doctor: %d %s", code, out)
	}
}

func TestNotifyCommand(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "doctor")
	k, url := newHook(t)
	h.fails(2, "notify", "Bad_Bot", "--url-file", "-")
	if code, _, _ := h.run("ftp://x", "notify", "planner", "--url-file", "-"); code != 2 {
		t.Fatalf("ftp URL: exit %d", code)
	}
	h.fails(3, "notify", "planner", "--test")
	if code, _, errs := h.run(url+"\n", "notify", "planner", "--url-file", "-"); code != 0 {
		t.Fatal(errs)
	}
	info, err := os.Stat(filepath.Join(h.dir, "notify", "planner.url"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("webhook file: %v %v", info, err)
	}
	code, out, _ := h.run("", "notify", "planner", "--test")
	if code != 0 || strings.Contains(out, "secret-token") || k.wait(t, 5*time.Second).State != "test" {
		t.Fatalf("test ring: %d %s", code, out)
	}
	var list struct{ Bots []string }
	h.ok(&list, "notify")
	if len(list.Bots) != 1 || list.Bots[0] != "planner" {
		t.Fatalf("list: %+v", list)
	}
	h.fails(2, "notify", "qa-lead", "--key-file", "-") // no URL yet
	if code, _, errs := h.run("k-123\n", "notify", "planner", "--key-file", "-"); code != 0 {
		t.Fatal(errs)
	}
	info, err = os.Stat(filepath.Join(h.dir, "notify", "planner.key"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", info, err)
	}
	code, out, _ = h.run("", "notify", "planner", "--test")
	if code != 0 || strings.Contains(out, "k-123") || k.lastAuth() != "Bearer k-123" {
		t.Fatalf("test ring with key: %d %s %q", code, out, k.lastAuth())
	}
	h.ok(nil, "notify", "planner", "--clear")
	if _, err := os.Stat(filepath.Join(h.dir, "notify", "planner.key")); err == nil {
		t.Fatal("--clear left the key")
	}
	h.ok(&list, "notify")
	if len(list.Bots) != 0 {
		t.Fatalf("after clear: %+v", list)
	}
}

// A bot that writes "timeout 1s" before the command, as the older
// time-limit rule says, gets that as the limit; sub-second rings reach the
// supervisor as given; and a stdin pipe nobody closes doesn't hang launch.
func TestLaunchTimeoutPrefixRingsAndOpenStdin(t *testing.T) {
	l := newLauncher(t, "qa-lead")
	k, url := newHook(t)
	l.h.run(url, "notify", "qa-lead", "--url-file", "-")

	r := l.launch("", "--rings", "0s,400ms", "--", "timeout", "1s", "sleep", "300")
	if r.Limit != "1s" || r.Command[0] != "sleep" {
		t.Fatalf("timeout prefix: %+v", r)
	}
	if e := l.waitEnded(r.ID, 15*time.Second); e.State != runs.TimedOut {
		t.Fatalf("run: %+v", e)
	}
	k.wait(t, 5*time.Second)
	k.wait(t, 5*time.Second) // the 400ms ring
	l.ackAll()
	if code, _, _ := l.h.run("", "launch", "--limit", "5s", "--", "timeout", "1s", "true"); code != 2 {
		t.Fatalf("--limit and timeout both: exit %d", code)
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Close()
	cmd := l.cmd("", "launch", "--json", "--rings", "0s", "--", "true")
	cmd.Stdin = pr
	start := time.Now()
	out, err := cmd.Output()
	pr.Close()
	if err != nil || time.Since(start) > 8*time.Second {
		t.Fatalf("launch with an open stdin: %v after %s\n%s", err, time.Since(start), out)
	}
	var o runs.Record
	json.Unmarshal(out, &o)
	l.waitEnded(o.ID, 10*time.Second)
	l.ackAll()
}

// A supervisor stopped as at a shutdown records the run failed and leaves
// the ring to the next look; a ring that fails there is tried again on a
// later look, and once one gets through, no look rings again.
func TestStoppedSupervisorRingsOnNextLook(t *testing.T) {
	l := newLauncher(t, "planner")
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL + "/hook"
	dead.Close()
	l.h.run(deadURL, "notify", "planner", "--url-file", "-")
	r := l.launch("", "--", "sleep", "300")
	if err := exec.Command("pkill", "-TERM", "-f", "supervise "+r.ID).Run(); err != nil {
		t.Fatalf("stop supervisor: %v", err)
	}
	e := l.waitEnded(r.ID, 15*time.Second)
	l.waitSupervisors()
	if e.State != runs.Failed || e.Rings != 0 || !strings.Contains(e.Error, "launcher was stopped") {
		t.Fatalf("stopped run: %+v", e)
	}
	l.pickup() // the look rings, and the dead webhook fails it
	if e = l.get(r.ID); e.Rang || e.OrphanTries != 1 || e.RingError == "" {
		t.Fatalf("after a failed ring: %+v", e)
	}
	k, url := newHook(t)
	l.h.run(url, "notify", "planner", "--url-file", "-")
	l.pickup()
	if n := k.wait(t, 5*time.Second); n.Run != r.ID || n.State != runs.Failed {
		t.Fatalf("ring: %+v", n)
	}
	l.pickup()
	l.h.ok(nil, "doctor")
	if k.count() != 1 {
		t.Fatalf("rang %d times; want 1", k.count())
	}
	l.ackAll()
}
