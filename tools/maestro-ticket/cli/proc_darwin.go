package cli

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// zombie reports whether pid has exited but not been reaped.
func zombie(pid int) bool {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && kp.Proc.P_pid == int32(pid) && kp.Proc.P_stat == 5 // SZOMB
}

// procStamp tells process pid from a later process that reuses its pid,
// as boot|darwin|start time. "" if either can't be read.
func procStamp(pid int) string {
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return ""
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp.Proc.P_pid != int32(pid) {
		return ""
	}
	st := kp.Proc.P_starttime
	return fmt.Sprintf("%d.%d|darwin|%d.%d", boot.Sec, boot.Usec, st.Sec, st.Usec)
}
