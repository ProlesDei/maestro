package runs

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// Stamp tells process pid from a later process that reuses its pid, as
// boot session|darwin|start time. "" if either can't be read. The boot
// part is the boot's own id: kern.boottime moves when the clock is set.
func Stamp(pid int) string {
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

// sessionLeft reports whether r's session still runs: nothing here ends
// it when its supervisor dies, and KillRun can't find it.
func sessionLeft(r Record) bool {
	return r.PID > 0 && r.Stamp != "" && Stamp(r.PID) == r.Stamp
}
