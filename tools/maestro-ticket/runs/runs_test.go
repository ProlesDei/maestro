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

func TestCreateAndPickOnce(t *testing.T) {
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
	if ok, err := Pick(dir, r.ID, "planner", now); !ok || err != nil {
		t.Fatalf("first pick: %v %v", ok, err)
	}
	if ok, _ := Pick(dir, r.ID, "planner", now); ok {
		t.Fatal("second pick succeeded")
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

// Prune leaves .picked until last, so a pickup running alongside never
// takes a pruned run again.
func TestPruneKeepsUnpicked(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old, _ := Create(dir, Record{Launcher: "planner", Limit: "2h", Command: []string{"true"}, StartedAt: now}, nil)
	Pick(dir, old.ID, "planner", now)
	waiting, _ := Create(dir, Record{Launcher: "gone-bot", Limit: "2h", Command: []string{"true"}, StartedAt: now}, nil)
	Prune(dir, time.Hour, now.Add(30*24*time.Hour))
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), old.ID) {
			t.Fatalf("picked run left %s", e.Name())
		}
	}
	if _, err := Load(dir, waiting.ID); err != nil {
		t.Fatalf("unpicked run pruned: %v", err)
	}
}
