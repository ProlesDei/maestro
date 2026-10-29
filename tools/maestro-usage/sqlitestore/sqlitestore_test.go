package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-usage/usage"
)

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func open(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func rec(key string, c usage.Context, usd *float64, tokens int64, status string) usage.Record {
	return usage.Record{Key: key, RecordedAt: now, Harness: "codex", Context: c,
		Usage: usage.Usage{InputTokens: tokens, CostUSD: usd, Status: status}}
}

func ptr(f float64) *float64 { return &f }

func TestSaveSameKeyReplaces(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "usage.db"))
	for _, tokens := range []int64{100, 250} {
		if err := s.Save(ctx, rec("k", usage.Context{Ticket: "T-1"}, ptr(1), tokens, "success")); err != nil {
			t.Fatal(err)
		}
	}
	tot, err := s.Spent(ctx, usage.Filter{Field: usage.FieldTicket, Value: "T-1"})
	if err != nil || tot.Tokens != 250 || tot.USD != 1 {
		t.Fatalf("spent = %+v, %v; want one row of 250 tokens", tot, err)
	}
	// The whole row is replaced, context included.
	if err := s.Save(ctx, rec("k", usage.Context{Ticket: "T-2"}, ptr(1), 50, "success")); err != nil {
		t.Fatal(err)
	}
	for ticket, want := range map[string]int64{"T-1": 0, "T-2": 50} {
		tot, err := s.Spent(ctx, usage.Filter{Field: usage.FieldTicket, Value: ticket})
		if err != nil || tot.Tokens != want {
			t.Fatalf("%s spent = %+v, %v; want %d tokens", ticket, tot, err, want)
		}
	}
	if err := s.Save(ctx, usage.Record{Usage: usage.Usage{Status: "success"}}); err == nil {
		t.Fatal("saved a record with no key")
	}
}

func TestSpentFilters(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "usage.db"))
	old := rec("old", usage.Context{Role: "qa"}, ptr(5), 10, "success")
	old.RecordedAt = now.Add(-48 * time.Hour)
	for _, r := range []usage.Record{old,
		rec("a", usage.Context{Role: "qa"}, ptr(1), 20, "success"),
		rec("b", usage.Context{Role: "dev"}, nil, 40, "error")} {
		if err := s.Save(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		f      usage.Filter
		usd    float64
		tokens int64
	}{
		{usage.Filter{}, 6, 70},
		{usage.Filter{Since: now.Add(-time.Hour)}, 1, 60},
		{usage.Filter{Field: usage.FieldRole, Value: "qa"}, 6, 30},
		{usage.Filter{Field: usage.FieldRole, Value: "dev"}, 0, 40},
		{usage.Filter{Field: usage.FieldRole, Value: "nobody"}, 0, 0},
	}
	for _, tc := range tests {
		got, err := s.Spent(ctx, tc.f)
		if err != nil || got.USD != tc.usd || got.Tokens != tc.tokens {
			t.Errorf("Spent(%+v) = %+v, %v; want %v, %d", tc.f, got, err, tc.usd, tc.tokens)
		}
	}
	if _, err := s.Spent(ctx, usage.Filter{Field: "role; DROP TABLE usage", Value: "x"}); err == nil {
		t.Error("accepted an unknown field")
	}
}

func TestReport(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "usage.db"))
	for _, r := range []usage.Record{
		rec("1", usage.Context{Workstream: "auth"}, ptr(2), 10, "success"),
		rec("2", usage.Context{Workstream: "auth"}, nil, 5, "unparsed"),
		rec("3", usage.Context{Workstream: "billing"}, ptr(3), 1, "success"),
		rec("4", usage.Context{}, ptr(0.5), 1, "error"),
	} {
		if err := s.Save(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.Report(ctx, usage.FieldWorkstream, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := []usage.ReportRow{
		{Group: "billing", Sessions: 1, USD: 3, Input: 1},
		{Group: "auth", Sessions: 2, USD: 2, Input: 15, Failures: 1, Unpriced: 1},
		{Group: "(none)", Sessions: 1, USD: 0.5, Input: 1, Failures: 1},
	}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Fatalf("report =\n %+v\nwant\n %+v", rows, want)
	}
	if rows, _ := s.Report(ctx, usage.FieldWorkstream, now.Add(time.Hour)); len(rows) != 0 {
		t.Fatalf("future since returned %v", rows)
	}
}

// Separate Store values stand in for separate processes: each has its own
// connections and must wait for the write lock.
func TestConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage.db")
	const writers, each = 16, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(ctx, path)
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			for i := range each {
				r := rec(fmt.Sprintf("w%d-%d", w, i), usage.Context{Role: "r"}, ptr(0.01), 1, "success")
				if err := s.Save(ctx, r); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	s := open(t, path)
	tot, err := s.Spent(ctx, usage.Filter{})
	if err != nil || tot.Tokens != writers*each {
		t.Fatalf("spent = %+v, %v; want %d rows", tot, err, writers*each)
	}
	sts, err := s.RecentStatuses(ctx, "r", 1000)
	if err != nil || len(sts) != writers*each {
		t.Fatalf("statuses = %d, %v", len(sts), err)
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	open(t, path).Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(context.Background(), path); err == nil {
		t.Fatal("opened a newer schema")
	}
}

func TestOpenSettings(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "usage.db"))
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	var busy int
	if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy != busyTimeoutMS {
		t.Fatalf("busy_timeout = %d, %v", busy, err)
	}
}
