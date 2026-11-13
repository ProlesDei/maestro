package runs

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// CanScan reports whether Untracked works on this system.
const CanScan = true

// Untracked lists the headless claude sessions (claude -p) working in one
// of roots, or a folder in one, that launch didn't start: no RunIDEnv in
// their environment. It skips this process's own ancestors, so a session
// that runs doctor never flags itself, and processes it can't read, such
// as another user's.
func Untracked(roots []string) ([]Session, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var clean []string
	for _, r := range roots {
		if p, err := filepath.EvalSymlinks(r); err == nil {
			clean = append(clean, p)
		}
	}
	skip := ancestors()
	var out []Session
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || skip[pid] {
			continue
		}
		argv := procList(pid, "cmdline")
		if !headlessClaude(argv) || hasEnv(procList(pid, "environ"), RunIDEnv) {
			continue
		}
		cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
		if err != nil || !under(cwd, clean) {
			continue
		}
		out = append(out, Session{PID: pid, Dir: cwd})
	}
	return out, nil
}

// KillRun kills every process launch started for run id, and every one
// they started, found by RunIDEnv in its environment: what is left of a
// run whose supervisor went away, even outside its process group.
func KillRun(id string) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		for _, kv := range procList(pid, "environ") {
			if kv == RunIDEnv+"="+id {
				syscall.Kill(pid, syscall.SIGKILL)
				break
			}
		}
	}
}

func procList(pid int, name string) []string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/%s", pid, name))
	if err != nil || len(b) == 0 {
		return nil
	}
	return strings.Split(string(bytes.TrimRight(b, "\x00")), "\x00")
}

func hasEnv(env []string, key string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return true
		}
	}
	return false
}

// ancestors returns this process and every process above it.
func ancestors() map[int]bool {
	out := map[int]bool{}
	for pid := os.Getpid(); pid > 1 && !out[pid]; {
		out[pid] = true
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			break
		}
		i := bytes.LastIndexByte(b, ')')
		f := bytes.Fields(b[i+1:]) // state is f[0], ppid f[1]
		if i < 0 || len(f) < 2 {
			break
		}
		pid, _ = strconv.Atoi(string(f[1]))
	}
	return out
}
