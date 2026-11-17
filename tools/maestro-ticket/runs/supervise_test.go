//go:build unix

package runs

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// superviseEnd runs a record's command under Supervise and returns the
// record OnEnd got.
func superviseEnd(t *testing.T, dir string, rec Record, o Options) Record {
	t.Helper()
	rec.Launcher, rec.Dir = "planner", dir
	r, err := Create(dir, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got Record
	o.OnEnd = func(r Record) { got = r }
	if err := Supervise(context.Background(), dir, r.ID, o); err != nil {
		t.Fatal(err)
	}
	if got.End == nil {
		t.Fatalf("OnEnd got no end facts: %+v", got)
	}
	return got
}

// The end facts reach OnEnd and nothing else: not the saved record, not
// its JSON.
func TestEndFactsNotInRecordJSON(t *testing.T) {
	dir := t.TempDir()
	got := superviseEnd(t, dir, Record{Limit: "10s", Command: []string{"true"}}, Options{})
	plain := got
	plain.End = nil
	with, _ := json.Marshal(got)
	without, _ := json.Marshal(plain)
	if !bytes.Equal(with, without) {
		t.Fatalf("End shows in the JSON:\n%s\n%s", with, without)
	}
	b, err := os.ReadFile(path(dir, got.ID, ".json"))
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(b, &keys); err != nil {
		t.Fatal(err)
	}
	for k := range keys {
		if k == "End" || strings.Contains(strings.ToLower(k), "wait") || strings.Contains(strings.ToLower(k), "stop") || strings.Contains(strings.ToLower(k), "escaped") {
			t.Fatalf("the saved record holds %s:\n%s", k, b)
		}
	}
	if l, _ := Load(dir, got.ID); l.End != nil {
		t.Fatal("a loaded record has end facts")
	}
}

// Prune takes a run's side file, and a temp copy a killed launch left,
// with the rest of its files.
func TestPruneRemovesSideFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	r, _ := Create(dir, Record{Launcher: "planner", Limit: "2h", Command: []string{"true"}, StartedAt: now}, nil)
	if err := os.WriteFile(path(dir, r.ID, ".stats"), []byte(`{"why":"fixed"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "."+r.ID+".stats.tmp-123")
	if err := os.WriteFile(tmp, []byte(`{"wh`), 0o644); err != nil {
		t.Fatal(err)
	}
	Ack(dir, r.ID, "planner", now)
	Prune(dir, time.Hour, now.Add(30*24*time.Hour))
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.Contains(e.Name(), r.ID) {
			t.Fatalf("pruned run left %s", e.Name())
		}
	}
}
