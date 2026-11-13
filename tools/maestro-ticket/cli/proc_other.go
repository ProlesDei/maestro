//go:build !unix

package cli

import (
	"os"
	"os/exec"
)

func alive(pid int) bool {
	if pid <= 1 {
		return false
	}
	p, err := os.FindProcess(pid)
	return err == nil && p != nil
}

func endsWithCommand(int) bool { return false }

func detach(*exec.Cmd) {}

// procStamp can't tell a pid's process from a later one here.
func procStamp(int) string { return "" }

// zombie can't tell here; alive covers a process that exited.
func zombie(int) bool { return false }
