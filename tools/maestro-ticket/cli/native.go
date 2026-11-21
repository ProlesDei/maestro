package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// native-worker runs inside the supervisor's process group. Recheck the
// binding here: a queued launch or a resume can outlive a desktop login.
func runNativeWorker(ctx context.Context, c *call) error {
	alias := c.fs.String("profile", "", "bound account")
	job := c.fs.String("job", "", "job name")
	model := c.fs.String("model", "", "model")
	effort := c.fs.String("effort", "", "effort")
	resume := c.fs.String("resume", "", "native session")
	session := c.fs.String("session-id", "", "new Claude session")
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	id := c.env.Getenv(runs.RunIDEnv)
	if !runs.ValidID(id) {
		return errors.New("worker: no supervised run")
	}
	dir, err := c.besideStore("runs")
	if err != nil {
		return err
	}
	record, err := runs.Load(dir, id)
	if err != nil {
		return err
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for record.State == runs.Starting {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("worker: supervisor did not record a launch")
		case <-tick.C:
		}
		record, err = runs.Load(dir, id)
		if err != nil {
			return err
		}
	}
	loc, err := c.location()
	if err != nil {
		return err
	}
	if record.State != runs.Running || record.BoardID != loc.boardID || record.Profile != *alias || record.Job != *job || record.PID != os.Getpid() || record.Stamp != runs.Stamp(os.Getpid()) || (loc.boardID != "" && record.LaunchID == 0) {
		return errors.New("worker: launch receipt does not match this process")
	}
	var provider *exec.Cmd
	defer func() {
		if provider == nil || provider.Process == nil {
			_ = writeWholeMode(filepath.Join(dir, id+".unspent"), []byte("no provider turn"), 0600)
		}
	}()
	if err := c.mayStart(); err != nil {
		return err
	}
	ac, brief := record.ACRevision, record.BriefRevision
	ctx, cancel := context.WithDeadline(ctx, record.StartedAt.Add(record.LimitDuration()))
	defer cancel()
	p, err := c.poolProfile(*alias)
	if err != nil {
		return err
	}
	if !poolAllows(p.Harness, *model, *effort) {
		return errors.New("worker: model or effort isn't one the pool runs on this sub")
	}
	m := jobModel{Model: *model, Effort: *effort, Estimate: record.Estimate}
	st, err := readProfile(ctx, *alias, p.Harness, p.ConfigDir)
	if err != nil || st.identity == "" || st.identity != p.Identity || st.Plan != p.Plan {
		if root, e := c.standingDir(); e == nil {
			if e = writeWholeMode(filepath.Join(root, "account-"+*alias+".login"), []byte(time.Now().UTC().Format(time.RFC3339)), 0600); e != nil {
				return e
			}
		}
		return errors.New("worker admission: subscription login could not be verified; refresh the bound account")
	}
	var args []string
	switch p.Harness {
	case "claude":
		args = []string{"-p", "--model", *model, "--effort", *effort, "--permission-mode", "bypassPermissions", "--verbose", "--output-format", "stream-json"}
		if *resume != "" {
			args = append(args, "--resume", *resume)
		} else {
			args = append(args, "--session-id", *session)
		}
		args, err = c.stopHook(args, *job)
		if err != nil {
			return err
		}
	case "codex":
		args = []string{"exec"}
		if *resume != "" {
			args = append(args, "resume")
		}
		args = append(args, "--json", "--model", *model, "-c", "model_reasoning_effort="+strconv.Quote(*effort), "-c", `model_provider="openai"`, "--dangerously-bypass-approvals-and-sandbox")
		if *resume != "" {
			args = append(args, *resume)
		}
		args = append(args, "-")
	default:
		return errors.New("worker: unknown harness")
	}
	cmd := profileCommand(ctx, p.Harness, p.ConfigDir, args...)
	provider = cmd
	cmd.Dir = c.env.Dir
	cmd.Stdin = c.env.Stdin
	if p.Harness == "codex" && *resume == "" {
		reader := bufio.NewReader(c.env.Stdin)
		k, _ := findJob(*job)
		if prefix, _ := reader.Peek(len(k.skill)); string(prefix) == k.skill {
			reader.Discard(1)
			cmd.Stdin = io.MultiReader(strings.NewReader("$"), reader)
		}
	}
	if len(st.Windows) != 0 {
		if err := c.copyNativeUsage(st, id); err != nil {
			return err
		}
	}
	if loc.boardID != "" {
		// The meter may have moved between admission and the provider login check.
		root := filepath.Dir(dir)
		ledger, err := readUsage(filepath.Join(root, "usage.json"))
		if err != nil {
			return err
		}
		list, err := runs.List(dir, time.Now(), true)
		if err != nil {
			return err
		}
		list = slices.DeleteFunc(list, func(r runs.Record) bool { return r.ID == id })
		if _, reason := projectedRoom(*alias, m, p.Reserve, ledger[*alias], list, time.Now().UTC()); reason != "" {
			return fmt.Errorf("worker admission: %s", reason)
		}
	}
	// Preserve the supervisor identity and the board chosen by the launcher.
	store, _, err := c.storePath()
	if err != nil {
		return err
	}
	cmd.Env = setWorkerVersions(cmd.Env, ac, brief)
	cmd.Env = append(cmd.Env, "MAESTRO_TICKET_STORE="+store, "MAESTRO_TICKET_ACTOR="+c.actor, runs.RunIDEnv+"="+c.env.Getenv(runs.RunIDEnv))
	if p.Harness == "claude" {
		cmd.Env = append(cmd.Env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
		if k, _ := findJob(*job); k.lean {
			cmd.Env = append(cmd.Env, "CLAUDE_CODE_PROMPT_CACHE_TTL=5m")
		}
		err = c.claudeResult(cmd, *alias)
	} else {
		err = codexResult(cmd, c.env.Stdout)
		if after, e := readProfile(ctx, *alias, p.Harness, p.ConfigDir); e == nil && after.identity == p.Identity && len(after.Windows) > 0 {
			if e = c.copyNativeUsage(after, ""); e != nil {
				fmt.Fprintln(c.env.Stderr, "refresh usage:", e)
			}
		}
	}
	if err == nil {
		if e := c.learnSpan(filepath.Dir(dir), record, p.Harness); e != nil {
			fmt.Fprintln(c.env.Stderr, "learn usage:", e)
		}
	}
	return err
}

// Keep the session ID as soon as Codex emits it. Tool calls can contain
// private data; only the last answer and provider error belong in pickup.
func codexResult(cmd *exec.Cmd, out io.Writer) error {
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	dec := json.NewDecoder(pipe)
	enc := json.NewEncoder(out)
	var id, answer string
	var complete, failed bool
	for {
		var event struct {
			Type     string
			ThreadID string `json:"thread_id"`
			Item     struct{ Type, Text string }
			Error    struct{ Message string }
		}
		if err = dec.Decode(&event); err != nil {
			break
		}
		switch event.Type {
		case "thread.started":
			id = event.ThreadID
			if err = enc.Encode(map[string]string{"session_id": id}); err != nil {
				cmd.Process.Kill()
				cmd.Wait()
				return err
			}
		case "item.completed":
			if event.Item.Type == "agent_message" {
				answer = event.Item.Text
			}
		case "turn.completed":
			complete = true
		case "turn.failed":
			failed = true
			answer = event.Error.Message
		}
	}
	if !errors.Is(err, io.EOF) {
		cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if !errors.Is(err, io.EOF) {
		return errors.New("Codex worker: invalid event stream")
	}
	failed = failed || !complete || waitErr != nil || id == ""
	if err := enc.Encode(map[string]any{"type": "result", "session_id": id, "is_error": failed, "result": answer}); err != nil {
		return err
	}
	if failed {
		return errors.New("Codex worker: turn failed or returned no completion")
	}
	return nil
}

func (c *call) nativeCommand(alias, job string, m jobModel, harness string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	store, _, err := c.storePath()
	if err != nil {
		return nil, err
	}
	cmd := []string{exe, "native-worker", "--store", store, "--profile", alias, "--job", job, "--model", m.Model, "--effort", m.Effort}
	if harness == "claude" {
		cmd = append(cmd, "--session-id", newSessionID())
	}
	return cmd, nil
}

func isNativeCommand(cmd []string) bool { return len(cmd) > 1 && cmd[1] == "native-worker" }

// Read the native meter in the same Claude turn. Only its result and
// session ID go to the run log; tool input and intermediate output stay private.
func (c *call) claudeResult(cmd *exec.Cmd, alias string) error {
	root, err := c.standingDir()
	if err != nil {
		return err
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	dec, enc := json.NewDecoder(pipe), json.NewEncoder(c.env.Stdout)
	complete := false
	for {
		var raw json.RawMessage
		if err = dec.Decode(&raw); err != nil {
			break
		}
		var e struct {
			Type    string
			Session string `json:"session_id"`
			Rate    struct {
				Kind    string   `json:"rateLimitType"`
				Used    *float64 `json:"utilization"`
				Reset   int64    `json:"resetsAt"`
				Overage bool     `json:"isUsingOverage"`
			} `json:"rate_limit_info"`
		}
		if err = json.Unmarshal(raw, &e); err != nil {
			break
		}
		switch e.Type {
		case "system":
			if e.Session != "" {
				err = enc.Encode(map[string]string{"session_id": e.Session})
			}
		case "rate_limit_event":
			if e.Rate.Overage {
				err = errors.New("Claude reported billing beyond the subscription allowance")
				break
			}
			if e.Rate.Used != nil && *e.Rate.Used >= 0 && *e.Rate.Used <= 1 && e.Rate.Reset > time.Now().Unix() && e.Rate.Kind != "" {
				w := map[string]paceWindow{e.Rate.Kind: {Used: 100 * *e.Rate.Used, Reset: time.Unix(e.Rate.Reset, 0).UTC(), Read: time.Now().UTC(), Source: "claude"}}
				c.span.see(w)
				err = saveObservation(root, alias, w)
			}
		case "result":
			var result map[string]json.RawMessage
			if err = json.Unmarshal(raw, &result); err != nil {
				break
			}
			out := map[string]json.RawMessage{}
			for _, field := range []string{"type", "subtype", "session_id", "is_error", "result", "duration_ms", "duration_api_ms", "num_turns", "total_cost_usd", "modelUsage", "stop_reason"} {
				if v, ok := result[field]; ok {
					out[field] = v
				}
			}
			err = enc.Encode(out)
			complete = true
		}
		if err != nil {
			break
		}
	}
	if !errors.Is(err, io.EOF) {
		cmd.Process.Kill()
		cmd.Wait()
		return err
	}
	if err = cmd.Wait(); err != nil {
		return fmt.Errorf("Claude worker: %w", err)
	}
	if !complete {
		return errors.New("Claude worker returned no final result")
	}
	return nil
}
