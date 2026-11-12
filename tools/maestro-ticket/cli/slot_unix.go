//go:build unix

package cli

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes f's exclusive lock without waiting. A lock held elsewhere
// is ok == false with no error.
func tryLock(f *os.File) (ok bool, err error) {
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
