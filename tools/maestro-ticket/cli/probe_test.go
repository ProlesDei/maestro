//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/streamfilter"
)

// TestProbeClaude runs the real Claude Code with a job's argv as
// claudeCommand writes it (json, as on main) and as the supervisor
// swaps it (stream-json --verbose), and checks that the swap costs no
// tokens, leaves stderr and the text before the first JSON line as they
// were, and gives the facts stats reads. It spends tokens, so it runs
// only with MAESTRO_PROBE_CLAUDE=1.
func TestProbeClaude(t *testing.T) {
	if os.Getenv("MAESTRO_PROBE_CLAUDE") != "1" {
		t.Skip("set MAESTRO_PROBE_CLAUDE=1 to run Claude Code for real")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("no claude on PATH")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	type arm struct {
		out, err []byte
		code     int
	}
	run := func(argv []string, prompt string, turns string) arm {
		t.Helper()
		argv = append(slices.Clone(argv), "--max-turns", turns)
		c := exec.Command(argv[0], argv[1:]...)
		c.Dir = dir
		c.Stdin = strings.NewReader(prompt)
		var out, errb bytes.Buffer
		c.Stdout, c.Stderr = &out, &errb
		c.Run()
		return arm{out.Bytes(), errb.Bytes(), c.ProcessState.ExitCode()}
	}
	both := func(model, prompt, turns string) (a, b arm, sum streamfilter.Summary, kept []byte) {
		t.Helper()
		a = run(withEnv(claudeCommand(newSessionID(), model, effortHigh, false)), prompt, turns)
		swapped, ok := runs.StreamArgv(withEnv(claudeCommand(newSessionID(), model, effortHigh, false)))
		if !ok {
			t.Fatal("claudeCommand has no --output-format json pair")
		}
		b = run(swapped, prompt, turns)
		sum, kept = streamfilter.Scan(bytes.NewReader(b.out), streamfilter.Names{Sub: commandNames(), Model: modelFamily})
		return
	}
	input := func(r claudeReply) (in, out int64) {
		for _, u := range r.ModelUsage {
			in += u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
			out += u.OutputTokens
		}
		return
	}

	// Tokens, stderr and the result's keys.
	a, b, sum, kept := both(modelSonnet, "Reply with the one word: ok\n", "1")
	ra, errA := lastResult(a.out)
	rb, errB := lastResult(kept)
	if errA != nil || errB != nil {
		t.Fatalf("results: json %v, stream %v\n%s\n%s", errA, errB, a.out, kept)
	}
	inA, outA := input(ra)
	inB, outB := input(rb)
	t.Logf("input+cache json %d, stream %d; output %d, %d", inA, inB, outA, outB)
	if d := inA - inB; d*100 > inA || -d*100 > inA {
		t.Errorf("input tokens differ by over 1%%: %d, %d", inA, inB)
	}
	if !bytes.Equal(a.err, b.err) || a.code != b.code {
		t.Errorf("stderr or exit differ: %d %q / %d %q", a.code, a.err, b.code, b.err)
	}
	for _, out := range [][]byte{a.out, kept} {
		var keys map[string]json.RawMessage
		json.Unmarshal(bytes.TrimSpace(out), &keys)
		for _, k := range []string{"subtype", "stop_reason", "num_turns", "duration_api_ms", "modelUsage", "session_id", "total_cost_usd"} {
			if _, ok := keys[k]; !ok {
				t.Errorf("result has no %s", k)
			}
		}
	}
	if !sum.Result || sum.Version == "" || sum.CtxPeak == 0 || len(sum.Usage) == 0 {
		t.Errorf("stream summary %+v", sum)
	}
	t.Logf("claude %s, ctx_peak %d, calls %d", sum.Version, sum.CtxPeak, sum.ToolCalls)

	// A bad model: the same exit, stderr and text before any JSON.
	a, b, _, kept = both("claude-no-such-model-0", "Reply ok\n", "1")
	pre := func(out []byte) string {
		i := bytes.IndexByte(out, '{')
		if i < 0 {
			return string(out)
		}
		return string(out[:i])
	}
	if a.code != b.code || !bytes.Equal(a.err, b.err) || pre(a.out) != pre(kept) {
		t.Errorf("bad model: exit %d/%d, stderr %q/%q, pre %q/%q", a.code, b.code, a.err, b.err, pre(a.out), pre(kept))
	}

	// A Bash call that exits nonzero counts as a fail.
	_, _, sum, _ = both(modelSonnet, "Run the shell command `false` with the Bash tool once, then reply done.\n", "2")
	if sum.ToolCalls == 0 || sum.ToolFails == 0 {
		t.Errorf("a failing Bash call: %+v", sum)
	}
	t.Logf("bash: %d calls, %d fails, steps %+v", sum.ToolCalls, sum.ToolFails, sum.Steps)
}
