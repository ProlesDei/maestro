// Package envcheck decides whether a directory is safe for the SQLite store.
// SQLite in WAL mode needs working POSIX locks and shared memory between
// the processes using the file. Network filesystems and VM or container
// shares break one or both, often silently, which loses writes.
package envcheck

import (
	"fmt"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// AllowEnv names the variable that skips the filesystem and lock checks
// when set to 1, for setups the checks misjudge. Repo checks still run.
const AllowEnv = "MAESTRO_TICKET_ALLOW_UNSAFE_FS"

// unsafeFS maps Linux statfs magic numbers to names. Network filesystems
// can't share WAL memory across hosts; FUSE, 9p and virtiofs are how VMs and
// containers share host folders, where locks and mmap are unreliable.
var unsafeFS = map[int64]string{
	0x6969:     "nfs",
	0x517b:     "smb",
	0xff534d42: "cifs",
	0xfe534d42: "smb2",
	0x5346414f: "afs",
	0x73757245: "coda",
	0x564c:     "ncp",
	0x00c36400: "ceph",
	0x0bd00bd0: "lustre",
	0x47504653: "gpfs",
	0x65735546: "fuse",
	0x01021997: "9p",
	0x6a656a63: "virtiofs",
}

// statfs returns the filesystem magic number for dir, or ok=false where
// the platform can't tell. Tests replace it.
var statfs = platformStatfs

// probeLocks checks that two open file descriptions of one file exclude
// each other's write locks. Tests replace it.
var probeLocks = platformProbeLocks

// Filesystem reports whether dir is on a filesystem the store may use.
func Filesystem(dir string) ticket.Check {
	magic, ok, err := statfs(dir)
	switch {
	case err != nil:
		return ticket.Check{Name: "filesystem", OK: false, Detail: fmt.Sprintf("statfs %s: %v", dir, err)}
	case !ok:
		return ticket.Check{Name: "filesystem", OK: true, Detail: "type unknown on this platform; not checked"}
	}
	if name, bad := unsafeFS[magic]; bad {
		return ticket.Check{Name: "filesystem", OK: false, Detail: fmt.Sprintf(
			"%s is on %s: SQLite locking and WAL memory are unreliable there; put the store on a local disk (--store or $MAESTRO_TICKET_STORE), or set %s=1", dir, name, AllowEnv)}
	}
	return ticket.Check{Name: "filesystem", OK: true, Detail: fmt.Sprintf("local (magic 0x%x)", magic)}
}

// Locks reports whether file locks in dir actually exclude each other.
func Locks(dir string) ticket.Check {
	checked, err := probeLocks(dir)
	switch {
	case err != nil:
		return ticket.Check{Name: "locks", OK: false, Detail: fmt.Sprintf(
			"file locks in %s don't exclude each other (%v); put the store on a local disk, or set %s=1", dir, err, AllowEnv)}
	case !checked:
		return ticket.Check{Name: "locks", OK: true, Detail: "not checked on this platform"}
	}
	return ticket.Check{Name: "locks", OK: true, Detail: "a second writer is refused"}
}

// Store runs the filesystem and lock checks for a store directory. With
// allow set, failures are reported as passing with a note.
func Store(dir string, allow bool) []ticket.Check {
	checks := []ticket.Check{Filesystem(dir), Locks(dir)}
	if allow {
		for i := range checks {
			if !checks[i].OK {
				checks[i].OK = true
				checks[i].Detail = "allowed by " + AllowEnv + ": " + checks[i].Detail
			}
		}
	}
	return checks
}

// Err returns the first failed check as an ErrInvalid error, or nil.
func Err(checks []ticket.Check) error {
	for _, c := range checks {
		if !c.OK {
			return fmt.Errorf("%w: %s check failed: %s", ticket.ErrInvalid, c.Name, c.Detail)
		}
	}
	return nil
}
