package cli

import "golang.org/x/sys/unix"

// zombie reports whether pid has exited but not been reaped.
func zombie(pid int) bool {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && kp.Proc.P_pid == int32(pid) && kp.Proc.P_stat == 5 // SZOMB
}

// procStamp can't tell a pid's process from a later one here.
func procStamp(int) string { return "" }
