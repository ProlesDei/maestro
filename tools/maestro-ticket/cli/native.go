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
	"strconv"
	"strings"

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
	p, m, err := c.jobProfile(*alias, *job)
	if err != nil {
		return err
	}
	if m.Model != *model || m.Effort != *effort {
		return errors.New("worker: job's model or effort no longer matches its binding")
	}
	st, err := readProfile(ctx, *alias, p.Harness, p.ConfigDir)
	if err != nil {
		return err
	}
	if st.identity == "" || st.identity != p.Identity || st.Plan != p.Plan {
		return errors.New("worker: native account or subscription changed; verify profile binding")
	}
	var args []string
	switch p.Harness {
	case "claude":
		args = []string{"-p", "--model", *model, "--effort", *effort, "--permission-mode", "bypassPermissions", "--output-format", "json"}
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
		if err := c.copyNativeUsage(st); err != nil {
			return err
		}
	}
	// Preserve the supervisor identity and the board chosen by the launcher.
	store, _, err := c.storePath()
	if err != nil {
		return err
	}
	cmd.Env = append(cmd.Env, "MAESTRO_TICKET_STORE="+store, "MAESTRO_TICKET_ACTOR="+c.actor, runs.RunIDEnv+"="+c.env.Getenv(runs.RunIDEnv))
	if p.Harness == "claude" {
		cmd.Env = append(cmd.Env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
		if k, _ := findJob(*job); k.lean {
			cmd.Env = append(cmd.Env, "CLAUDE_CODE_PROMPT_CACHE_TTL=5m")
		}
		cmd.Stdout = c.env.Stdout
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("Claude worker: %w", err)
		}
		return nil
	}
	return codexResult(cmd, c.env.Stdout)
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
