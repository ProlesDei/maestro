package runs

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestParseOutput(t *testing.T) {
	for _, c := range []struct {
		out, text, session string
		isErr              bool
	}{
		{`{"type":"result","result":"done: T-1","session_id":"s1","is_error":false}`, "done: T-1", "s1", false},
		{"{\"type\":\"system\"}\n{\"type\":\"result\",\"result\":\"limit hit\",\"session_id\":\"s2\",\"is_error\":true}\n", "limit hit", "s2", true},
		{"{\n  \"result\": \"two\\nlines\",\n  \"session_id\": \"s3\"\n}\n", "two\nlines", "s3", false},
		{"plain text\n", "plain text", "", false},
		{"", "", "", false},
	} {
		text, session, isErr := ParseOutput([]byte(c.out))
		if text != c.text || session != c.session || isErr != c.isErr {
			t.Errorf("ParseOutput(%q) = %q %q %v", c.out, text, session, isErr)
		}
	}
}

// A command launched on stream-json itself is never swapped, and its raw
// stream is its .out: one that ends with no result keeps the session of
// its last line and gives no reply, as before stats.
func TestParseOutputRawStream(t *testing.T) {
	out := "{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"s1\",\"cwd\":\"/x\"}\n" +
		"{\"type\":\"assistant\",\"session_id\":\"s1\"}\n" +
		"{\"type\":\"user\",\"session_id\":\"s1\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"content\":\"SECRET\"}]}}\n"
	if text, session, isErr := ParseOutput([]byte(out)); text != "" || session != "s1" || isErr {
		t.Errorf("raw stream: %q %q %v; want no reply and session s1", text, session, isErr)
	}
	// A result with no result text counts, wherever type sits in it.
	text, session, isErr := ParseOutput([]byte(`{"subtype":"error_max_turns","session_id":"s2","is_error":true,"type":"result"}`))
	if text != "" || session != "s2" || !isErr {
		t.Errorf("max turns result: %q %q %v", text, session, isErr)
	}
}

func TestTailKeepsWholeRunes(t *testing.T) {
	s := tail(strings.Repeat("é", 10), 5)
	if !strings.HasPrefix(s, "…") || strings.ContainsRune(s, utf8.RuneError) || len(s) > 5+len("…") {
		t.Fatalf("tail: %q", s)
	}
}

func TestHeadlessClaude(t *testing.T) {
	for argv, want := range map[string]bool{
		"/home/u/.local/bin/claude -p hi": true,
		"claude --print":                  true,
		"node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js -p": true,
		"node /usr/bin/claude -p": true,
		"claude":                  false,
		"claude --resume x":       false,
		"/usr/bin/sleep -p":       false,
		"sh -c claude -p":         false,
	} {
		if got := headlessClaude(strings.Fields(argv)); got != want {
			t.Errorf("%q: %v; want %v", argv, got, want)
		}
	}
}

func TestUnder(t *testing.T) {
	roots := []string{"/r/main", "/w/t3-1"}
	for dir, want := range map[string]bool{"/r/main": true, "/r/main/sub": true, "/r/mainx": false, "/w/t3-1/a/b": true, "/w": false} {
		if under(dir, roots) != want {
			t.Errorf("under(%q) = %v", dir, !want)
		}
	}
}

func TestCreateAndAckOnce(t *testing.T) {
	dir := t.TempDir()
	r, err := Create(dir, Record{Launcher: "planner", Limit: "2h", Command: []string{"true"}, StartedAt: time.Now()}, []byte("in"))
	if err != nil || !ValidID(r.ID) || r.State != Starting {
		t.Fatalf("create: %+v %v", r, err)
	}
	// No supervisor yet, but inside the grace: still starting.
	if got, _ := Get(dir, r.ID, time.Now()); got.State != Starting {
		t.Fatalf("fresh record: %s", got.State)
	}
	if got, _ := Get(dir, r.ID, time.Now().Add(startGrace+time.Minute)); got.State != Died {
		t.Fatalf("record with no supervisor past the grace: %s", got.State)
	}
	now := time.Now()
	if ok, err := Ack(dir, r.ID, "planner", now); !ok || err != nil {
		t.Fatalf("first ack: %v %v", ok, err)
	}
	if ok, _ := Ack(dir, r.ID, "planner", now); ok {
		t.Fatal("second ack succeeded")
	}
	if err := Prune(dir, time.Hour, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if list, _ := List(dir, now, true); len(list) != 0 {
		t.Fatalf("after prune: %+v", list)
	}
}

func TestUnreadableRecordShows(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "R-aaaaaaaa.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := List(dir, time.Now(), false)
	if err != nil || len(list) != 1 || list[0].State != Unreadable || !list[0].Ended() {
		t.Fatalf("list: %+v %v", list, err)
	}
}

// Prune removes only acked runs; an unacked one stays however old.
func TestPruneKeepsUnpicked(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old, _ := Create(dir, Record{Launcher: "planner", Limit: "2h", Command: []string{"true"}, StartedAt: now}, nil)
	Ack(dir, old.ID, "planner", now)
	waiting, _ := Create(dir, Record{Launcher: "gone-bot", Limit: "2h", Command: []string{"true"}, StartedAt: now}, nil)
	Prune(dir, time.Hour, now.Add(30*24*time.Hour))
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), old.ID) {
			t.Fatalf("acked run left %s", e.Name())
		}
	}
	if _, err := Load(dir, waiting.ID); err != nil {
		t.Fatalf("unacked run pruned: %v", err)
	}
}

// Adopt lets one caller at a time in, and only once the supervisor is
// gone: it writes a live record as died, and holds off a late supervisor
// and every other reader until it lets go. A run on another machine,
// whose supervisor it can't test, never reads as died.
func TestAdoptOnce(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	r, _ := Create(dir, Record{Launcher: "planner", Limit: "2h", Command: []string{"true"}, StartedAt: now}, nil)
	if _, _, _, ok := Adopt(dir, r.ID, now); ok {
		t.Fatal("adopted a run still inside its start grace")
	}
	later := now.Add(startGrace + time.Minute)
	if ids, _ := Orphans(dir, later); len(ids) != 1 || ids[0] != r.ID {
		t.Fatalf("orphans: %v", ids)
	}
	got, release, _, ok := Adopt(dir, r.ID, later)
	if !ok || got.State != Died || got.EndedAt == nil {
		t.Fatalf("adopt: %+v %v", got, ok)
	}
	if _, _, _, ok := Adopt(dir, r.ID, later); ok {
		t.Fatal("two callers adopted one run")
	}
	if g, _ := Get(dir, r.ID, later); g.State == Died {
		t.Fatal("a reader saw died while the run was adopted")
	}
	got.Rang = true
	Save(dir, got)
	release()
	if _, _, _, ok := Adopt(dir, r.ID, later); ok {
		t.Fatal("adopted a run that already rang")
	}
	if ids, _ := Orphans(dir, later); len(ids) != 0 {
		t.Fatalf("orphans after the ring: %v", ids)
	}
}

// A record under another host name is this machine's under an old name,
// unless the store may sit on a shared disk: its free lock means its
// supervisor is gone, at once.
func TestRenamedHostReadsByItsLock(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	later := now.Add(time.Minute)
	old, _ := Create(dir, Record{Launcher: "planner", Limit: "2h", Command: []string{"true"}, StartedAt: now}, nil)
	old.Host, old.State = "old-name.local", Running
	Save(dir, old)
	if g, _ := Get(dir, old.ID, later); g.State != Died {
		t.Fatalf("a dead run under an old host name read as %s", g.State)
	}
	got, release, _, ok := Adopt(dir, old.ID, later)
	if !ok || got.State != Died || got.Host != hostname() {
		t.Fatalf("adopt: %+v %v", got, ok)
	}
	// A ring that failed is tried again, as any run's.
	got.OrphanTries = 1
	Save(dir, got)
	release()
	if _, release, _, ok := Adopt(dir, old.ID, later); !ok {
		t.Fatal("a failed ring isn't tried again")
	} else {
		release()
	}
}

// On a store that may sit on a shared disk, a run on another machine is
// alive, its lock out of reach, until no supervisor would still run it.
func TestRunElsewhereOnASharedStore(t *testing.T) {
	sharedStore = func() bool { return true }
	t.Cleanup(func() { sharedStore = func() bool { return false } })
	dir := t.TempDir()
	now := time.Now()
	later := now.Add(time.Minute)
	far, _ := Create(dir, Record{Launcher: "planner", Limit: "2h", Command: []string{"true"}, StartedAt: now}, nil)
	far.Host = "another-machine"
	far.State = Running
	Save(dir, far)
	if g, _ := Get(dir, far.ID, later); g.State != Running {
		t.Fatalf("a run on another machine read as %s", g.State)
	}
	if _, _, _, ok := Adopt(dir, far.ID, later); ok {
		t.Fatal("adopted a run on another machine")
	}
	gone := now.Add(2*time.Hour + maxSlotCredit + KillGrace + 2*time.Hour)
	// A lock held here is a supervisor here, alive, whatever the clock
	// says: a laptop that slept past the deadline.
	lf, err := os.OpenFile(path(dir, far.ID, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := lockFile(lf); err != nil {
		t.Fatal(err)
	}
	if g, _ := Get(dir, far.ID, gone); g.State != Running {
		t.Fatalf("a run whose supervisor holds its lock here read as %s", g.State)
	}
	lf.Close()
	if g, _ := Get(dir, far.ID, gone); g.State != Died {
		t.Fatalf("an overdue run on another machine read as %s", g.State)
	}
	if _, release, _, ok := Adopt(dir, far.ID, gone); !ok {
		t.Fatal("didn't adopt an overdue run on another machine")
	} else {
		release()
	}
}

// A run's record is what runs --json, pickup --json and job --json print,
// and what every reader of runs/ parses: its fields and their JSON names
// stay as they are. A field a reader never sees is tagged json:"-".
func TestRecordJSONKeys(t *testing.T) {
	want := []string{
		"ID id", "Launcher launcher", "Ticket ticket,omitempty", "Limit limit", "Command command",
		"Job job,omitempty", "Tickets tickets,omitempty", "ResumeOf resume_of,omitempty",
		"AfterDeath after_death,omitempty", "AgainOf again_of,omitempty", "Overrides overrides,omitempty", "Nudge nudge,omitempty", "Dir dir", "StartedAt started_at",
		"State state", "PID pid,omitempty", "EndedAt ended_at,omitempty", "Exit exit,omitempty",
		"SessionID session_id,omitempty", "IsError is_error,omitempty", "Reply reply,omitempty",
		"Error error,omitempty", "Frozen frozen,omitempty", "Rings rings,omitempty", "RingError ring_error,omitempty",
		"Rang rang,omitempty", "OrphanTries orphan_tries,omitempty", "LaunchID launch_id,omitempty",
		"Host host,omitempty", "Stamp stamp,omitempty", "ChainOf chain_of,omitempty", "Hops hops,omitempty",
		"Chained chained,omitempty", "Routed routed,omitempty", "Notify notify,omitempty",
		"Settling settling,omitempty",
		"AckedAt acked_at,omitempty", "AckedBy acked_by,omitempty",
	}
	var got []string
	rt := reflect.TypeFor[Record]()
	for i := range rt.NumField() {
		f := rt.Field(i)
		if tag := f.Tag.Get("json"); tag != "-" {
			got = append(got, f.Name+" "+tag)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Record's JSON fields changed:\n got %q\nwant %q", got, want)
	}
}
