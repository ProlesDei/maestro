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

func detach(*exec.Cmd) {}
