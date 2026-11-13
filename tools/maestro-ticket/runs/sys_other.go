//go:build !unix

package runs

import (
	"errors"
	"os"
	"syscall"
)

// Supported reports whether launch works on this system.
const Supported = false

var errUnix = errors.New("launch needs a unix system")

func lockFile(*os.File) error         { return errUnix }
func tryLock(*os.File) (bool, error)  { return false, errUnix }
func holdLock(*os.File) (bool, error) { return false, errUnix }
func killGroup(int, syscall.Signal)   {}
func procAttr() *syscall.SysProcAttr  { return nil }
func Detach() *syscall.SysProcAttr    { return nil }
