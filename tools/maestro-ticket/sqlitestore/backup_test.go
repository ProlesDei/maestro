package sqlitestore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// Backups taken while writers run are consistent snapshots: every ticket
// in one has its created event.
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
		if len(ex.Events) != len(ex.Tickets) {
			t.Errorf("%s: %d tickets, %d events", p, len(ex.Tickets), len(ex.Events))
		}
		b.Close()
	}
}

// Two backups to one path: one wins, the other fails before it writes,
// and the winner's file survives.
func TestBackupSamePathRace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	stores := openMany(t, filepath.Join(dir, "tickets.db"), 2)
	if _, err := stores[0].Create(ctx, ticket.CreateRequest{Title: "t"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		p := filepath.Join(dir, "backups", fmt.Sprintf("b%d.db", i))
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for j := range stores {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[j] = stores[j].Backup(ctx, p)
			}()
		}
		wg.Wait()
		if (errs[0] == nil) == (errs[1] == nil) {
			t.Fatalf("trial %d: want one success, got %v and %v", i, errs[0], errs[1])
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("trial %d: the winner's backup is gone: %v", i, err)
		}
		if err := checkBackup(ctx, p); err != nil {
			t.Fatalf("trial %d: %v", i, err)
		}
	}
}
