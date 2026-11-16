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
// run whose supervisor went away, even outside its process group. A
// process can fork between a scan and its kill, so it scans again until a
// scan finds none.
func KillRun(id string) {
	for range 20 {
		if n, _ := signalRun(id, syscall.SIGKILL, 0); n == 0 {
			return
		}
	}
}

// termRun asks every process of run id to stop, as KillRun finds them,
// and returns how many of them had left group, the run's process group.
func termRun(id string, group int) (escaped int) {
	_, escaped = signalRun(id, syscall.SIGTERM, group)
	return escaped
}

// runAlive reports whether any process of run id is left.
func runAlive(id string) bool {
	n, _ := signalRun(id, 0, 0)
	return n > 0
}

// signalRun sends sig to every process with run id in its environment,
// but this one, and returns how many took it; with a group, also how
// many of those were outside it.
func signalRun(id string, sig syscall.Signal, group int) (n, outside int) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return 0, 0
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		for _, kv := range procList(pid, "environ") {
			if kv == RunIDEnv+"="+id {
				pg := group
				if group != 0 {
					pg, _ = syscall.Getpgid(pid) // before the signal ends it
				}
				if syscall.Kill(pid, sig) == nil {
					n++
					if pg != group {
						outside++
					}
				}
				break
			}
		}
	}
	return n, outside
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
