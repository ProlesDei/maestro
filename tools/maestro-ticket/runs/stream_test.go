//go:build unix

package runs

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/streamfilter"
)

const streamResult = `{"duration_api_ms":1,"session_id":"9e826f97-f358-494b-ba61-a9b4f1b68347","total_cost_usd":0.01,"result":"done: T-1","is_error":false,"type":"result"}`

// streamScript is a command that saves its argv to argsFile(cmd), then
// prints out (a file) as its stdout, then runs tail.
func streamScript(t *testing.T, out, tail string) []string {
	t.Helper()
	d := t.TempDir()
	scriptDirs[d] = true
	if err := os.WriteFile(filepath.Join(d, "out"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `printf '%s\n' "$@" >` + filepath.Join(d, "args") + `; cat ` + filepath.Join(d, "out") + `; ` + tail
	return []string{"sh", "-c", script, "sh", "-p", "--output-format", "json", "--model", "m"}
}

var scriptDirs = map[string]bool{}

func readArgs(t *testing.T, cmd []string) []string {
	t.Helper()
	var d string
	for k := range scriptDirs {
		if strings.Contains(cmd[2], k) {
			d = k
		}
	}
	b, err := os.ReadFile(filepath.Join(d, "args"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

var names = &streamfilter.Names{Sub: []string{"look"}}

// Only the exact --output-format json pair changes, and only in what
// runs: the record keeps its command.
func TestStreamArgvSwap(t *testing.T) {
	for in, want := range map[string]string{
		"claude -p --output-format json --model m": "claude -p --output-format stream-json --verbose --model m",
		"claude -p --model m --output-format json": "claude -p --model m --output-format stream-json --verbose",
	} {
		if got, ok := StreamArgv(strings.Fields(in)); !ok || strings.Join(got, " ") != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"claude -p --output-format stream-json --verbose", "claude -p --output-format=json", "claude -p json --output-format", "true"} {
		if got, ok := StreamArgv(strings.Fields(in)); ok {
			t.Errorf("%q swapped to %q", in, got)
		}
	}
	cmd := streamScript(t, streamResult+"\n", "")
	orig := slices.Clone(cmd)
	dir := t.TempDir()
	got := superviseEnd(t, dir, Record{Limit: "10s", Command: cmd}, Options{Stream: names})
	if args := readArgs(t, cmd); !slices.Equal(args, []string{"-p", "--output-format", "stream-json", "--verbose", "--model", "m"}) {
		t.Fatalf("argv %q", args)
	}
	saved, _ := Load(dir, got.ID)
	if !slices.Equal(got.Command, orig) || !slices.Equal(saved.Command, orig) {
		t.Fatalf("record command %q / %q", got.Command, saved.Command)
	}
	if got.Stream == nil || !got.Stream.Result || got.Reply != "done: T-1" || got.SessionID == "" {
		t.Fatalf("streamed run: %+v %+v", got, got.Stream)
	}
	// With no Stream option, nothing changes.
	got = superviseEnd(t, dir, Record{Limit: "10s", Command: cmd}, Options{})
	if args := readArgs(t, cmd); !slices.Equal(args, orig[4:]) || got.Stream != nil {
		t.Fatalf("argv %q, stream %+v", args, got.Stream)
	}
}

// .out holds what json would have: text before the first JSON line,
// then the result line; no init line, so no session from one.
func TestStreamOutFile(t *testing.T) {
	dir := t.TempDir()
	init := `{"type":"system","subtype":"init","cwd":"/secret","session_id":"11111111-2222-4333-8444-555555555555","claude_code_version":"2.1.322"}`
	got := superviseEnd(t, dir, Record{Limit: "10s", Command: streamScript(t, "warn: x\n"+init+"\n"+streamResult+"\n", "")}, Options{Stream: names})
	b, _ := os.ReadFile(path(dir, got.ID, ".out"))
	if want := "warn: x\n" + streamResult + "\n"; string(b) != want {
		t.Fatalf(".out %q", b)
	}
	// A stopped run whose stream had only the init line has no session.
	got = superviseEnd(t, dir, Record{Limit: "10s", Command: streamScript(t, init+"\n", "exit 1")}, Options{Stream: names})
	if b, _ := os.ReadFile(path(dir, got.ID, ".out")); len(b) != 0 || got.SessionID != "" || got.State != Failed || got.Stream.Result {
		t.Fatalf(".out %q, record %+v", b, got)
	}
}

// The stream file is mode 0600 while the run goes, and gone once its end
// is recorded; Prune takes one a dead supervisor left.
func TestStreamFileRemoved(t *testing.T) {
	dir := t.TempDir()
	var mode os.FileMode
	cmd := streamScript(t, streamResult+"\n", "")
	r, err := Create(dir, Record{Launcher: "planner", Limit: "10s", Command: cmd, Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Stream: names, OnEnd: func(r Record) {
		if fi, err := os.Stat(path(dir, r.ID, streamExt)); err == nil {
			mode = fi.Mode().Perm()
		}
	}}
	if err := Supervise(context.Background(), dir, r.ID, o); err != nil {
		t.Fatal(err)
	}
	if mode != 0o600 {
		t.Fatalf("stream file mode %v during OnEnd", mode)
	}
	if _, err := os.Lstat(path(dir, r.ID, streamExt)); err == nil {
		t.Fatal("stream file left after the end")
	}
	// A command that never starts leaves none.
	r, _ = Create(dir, Record{Launcher: "planner", Limit: "10s", Command: []string{"/nonexistent", "--output-format", "json"}, Dir: dir}, nil)
	Supervise(context.Background(), dir, r.ID, Options{Stream: names})
	if _, err := os.Lstat(path(dir, r.ID, streamExt)); err == nil {
		t.Fatal("stream file left by a start that failed")
	}
	// Prune.
	now := time.Now()
	r, _ = Create(dir, Record{Launcher: "planner", Limit: "2h", Command: []string{"true"}, StartedAt: now}, nil)
	os.WriteFile(path(dir, r.ID, streamExt), []byte("{}\n"), 0o600)
	Ack(dir, r.ID, "planner", now)
	Prune(dir, time.Hour, now.Add(30*24*time.Hour))
	if _, err := os.Lstat(path(dir, r.ID, streamExt)); err == nil {
		t.Fatal("prune left the stream file")
	}
}
