package cli

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// zombie reports whether pid has exited but not been reaped.
func zombie(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	return err == nil && zombieStat(b)
}

// zombieStat reads a /proc/<pid>/stat line. The fields follow the command
// name, which may hold spaces and parentheses. A process whose main thread
// exited shows Z too, but still has other threads running.
func zombieStat(b []byte) bool {
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return false
	}
	f := bytes.Fields(b[i+1:]) // state is f[0], num_threads f[17]
	if len(f) < 18 || (string(f[0]) != "Z" && string(f[0]) != "X") {
		return false
	}
	return string(f[17]) == "1"
}

// procStamp tells process pid from a later process that reuses its pid,
// as boot|pid namespace|start time. "" if any part can't be read. A
// process seen from another pid namespace (a container) has another pid,
// so a stamp from there can't be judged here: see launchGone.
func procStamp(pid int) string {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	ns, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return ""
	}
	f := bytes.Fields(b[i+1:]) // state is f[0]; starttime, field 22, is f[19]
	if len(f) < 20 {
		return ""
	}
	return strings.TrimSpace(string(boot)) + "|" + ns + "|" + string(f[19])
}
