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
// as boot session|darwin|start time. "" if either can't be read. The boot
// part is the boot's own id: kern.boottime moves when the clock is set.
func procStamp(pid int) string {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || boot == "" {
		return ""
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp.Proc.P_pid != int32(pid) {
		return ""
	}
	st := kp.Proc.P_starttime
	return fmt.Sprintf("%s|darwin|%d.%d", boot, st.Sec, st.Usec)
}
