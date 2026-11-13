//go:build !linux

package runs

import "errors"

// CanScan reports whether Untracked works on this system.
const CanScan = false

// KillRun needs /proc to find a run's processes; here a supervisor's
// death leaves them be.
func KillRun(string) {}

// Untracked needs /proc to read other processes' working directory and
// environment.
func Untracked([]string) ([]Session, error) {
	return nil, errors.New("finding untracked sessions needs Linux")
}
