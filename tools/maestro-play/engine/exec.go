package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type execResult struct {
	exit   int
	stdout string
	stderr string
}

// timedOutExit is a timed-out command's exit code, as timeout(1) has it.
const timedOutExit = 124

// run starts bin and waits, with no time limit: maestro-ticket calls are
// short, and a claim's keepalive runs in a session of its own (maestro-
// ticket starts it with setsid), so it outlives the call. The error is
// only for a binary that can't start; a non-zero exit is in the result.
func (e *Engine) run(dir, bin string, args ...string) (execResult, error) {
	return e.exec(dir, bin, nil, "", -1, args...)
}

// exec is run with extra environment variables, stdin and a time limit (a
// negative one is none). The command gets its own process group, so a
// timeout kills what it started too; the result then has exit 124 and
// stderr ends "timed out after <d>". Once the command exits, the engine
// kills what it left in the group, so a background child can neither hold
// the output open nor outlive the call. A signal that would stop this
// process goes to the group first.
func (e *Engine) exec(dir, bin string, extra []string, stdin string, timeout time.Duration, args ...string) (execResult, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string(nil), e.cfg.Environ...), extra...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	// Our own pipes: with an *os.File for stdout and stderr, Wait returns
	// when the command exits, not when the last child holding them does.
	outR, outW, err := os.Pipe()
	if err != nil {
		return execResult{}, err
	}
	defer outR.Close()
	errR, errW, err := os.Pipe()
	if err != nil {
		outW.Close()
		return execResult{}, err
	}
	defer errR.Close()
	cmd.Stdout, cmd.Stderr = outW, errW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second // a child that left the group may hold stdin
	err = cmd.Start()
	outW.Close()
	errW.Close()
	if err != nil {
		return execResult{}, fmt.Errorf("%s: %w", bin, err)
	}
	var out, errb bytes.Buffer
	copied := make(chan struct{}, 2)
	for _, p := range []struct {
		dst *bytes.Buffer
		src *os.File
	}{{&out, outR}, {&errb, errR}} {
		go func() { io.Copy(p.dst, p.src); copied <- struct{}{} }()
	}
	stop := forwardSignals(cmd.Process.Pid)
	err = cmd.Wait()
	stop()
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	// A child that left the group may still hold the pipes: read 5s more.
	late := time.After(5 * time.Second)
	for range 2 {
		select {
		case <-copied:
		case <-late:
			outR.Close()
			errR.Close()
			<-copied
		}
	}
	res := execResult{stdout: out.String(), stderr: errb.String()}
	var ee *exec.ExitError
	switch {
	case err != nil && ctx.Err() == context.DeadlineExceeded:
		res.exit = timedOutExit
		res.stderr += fmt.Sprintf("\ntimed out after %s\n", timeout)
	case errors.As(err, &ee):
		res.exit = ee.ExitCode()
	case errors.Is(err, exec.ErrWaitDelay): // it exited; a child held stdin
		res.exit = cmd.ProcessState.ExitCode()
	case err != nil:
		return res, fmt.Errorf("%s: %w", bin, err)
	}
	return res, nil
}

// forwardSignals sends an interrupt, hangup or terminate to the process
// group pgid, which no longer shares this process's, then lets it stop
// this process as before. stop ends it.
func forwardSignals(pgid int) (stop func()) {
	sigs := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(sigs, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		select {
		case s := <-sigs:
			syscall.Kill(-pgid, s.(syscall.Signal))
			signal.Reset(s)
			syscall.Kill(os.Getpid(), s.(syscall.Signal))
		case <-done:
		}
	}()
	return func() {
		signal.Stop(sigs)
		close(done)
	}
}

// ticketCall is one maestro-ticket call's outcome.
type ticketCall struct {
	exit   int
	result any    // the parsed --json output
	code   string // the error code, on failure
	output string // trimmed stdout and stderr
}

func (e *Engine) ticket(args ...string) (ticketCall, error) {
	res, err := e.run(e.cfg.Dir, e.cfg.TicketBin, args...)
	if err != nil {
		return ticketCall{}, err
	}
	c := ticketCall{exit: res.exit, output: trimOutput(res.stdout + res.stderr)}
	var v any
	if decodeJSON([]byte(res.stdout), &v) == nil {
		c.result = v
		if m, ok := v.(map[string]any); ok && res.exit != 0 {
			if em, ok := m["error"].(map[string]any); ok {
				c.code, _ = em["code"].(string)
			}
		}
	}
	return c, nil
}

// RunEnv names the run a process belongs to. The engine sets it on every
// command and session it starts, so none of them can start a run of its
// own.
const RunEnv = "MAESTRO_RUN_ID"

// insideRun is the run this engine's own process belongs to, or "".
func (e *Engine) insideRun() string {
	for _, kv := range e.cfg.Environ {
		if v, ok := strings.CutPrefix(kv, RunEnv+"="); ok && v != "" {
			return v
		}
	}
	return ""
}

// trimOutput keeps the last 40 lines.
func trimOutput(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return strings.Join(lines, "\n")
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

// decodeJSON decodes like encoding/json, but integral numbers become ints,
// so templates can compare and index with them.
func decodeJSON(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	switch x := v.(type) {
	case *any:
		*x = normalizeValue(*x)
	case *map[string]any:
		normalize(*x)
	}
	return nil
}

func normalize(m map[string]any) {
	for k, v := range m {
		m[k] = normalizeValue(v)
	}
}

func normalizeValue(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i)
		}
		f, _ := x.Float64()
		return f
	case float64:
		if x == float64(int(x)) {
			return int(x)
		}
	case map[string]any:
		normalize(x)
	case []any:
		for i := range x {
			x[i] = normalizeValue(x[i])
		}
	}
	return v
}
