package cli

import (
	"bytes"
	"fmt"
	"os"
)

// zombie reports whether pid has exited but not been reaped. The state
// follows the command name, which may hold spaces and parentheses.
func zombie(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 || i+2 >= len(b) {
		return false
	}
	return b[i+2] == 'Z' || b[i+2] == 'X'
}
