package envcheck

import (
	"errors"
	"os"
	"syscall"
)

func platformStatfs(dir string) (int64, bool, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false, err
	}
	return int64(st.Type) & 0xffffffff, true, nil
}

// fOFDSetlk is F_OFD_SETLK: a lock owned by the open file description, so
// two opens in one process conflict as two processes would.
const fOFDSetlk = 37

func platformProbeLocks(dir string) (bool, error) {
	f, err := os.CreateTemp(dir, ".maestro-lock-probe-*")
	if err != nil {
		return false, err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	g, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer g.Close()
	lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 1}
	if err := syscall.FcntlFlock(f.Fd(), fOFDSetlk, &lk); err != nil {
		if errors.Is(err, syscall.EINVAL) {
			return false, nil // kernel without OFD locks: can't tell
		}
		return true, err
	}
	err = syscall.FcntlFlock(g.Fd(), fOFDSetlk, &lk)
	switch {
	case err == nil:
		return true, errors.New("a second write lock on the same byte was granted")
	case errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EACCES):
		return true, nil
	default:
		return true, err
	}
}
