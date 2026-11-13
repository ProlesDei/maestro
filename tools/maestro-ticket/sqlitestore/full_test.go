package sqlitestore

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

type codeErr int

func (e codeErr) Error() string { return fmt.Sprintf("sqlite code %d", int(e)) }
func (e codeErr) Code() int     { return int(e) }

// A write that fails for want of disk says low_disk, so a session can tell
// a full disk from a bug without reading SQLite's text.
func TestFullDiskIsLowDisk(t *testing.T) {
	for _, tc := range []struct {
		err  error
		full bool
	}{
		{codeErr(13), true},        // SQLITE_FULL: "database or disk is full"
		{codeErr(13 | 3<<8), true}, // an extended code of it
		{fmt.Errorf("open: %w", syscall.ENOSPC), true},
		{codeErr(10), false}, // SQLITE_IOERR
		{errors.New("boom"), false},
	} {
		err := retryBusy(context.Background(), func() error { return tc.err })
		if got := errors.Is(err, ticket.ErrLowDisk); got != tc.full || !errors.Is(err, tc.err) {
			t.Errorf("%v: low disk %v; want %v, still wrapping it", tc.err, got, tc.full)
		}
	}
}
