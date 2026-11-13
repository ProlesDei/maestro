package runs

import (
	"os"
	"path/filepath"
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
	if got, _ := Get(dir, r.ID, time.Now().Add(time.Minute)); got.State != Died {
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
	if _, _, ok := Adopt(dir, r.ID, now); ok {
		t.Fatal("adopted a run still inside its start grace")
	}
	later := now.Add(time.Minute)
	if ids, _ := Orphans(dir, later); len(ids) != 1 || ids[0] != r.ID {
		t.Fatalf("orphans: %v", ids)
	}
	got, release, ok := Adopt(dir, r.ID, later)
	if !ok || got.State != Died || got.EndedAt == nil {
		t.Fatalf("adopt: %+v %v", got, ok)
	}
	if _, _, ok := Adopt(dir, r.ID, later); ok {
		t.Fatal("two callers adopted one run")
	}
	if g, _ := Get(dir, r.ID, later); g.State == Died {
		t.Fatal("a reader saw died while the run was adopted")
	}
	got.Rang = true
	Save(dir, got)
	release()
	if _, _, ok := Adopt(dir, r.ID, later); ok {
		t.Fatal("adopted a run that already rang")
	}
	if ids, _ := Orphans(dir, later); len(ids) != 0 {
		t.Fatalf("orphans after the ring: %v", ids)
	}

	far, _ := Create(dir, Record{Launcher: "planner", Limit: "2h", Command: []string{"true"}, StartedAt: now}, nil)
	far.Host = "another-machine"
	far.State = Running
	Save(dir, far)
	if g, _ := Get(dir, far.ID, later); g.State != Running {
		t.Fatalf("a run on another machine read as %s", g.State)
	}
	if _, _, ok := Adopt(dir, far.ID, later); ok {
		t.Fatal("adopted a run on another machine")
	}
}
