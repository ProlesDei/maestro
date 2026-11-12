//go:build unix

package cli

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// ownGroup starts cmd in a new process group, whose id is its pid.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// signalGroup sends sig to every process in the group pid leads.
func signalGroup(pid int, sig syscall.Signal) error { return syscall.Kill(-pid, sig) }

// tryLock takes f's exclusive lock without waiting. A lock held elsewhere
// is ok == false with no error.
func tryLock(f *os.File) (ok bool, err error) {
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
