package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type execResult struct {
	exit   int
	stdout string
	stderr string
}

// run starts bin and waits. The error is only for a binary that can't
// start; a non-zero exit is in the result.
func (e *Engine) run(dir, bin string, args ...string) (execResult, error) {
	return e.runEnv(dir, bin, nil, args...)
}

// runEnv is run with extra environment variables.
func (e *Engine) runEnv(dir, bin string, extra []string, args ...string) (execResult, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string(nil), e.cfg.Environ...), extra...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := execResult{stdout: out.String(), stderr: errb.String()}
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		res.exit = ee.ExitCode()
	case err != nil:
		return res, fmt.Errorf("%s: %w", bin, err)
	}
	return res, nil
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

// runner sends one request and returns its reply: status, exit_code,
// output and any fields the request adds. A reply that isn't JSON is
// status failed.
func (e *Engine) runner(st *State, words ...string) (map[string]any, error) {
	args := []string{"--json", "--bot", st.Bot, "--role", st.Role}
	if st.Ticket != "" {
		args = append(args, "--ticket", st.Ticket)
	}
	if st.ID != "" {
		args = append(args, "--run", st.ID)
	}
	// Every process a run starts, and every session the runner launches for
	// it, carries the run's id, so none can start a run of its own (0787).
	var env []string
	if st.ID != "" {
		env = []string{RunEnv + "=" + st.ID}
	}
	res, err := e.runEnv(e.cfg.Dir, e.cfg.RunnerBin, env, append(args, words...)...)
	if err != nil {
		return nil, err
	}
	reply := map[string]any{}
	if decodeJSON([]byte(res.stdout), &reply) != nil {
		reply = map[string]any{"status": "failed", "output": trimOutput(res.stdout + res.stderr)}
	}
	if _, ok := reply["status"].(string); !ok {
		reply["status"] = "failed"
	}
	if _, ok := reply["exit_code"]; !ok {
		reply["exit_code"] = res.exit
	}
	if _, ok := reply["output"]; !ok {
		reply["output"] = trimOutput(res.stderr)
	}
	reply["exit_code"] = toInt(reply["exit_code"])
	return reply, nil
}

// RunEnv names the run a process belongs to. The engine sets it on the
// runner, and the runner passes it to every session it launches.
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

// trimOutput keeps the last 40 lines, like the runner does.
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
