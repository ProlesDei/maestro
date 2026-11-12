//go:build !linux

package runs

import "errors"

// CanScan reports whether Untracked works on this system.
const CanScan = false

// Untracked needs /proc to read other processes' working directory and
// environment.
func Untracked([]string) ([]Session, error) {
	return nil, errors.New("finding untracked sessions needs Linux")
}
