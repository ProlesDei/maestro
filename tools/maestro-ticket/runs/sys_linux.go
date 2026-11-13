package runs

import "syscall"

// procAttr starts the command in its own process group, and has the
// kernel send it SIGTERM if the supervisor dies first, so no run outlives
// its time limit.
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
