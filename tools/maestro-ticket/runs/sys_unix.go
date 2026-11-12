//go:build unix

package runs

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Supported reports whether launch works on this system.
const Supported = true

// lockFile takes f's exclusive lock, waiting for it.
func lockFile(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

// tryLock takes and drops f's exclusive lock without waiting. A lock held
// elsewhere is ok == false with no error.
func tryLock(f *os.File) (ok bool, err error) {
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, unix.Flock(int(f.Fd()), unix.LOCK_UN)
}

// killGroup sends sig to every process in the group pid leads.
func killGroup(pid int, sig syscall.Signal) { syscall.Kill(-pid, sig) }

// Detach makes a process outlive the command that started it: its own
// session, no terminal.
func Detach() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
