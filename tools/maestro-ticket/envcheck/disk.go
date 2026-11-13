package envcheck

import (
	"fmt"
	"strconv"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// MinFreeEnv names the variable that sets the free-disk margin, a whole
// percent from 0 to 99: claim and slot refuse to start while a filesystem
// they use has less than that share of its inodes or its space free, and
// less than the floor below. 0 turns the check off.
const MinFreeEnv = "MAESTRO_TICKET_MIN_FREE"

// DefaultMinFree is the margin when MinFreeEnv is unset.
const DefaultMinFree = 10

// A filesystem under the margin still passes while it has this much free:
// on a big disk, 9% can be hundreds of gigabytes and millions of inodes.
const (
	FloorInodes = 1 << 20 // about 40 pytest runs' worth of files
	FloorBytes  = 10 << 30
)

// Free is what a filesystem has free: Inodes and Space as a percent of its
// total, InodesLeft and BytesLeft as counts. Inodes is -1 on a filesystem
// with no fixed inode count, such as btrfs.
type Free struct {
	Inodes, Space         float64
	InodesLeft, BytesLeft uint64
}

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
// filesystem is under minPct and under the floor, for its inodes or its
// space. A dir it can't measure is skipped: a missing or unreadable one
// fails later with its own error, and a run is never stopped for want of
// a number.
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
		case f.Inodes >= 0 && f.Inodes < m && f.InodesLeft < FloorInodes:
			return fmt.Errorf("%w: %s has %.1f%% of its inodes free (%d), under the %d%% margin", ticket.ErrLowDisk, d, f.Inodes, f.InodesLeft, minPct)
		case f.Space < m && f.BytesLeft < FloorBytes:
			return fmt.Errorf("%w: %s has %.1f%% of its space free (%d MiB), under the %d%% margin", ticket.ErrLowDisk, d, f.Space, f.BytesLeft>>20, minPct)
		}
	}
	return nil
}
