package sqlitestore

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// Backups taken while writers run are consistent snapshots: every ticket
// in one has its created event and every event its outbox row.
func TestBackupDuringWrites(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "tickets.db")
	stores := openMany(t, path, 5)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := stores[w].Create(ctx, ticket.CreateRequest{Title: fmt.Sprint(w, "-", i)}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	var snaps []string
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, "backups", fmt.Sprintf("b%d.db", i))
		if err := stores[4].Backup(ctx, p); err != nil {
			t.Fatal(err)
		}
		snaps = append(snaps, p)
	}
	wg.Wait()
	if err := stores[4].Backup(ctx, snaps[0]); err == nil {
		t.Fatal("backup overwrote an existing file")
	}
	for _, p := range snaps {
		b, err := Open(ctx, p, Options{})
		if err != nil {
			t.Fatal(err)
		}
		ex, err := b.Export(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var outbox int
		b.r.QueryRow("SELECT COUNT(*) FROM outbox").Scan(&outbox)
		if len(ex.Events) != len(ex.Tickets) || outbox != len(ex.Events) {
			t.Errorf("%s: %d tickets, %d events, %d outbox rows", p, len(ex.Tickets), len(ex.Events), outbox)
		}
		b.Close()
	}
}
