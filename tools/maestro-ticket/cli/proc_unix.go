//go:build unix

package cli

import (
	"os/exec"
	"syscall"
)

// alive reports whether process pid exists and is ours to signal. A
// session's process always is; EPERM means pid is another user's process,
// such as a recycled pid, and PID 1 never exits.
func alive(pid int) bool {
	return pid > 1 && syscall.Kill(pid, 0) == nil
}

// detach makes cmd outlive the caller: its own session, no terminal.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
