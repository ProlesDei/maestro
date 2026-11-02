//go:build unix

package cli

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// alive reports whether process pid runs and is ours to signal. A
// session's process always is; EPERM means pid is another user's process,
// such as a recycled pid, and PID 1 never exits. A zombie has exited:
// kill(pid, 0) finds it until its parent reaps it, which may be never.
func alive(pid int) bool {
	return pid > 1 && syscall.Kill(pid, 0) == nil && !zombie(pid)
}

// endsWithCommand reports whether pid shares this process's session and
// that session has no terminal. Claude Code, Codex and Grok Build run each
// command in a new session with no terminal, which ends with the command;
// the harness runs in another. A terminal's session is a person's and lasts.
func endsWithCommand(pid int) bool {
	sid, err := unix.Getsid(pid)
	if err != nil {
		return false
	}
	if own, err := unix.Getsid(0); err != nil || sid != own {
		return false
	}
	tty, err := os.Open("/dev/tty") // fails with no controlling terminal
	if err == nil {
		tty.Close()
	}
	return err != nil
}

// detach makes cmd outlive the caller: its own session, no terminal.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
