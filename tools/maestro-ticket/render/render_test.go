package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func sample() []ticket.Ticket {
	exp := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	return []ticket.Ticket{
		{ID: 1, Title: "Pipe | and\nnewline", Status: ticket.StatusTodo, Role: "engineer", Priority: 1, Version: 1},
		{ID: 2, Title: "Live", Status: ticket.StatusInProgress, Priority: 0, Version: 3,
			Claim: &ticket.Claim{Holder: "sse/s1", ExpiresAt: exp}},
		{ID: 3, Title: "Stale", Status: ticket.StatusInProgress, Priority: 2, Version: 2,
			Claim: &ticket.Claim{Holder: "jse/s9", ExpiresAt: exp, Expired: true}},
		{ID: 4, Title: "Later", Status: ticket.StatusBacklog, Parked: ticket.ParkedHold, Priority: 2, Version: 1},
	}
}

func TestBacklog(t *testing.T) {
	out := string(Backlog(sample(), 42))
	if !strings.HasPrefix(out, Banner+"\n") {
		t.Fatal("missing banner on line 1")
	}
	for _, want := range []string{
		"at event seq 42",
		"| todo | 1 |", "| in_progress | 2 |", "| done | 0 |",
		"| T-1 | P1 | Pipe \\| and newline | engineer |  | 1 |",
		"sse/s1 until 2026-10-28 10:00Z",
		"jse/s9 (expired)",
		"| ID | P | Title | Parked | Role | Claim | Version |",
		"| T-4 | P2 | Later | hold |  |  | 1 |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "## done") {
		t.Error("empty sections should be omitted")
	}
	if strings.Index(out, "## todo") > strings.Index(out, "## in_progress") {
		t.Error("sections out of workflow order")
	}
}

func TestBacklogDeterministic(t *testing.T) {
	if !bytes.Equal(Backlog(sample(), 7), Backlog(sample(), 7)) {
		t.Fatal("same input must render identically")
	}
}

func TestWriteAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".maestro")
	path := filepath.Join(dir, "Backlog.md")
	if err := WriteAtomic(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, []byte("two")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "two" {
		t.Fatalf("content %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

func TestWriteAtomicFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Backlog.md")
	WriteAtomic(path, []byte("old"))
	// A directory where the file should go makes the rename fail.
	bad := filepath.Join(dir, "sub")
	os.MkdirAll(filepath.Join(bad, "Backlog.md"), 0o755)
	if err := WriteAtomic(filepath.Join(bad, "Backlog.md"), []byte("new")); err == nil {
		t.Fatal("expected rename onto a directory to fail")
	}
	entries, _ := os.ReadDir(bad)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind after failure: %v", entries)
	}
	if b, _ := os.ReadFile(path); string(b) != "old" {
		t.Fatal("unrelated file changed")
	}
}
