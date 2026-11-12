//go:build !unix

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func ownGroup(*exec.Cmd) {}

func signalGroup(int, syscall.Signal) error { return nil }

func tryLock(*os.File) (bool, error) {
	return false, fmt.Errorf("%w: slot needs a unix system", ticket.ErrInvalid)
}
