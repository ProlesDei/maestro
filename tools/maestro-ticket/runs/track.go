package runs

import (
	"context"
	"os"
	"time"
)

// TrackNative holds the supervisor lock while a recovered native worker runs.
// Adopt reads the saved result after this lock releases. A second tracker exits.
func TrackNative(ctx context.Context, dir, id, board string) (bool, error) {
	f, err := os.OpenFile(path(dir, id, ".lock"), os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	ok, err := holdLock(f)
	if !ok || err != nil {
		return false, err
	}
	r, err := Load(dir, id)
	if err != nil || r.BoardID != board || r.Ended() {
		return false, err
	}
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for orphanLives(dir, r, time.Now()) {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-timer.C:
		}
	}
	return true, nil
}
