package sqlitestore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/storetest"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// openMany opens n independent stores on one file. Each has its own
// connections, so they contend through SQLite's file locks exactly as
// separate processes would.
func openMany(t *testing.T, path string, n int) []*Store {
	t.Helper()
	stores := make([]*Store, n)
	for i := range stores {
		s, err := Open(context.Background(), path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = s
		t.Cleanup(func() { s.Close() })
	}
	return stores
}

// Many processes opening a new store at once all migrate it
// once, to the current version.
func TestRaceFirstOpenMigrates(t *testing.T) {
	const n = 30
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fresh", "tickets.db")
	stores := make([]*Store, n)
	errs := make([]error, n)
	storetest.RunAll(n, func(i int) { stores[i], errs[i] = Open(ctx, path, Options{}) })
	for i, err := range errs {
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		defer stores[i].Close()
	}
	var v int
	stores[0].w.QueryRow("PRAGMA user_version").Scan(&v)
	rep, err := stores[0].Doctor(ctx)
	if v != schemaVersion || err != nil || !rep.OK() {
		t.Fatalf("user_version %d, doctor %+v, %v", v, rep, err)
	}
	if _, err := stores[n-1].Create(ctx, ticket.CreateRequest{Title: "works"}); err != nil {
		t.Fatal(err)
	}
}
