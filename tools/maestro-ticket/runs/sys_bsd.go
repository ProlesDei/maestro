//go:build unix && !linux

package runs

import "syscall"

// procAttr starts the command in its own process group.
func procAttr(bool) *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
