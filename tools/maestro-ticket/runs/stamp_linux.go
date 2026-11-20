package runs

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// procStamp tells process pid from a later process that reuses its pid,
// as boot|pid namespace|start time. "" if any part can't be read. A
// process seen from another pid namespace (a container) has another pid,
// so a stamp from there can't be judged here: see launchGone.
func Stamp(pid int) string {
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

func sessionLeft(r Record) bool {
	return r.BoardID != "" && r.PID > 0 && r.Stamp != "" && Stamp(r.PID) == r.Stamp &&
		nativeProcess(r.PID, r.BoardID) == r.ID
}
