package runs

import (
	"path/filepath"
	"slices"
	"strings"
)

// Session is a claude -p process that launch didn't start.
type Session struct {
	PID int    `json:"pid"`
	Dir string `json:"dir"`
}

// headlessClaude reports whether argv runs Claude Code with -p or
// --print: the claude binary itself, or node or bun running its script.
func headlessClaude(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	isClaude := func(a string) bool {
		b := filepath.Base(a)
		return b == "claude" || strings.Contains(a, "claude-code/cli.")
	}
	if !isClaude(argv[0]) && !(len(argv) > 1 && slices.Contains([]string{"node", "bun"}, filepath.Base(argv[0])) && isClaude(argv[1])) {
		return false
	}
	for _, a := range argv[1:] {
		if a == "-p" || a == "--print" || strings.HasPrefix(a, "--print=") {
			return true
		}
	}
	return false
}

// under reports whether dir is one of roots or inside one.
func under(dir string, roots []string) bool {
	for _, r := range roots {
		if dir == r || strings.HasPrefix(dir, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
