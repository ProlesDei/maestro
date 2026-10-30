package engine

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Sessions: the engine runs each prompt step as one headless harness call,
// claude -p (the default) or codex exec, and reads the structured reply.
// A run's prompt steps share one session unless the playbook says
// otherwise: the first call starts it, later calls resume it.

// harnessName is .env.harness, the name nameSkills reads.
func harnessName(h string) string {
	if h == "codex" {
		return "codex"
	}
	return "claude-code"
}

// familyOf is the model family a harness runs.
func familyOf(h string) string {
	if h == "codex" {
		return "openai"
	}
	return "claude"
}

func otherHarness(h string) string {
	if h == "codex" {
		return "claude"
	}
	return "codex"
}

func (e *Engine) harnessBin(h string) string {
	if h == "codex" {
		return e.cfg.CodexBin
	}
	return e.cfg.ClaudeBin
}

// model is the model name for a tier: Config.Models for the configured
// harness, else the harness's own default ("" lets the harness pick).
func (e *Engine) model(h, tier string) string {
	if h == e.cfg.Harness {
		if m := e.cfg.Models[tier]; m != "" {
			return m
		}
	}
	if h == "claude" {
		return map[string]string{"mid": "sonnet", "frontier": "opus"}[tier]
	}
	return ""
}

// sessionCall is one prompt for a harness session.
type sessionCall struct {
	harness    string
	prompt     string
	schemaFile string
	tier       string
	resume     bool
}

// harness sends one prompt and returns status (ok or failed), exit_code,
// output, reply (the structured reply, or nil), family and session.
func (e *Engine) harness(st *State, c sessionCall) (map[string]any, error) {
	rules := ""
	if b, err := os.ReadFile(filepath.Join(e.cfg.Playbooks, "rules.md")); err == nil {
		rules = strings.TrimSpace(string(b))
	}
	schema, err := os.ReadFile(c.schemaFile)
	if err != nil {
		return nil, err
	}
	if c.harness == "codex" {
		return e.codex(st, c, rules, schema)
	}
	args := []string{"-p", "--output-format", "json", "--json-schema", string(schema)}
	if m := e.model(c.harness, c.tier); m != "" {
		args = append(args, "--model", m)
	}
	if rules != "" {
		args = append(args, "--append-system-prompt", rules)
	}
	if c.resume {
		args = append(args, "--resume", st.HarnessSession)
	} else {
		args = append(args, "--session-id", st.HarnessSession)
	}
	args = append(args, e.cfg.ClaudeArgs...)
	r, err := e.exec(e.workDir(st), e.cfg.ClaudeBin, e.runEnv(st), c.prompt, args...)
	if err != nil {
		return nil, err
	}
	var out struct {
		IsError    bool    `json:"is_error"`
		Result     string  `json:"result"`
		SessionID  string  `json:"session_id"`
		Structured any     `json:"structured_output"`
		Cost       float64 `json:"total_cost_usd"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		return newReply("failed", r.exit, "claude: no JSON result: "+r.stdout+r.stderr), nil
	}
	status := "ok"
	if r.exit != 0 || out.IsError {
		status = "failed"
	}
	rep, _ := normalizeValue(out.Structured).(map[string]any)
	if rep == nil {
		rep = replyFromText(out.Result)
	}
	if out.SessionID != "" {
		st.HarnessSession = out.SessionID
	}
	return newReply(status, r.exit, out.Result+r.stderr, "reply", rep, "family", familyOf(c.harness),
		"session", st.HarnessSession, "cost_usd", out.Cost), nil
}

// codex runs codex exec, or codex exec resume. Codex has no flag for extra
// rules, so the first prompt of a session starts with them. Its
// --output-schema wants a strict schema (every key required, no other
// keys), which out: fields of any type can't be, so the prompt ends with
// the schema and the reply is read from the last message's text.
func (e *Engine) codex(st *State, c sessionCall, rules string, schema []byte) (map[string]any, error) {
	last := strings.TrimSuffix(c.schemaFile, ".schema.json") + ".last.json"
	os.Remove(last)
	var args []string
	prompt := c.prompt + "\nEnd with one JSON object that matches this JSON schema, and nothing after it:\n" + string(schema) + "\n"
	if c.resume {
		args = []string{"exec", "resume", st.HarnessSession}
	} else {
		args = []string{"exec", "-C", e.workDir(st)}
		if rules != "" {
			prompt = rules + "\n\n" + prompt
		}
	}
	args = append(args, "--json", "-o", last)
	if m := e.model(c.harness, c.tier); m != "" {
		args = append(args, "-m", m)
	}
	args = append(append(args, e.cfg.CodexArgs...), "-")
	r, err := e.exec(e.workDir(st), e.cfg.CodexBin, e.runEnv(st), prompt, args...)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(strings.NewReader(r.stdout))
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		var ev struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Type == "thread.started" && ev.ThreadID != "" {
			st.HarnessSession = ev.ThreadID
		}
	}
	b, _ := os.ReadFile(last)
	status := "ok"
	if r.exit != 0 {
		status = "failed"
	}
	return newReply(status, r.exit, string(b)+r.stderr, "reply", replyFromText(string(b)), "family", familyOf(c.harness),
		"session", st.HarnessSession), nil
}

// replyFromText finds the JSON object in a reply's text: the whole text, a
// fenced block, or the outermost braces.
func replyFromText(text string) map[string]any {
	t := strings.TrimSpace(text)
	try := []string{t}
	if i := strings.Index(t, "```"); i >= 0 {
		rest := t[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		if j := strings.Index(rest, "```"); j >= 0 {
			try = append(try, rest[:j])
		}
	}
	if i, j := strings.Index(t, "{"), strings.LastIndex(t, "}"); i >= 0 && j > i {
		try = append(try, t[i:j+1])
	}
	for _, s := range try {
		m := map[string]any{}
		if decodeJSON([]byte(strings.TrimSpace(s)), &m) == nil {
			return m
		}
	}
	return nil
}
