package envcheck

import (
	"fmt"
	"strconv"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// MinFreeEnv names the variable that sets the free-disk margin, a whole
// percent from 0 to 99: claim and slot refuse to start while a filesystem
// they use has less than that share of its inodes or its space free. 0
// turns the check off.
const MinFreeEnv = "MAESTRO_TICKET_MIN_FREE"

// DefaultMinFree is the margin when MinFreeEnv is unset.
const DefaultMinFree = 10

// Free is the share of a filesystem's inodes and space still free, in
// percent. Inodes is -1 on a filesystem with no fixed inode count, such as
// btrfs.
type Free struct{ Inodes, Space float64 }

// DiskFree reads the free shares of dir's filesystem. ok is false where
// the platform can't tell.
func DiskFree(dir string) (f Free, ok bool, err error) { return platformDiskFree(dir) }

// ParseMinFree reads MinFreeEnv's value; empty means DefaultMinFree.
func ParseMinFree(s string) (int, error) {
	if s == "" {
		return DefaultMinFree, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 99 {
		return 0, fmt.Errorf("%w: %s=%q: want a whole percent from 0 to 99", ticket.ErrInvalid, MinFreeEnv, s)
	}
	return n, nil
}

// Headroom returns an ErrLowDisk error naming the first of dirs whose
// filesystem has less than minPct of its inodes or space free. A dir it
// can't measure is skipped: a missing or unreadable one fails later with
// its own error, and a run is never stopped for want of a number.
func Headroom(dirs []string, minPct int, free func(string) (Free, bool, error)) error {
	if minPct <= 0 {
		return nil
	}
	for _, d := range dirs {
		f, ok, err := free(d)
		if err != nil || !ok {
			continue
		}
		switch m := float64(minPct); {
		case f.Inodes >= 0 && f.Inodes < m:
			return fmt.Errorf("%w: %s has %.1f%% of its inodes free, under the %d%% margin", ticket.ErrLowDisk, d, f.Inodes, minPct)
		case f.Space < m:
			return fmt.Errorf("%w: %s has %.1f%% of its space free, under the %d%% margin", ticket.ErrLowDisk, d, f.Space, minPct)
		}
	}
	return nil
}
