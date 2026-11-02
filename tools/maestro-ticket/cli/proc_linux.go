package cli

import (
	"bytes"
	"fmt"
	"os"
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
