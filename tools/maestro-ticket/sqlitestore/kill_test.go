package sqlitestore

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// When re-executed with this variable set to "<mode>:<store path>", the test
// binary runs a child for the kill tests instead of the tests.
const killChildEnv = "MAESTRO_SQLITESTORE_KILL_CHILD"

func TestMain(m *testing.M) {
	if v := os.Getenv(killChildEnv); v != "" {
		mode, path, _ := strings.Cut(v, ":")
		os.Exit(killChild(mode, path))
	}
	os.Exit(m.Run())
}

// killChild either stops inside an open write transaction ("mid") or
// commits creates in a loop, printing each id after its commit ("loop").
// The parent kills it with SIGKILL.
func killChild(mode, path string) int {
	ctx := context.Background()
	s, err := Open(ctx, path, Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	switch mode {
	case "mid":
		s.write(ctx, func(tx *sql.Tx, now int64) error {
			res, err := tx.ExecContext(ctx, `INSERT INTO tickets (title, status, priority, created_by, owner, version, created_at, updated_at)
				VALUES ('half-written', 'todo', 2, 'child', 'child', 1, ?, ?)`, now, now)
			if err != nil {
				return err
			}
			id, _ := res.LastInsertId()
			if err := appendEvent(ctx, tx, ticket.ID(id), ticket.EventCreated, "child", map[string]any{}, now); err != nil {
				return err
			}
			fmt.Println("ready")
			time.Sleep(time.Hour)
			return nil
		})
	case "loop":
		for i := 0; ; i++ {
			tk, err := s.Create(ctx, ticket.CreateRequest{Title: fmt.Sprint("loop-", i)})
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Println(int64(tk.ID))
		}
	}
	return 1
}

func startKillChild(t *testing.T, mode, path string) (*exec.Cmd, *bufio.Scanner) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), killChildEnv+"="+mode+":"+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd, bufio.NewScanner(out)
}

func checkIntact(t *testing.T, s *Store) {
	t.Helper()
	rep, err := s.Doctor(context.Background())
	if err != nil || !rep.OK() {
		t.Fatalf("doctor after kill: %+v, %v", rep, err)
	}
}

// A process killed inside a write transaction leaves nothing behind: no
// ticket, no event, no outbox row, and the write lock is free again.
func TestKillMidTransaction(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tickets.db")
	s, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustCreate(t, s, "before", "", 2)

	cmd, out := startKillChild(t, "mid", path)
	if !out.Scan() || out.Text() != "ready" {
		cmd.Process.Kill()
		t.Fatalf("child didn't reach the open transaction: %q", out.Text())
	}
	// The child holds the write lock: our write waits on it.
	cmd.Process.Kill()
	cmd.Wait()

	start := time.Now()
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "after"}); err != nil {
		t.Fatalf("write after kill: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("write lock stuck for %s after the holder died", d)
	}
	all, _ := s.List(ctx, ticket.ListFilter{})
	for _, tk := range all {
		if tk.Title == "half-written" {
			t.Fatal("killed transaction's ticket is visible")
		}
	}
	if evs, _ := s.Events(ctx, ticket.EventFilter{}); len(all) != 2 || len(evs) != 2 {
		t.Fatalf("%d tickets, %d events; want 2 each", len(all), len(evs))
	}
	checkIntact(t, s)
}

// A process killed while committing in a loop loses nothing it reported as
// committed, and leaves a store that passes doctor.
func TestKillDuringCommits(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tickets.db")
	s, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for round := 0; round < 3; round++ {
		cmd, out := startKillChild(t, "loop", path)
		var last int64
		for i := 0; i < 20+round*15 && out.Scan(); i++ {
			last, _ = strconv.ParseInt(out.Text(), 10, 64)
		}
		cmd.Process.Kill()
		cmd.Wait()
		if last == 0 {
			t.Fatal("child committed nothing")
		}
		if _, err := s.Get(ctx, ticket.ID(last)); err != nil {
			t.Fatalf("round %d: committed %s lost: %v", round, ticket.ID(last), err)
		}
		checkIntact(t, s)
	}
	all, _ := s.List(ctx, ticket.ListFilter{})
	evs, _ := s.Events(ctx, ticket.EventFilter{})
	if len(evs) != len(all) {
		t.Fatalf("%d tickets, %d events", len(all), len(evs))
	}
	t.Logf("3 kills, %d tickets committed, store intact", len(all))
}
