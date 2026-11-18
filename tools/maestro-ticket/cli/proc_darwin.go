package cli

import (
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"golang.org/x/sys/unix"
)

// zombie reports whether pid has exited but not been reaped.
func zombie(pid int) bool {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && kp.Proc.P_pid == int32(pid) && kp.Proc.P_stat == 5 // SZOMB
}

// procStamp tells process pid from a later process that reuses its pid.
func procStamp(pid int) string { return runs.Stamp(pid) }
