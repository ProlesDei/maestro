package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

// readPrompt reads <name>.md from the playbook's folder, else from the
// playbooks folder, where the shared _contract.md and _escalate.md live.
func (e *Engine) readPrompt(p *playbook.Playbook, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(p.Dir(), name+".md"))
	if errors.Is(err, os.ErrNotExist) {
		b, err = os.ReadFile(filepath.Join(e.cfg.Playbooks, name+".md"))
	}
	return string(b), err
}

// compose renders a prompt step: _contract.md, then the skills use.yaml
// names for the template, then the template. The fixed text comes first so
// the harness can cache it.
func (e *Engine) compose(p *playbook.Playbook, template string, data map[string]any, harness string) (string, error) {
	contract, err := e.readPrompt(p, "_contract")
	if err != nil {
		return "", err
	}
	parts := []string{}
	c, err := renderTemplate("_contract.md", contract, data)
	if err != nil {
		return "", err
	}
	parts = append(parts, strings.TrimRight(c, "\n"))
	if skills := e.uses.PromptSkills(template); len(skills) > 0 {
		parts = append(parts, nameSkills(harness, skills))
	}
	text, err := e.readPrompt(p, template)
	if err != nil {
		return "", err
	}
	body, err := renderTemplate(template+".md", text, data)
	if err != nil {
		return "", err
	}
	parts = append(parts, strings.TrimRight(body, "\n"))
	return strings.Join(parts, "\n\n") + "\n", nil
}

// nameSkills names skills the harness's way.
func nameSkills(harness string, skills []string) string {
	lines := []string{"Skills for this step:"}
	for _, s := range skills {
		switch harness {
		case "claude-code", "claude":
			lines = append(lines, "/"+s)
		case "grok-build", "grok":
			lines = append(lines, "Read and follow .agents/skills/"+s+"/SKILL.md")
		case "codex":
			lines = append(lines, "$"+s)
		default:
			lines = append(lines, "Use the "+s+" skill.")
		}
	}
	return strings.Join(lines, "\n")
}

// replySchema is the JSON schema for a prompt step's reply: the base
// fields from _contract.md plus the step's out: fields.
func replySchema(out []string) map[string]any {
	props := map[string]any{
		"status":    map[string]any{"enum": playbook.ReplyStatuses},
		"summary":   map[string]any{"type": "string"},
		"evidence":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"retro":     map[string]any{"type": "string"},
		"memory":    map[string]any{},
		"questions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"processes": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "required": []string{"pid", "command"},
			"properties": map[string]any{"pid": map[string]any{"type": "integer"}, "command": map[string]any{"type": "string"}},
		}},
	}
	for _, f := range out {
		props[f] = map[string]any{}
	}
	return map[string]any{"type": "object", "required": []string{"status", "summary"}, "properties": props}
}

// checkReply says what a reply misses, or "". Done and failed replies need
// every out: field; a done reply needs evidence.
func checkReply(reply map[string]any, out []string) string {
	if reply == nil {
		return "no structured reply"
	}
	status, _ := reply["status"].(string)
	if !playbook.Contains(playbook.ReplyStatuses, status) {
		return fmt.Sprintf("status %q: want one of %s", status, strings.Join(playbook.ReplyStatuses, ", "))
	}
	if s, _ := reply["summary"].(string); strings.TrimSpace(s) == "" {
		return "summary"
	}
	var missing []string
	if status == "done" || status == "failed" {
		for _, f := range out {
			if v, ok := reply[f]; !ok || v == nil {
				missing = append(missing, f)
			}
		}
	}
	if len(missing) > 0 {
		return "fields: " + strings.Join(missing, ", ")
	}
	// A commit or sha lands in a note's sha: trailer as it is, so it must be
	// one sha, never text that could add a line of its own.
	for _, f := range shaFields {
		if v, ok := reply[f]; ok && v != nil {
			sha, _ := v.(string)
			if !shaRE.MatchString(sha) && !(sha == "" && status != "done") {
				return fmt.Sprintf("%s: want one git sha (7 to 40 hex digits), got %q", f, v)
			}
		}
	}
	if status == "done" {
		if ev, _ := reply["evidence"].([]any); len(ev) == 0 {
			return "evidence: a done reply lists what you ran or read"
		}
	}
	if ps, ok := reply["processes"]; ok && ps != nil {
		if _, err := processes(ps); err != nil {
			return "processes: " + err.Error()
		}
	}
	return ""
}

// shaFields are the reply fields notes write as a sha: trailer.
var shaFields = []string{"commit", "sha"}

var shaRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func processes(v any) ([]Process, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var ps []Process
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, fmt.Errorf("want [{pid, command}]")
	}
	for _, p := range ps {
		if p.PID <= 0 || p.Command == "" {
			return nil, fmt.Errorf("want a pid and a command for each")
		}
	}
	return ps, nil
}

func tierUp(tier string) string {
	i := -1
	for j, t := range playbook.Tiers {
		if t == tier {
			i = j
		}
	}
	if i >= 0 && i+1 < len(playbook.Tiers) {
		return playbook.Tiers[i+1]
	}
	return tier
}

// authorFamily reads author-family: from the latest Handoff: comment: its
// last such line, which the note writes after every reply value, so a
// reply can't forge it.
func authorFamily(t map[string]any) string {
	cs, _ := t["comments"].([]any)
	for i := len(cs) - 1; i >= 0; i-- {
		c, _ := cs[i].(map[string]any)
		body := str(c["body"])
		if !strings.HasPrefix(body, "Handoff:") {
			continue
		}
		return lineValue("author-family", body)
	}
	tr, _ := t["trailers"].(map[string]any)
	return str(tr["author-family"])
}

// session sends one prompt to the harness: a new session for the first
// prompt, a resume after. A not-author step whose author's family is the
// harness's own goes to the other harness, in a session of its own.
func (e *Engine) session(st *State, name, prompt string, out []string, tier, notFamily string, fresh bool) (map[string]any, error) {
	dir := runDir(e.cfg.Common, st.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	promptFile := filepath.Join(dir, name+".prompt.md")
	schemaFile := filepath.Join(dir, name+".schema.json")
	schema, _ := json.Marshal(replySchema(out))
	if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(schemaFile, schema, 0o644); err != nil {
		return nil, err
	}
	h := e.cfg.Harness
	if notFamily != "" && familyOf(h) == notFamily {
		h = otherHarness(h)
	}
	if fresh || st.Harness != h {
		st.SessionStarted = false
	}
	if !st.SessionStarted {
		// A failed start may still have made the session, so every start
		// gets a new id.
		st.Session, st.Harness = newUUID(), h
		st.HarnessSession = st.Session
	}
	r, err := e.harness(st, sessionCall{harness: h, prompt: prompt, schemaFile: schemaFile, tier: tier, resume: st.SessionStarted})
	if err != nil {
		return nil, err
	}
	if r["status"] == "ok" {
		st.SessionStarted = true
	}
	return r, nil
}

// harnessReply is the structured reply inside a session reply.
func harnessReply(r map[string]any) map[string]any {
	m, _ := r["reply"].(map[string]any)
	return m
}

func (e *Engine) promptStep(p *playbook.Playbook, st *State, s *playbook.Step, c call) (stepOut, error) {
	rec := c.rec
	notFamily := ""
	if s.Family == "not-author" {
		if notFamily = authorFamily(st.TicketD); notFamily == "" {
			return blockOut("step %s: family not-author: no author-family: in the latest Handoff: comment", s.ID), nil
		}
	}
	retry := playbook.Retry{Max: toInt(e.limits(st)["retries"])}
	if s.Retry != nil {
		retry = *s.Retry
	}
	tier := s.Tier()
	fresh := p.Session == "step" || s.Session == "fresh"
	for try := 0; ; try++ {
		prompt, err := e.compose(p, s.Prompt, e.data(p, st, s), str(st.Env["harness"]))
		if err != nil {
			return blockOut("step %s: render %s: %v", s.ID, s.Prompt, err), nil
		}
		name := s.ID
		if try > 0 {
			name = fmt.Sprintf("%s.try%d", name, try)
		}
		r, err := e.session(st, name, prompt, s.Out, tier, notFamily, fresh && try == 0)
		if err != nil {
			return stepOut{}, err
		}
		reply := harnessReply(r)
		miss := checkReply(reply, s.Out)
		if r["status"] != "ok" {
			// A session that failed or timed out says why on its last line.
			miss = fmt.Sprintf("session %v: %s", r["status"], lastLine(str(r["output"])))
		}
		rec["session"], rec["tier"] = st.Session, tier
		if f := str(r["family"]); f != "" {
			rec["family"] = f
			if st.Env == nil {
				st.Env = map[string]any{}
			}
			st.Env["family"] = f
		}
		if miss == "" {
			st.Retry = nil
			rec["reply"] = merge(zeroReply(s.Out), reply)
			status := reply["status"].(string)
			rec["status"] = status
			if ps, err := processes(reply["processes"]); err == nil && reply["processes"] != nil {
				st.Processes = append(st.Processes, ps...)
			}
			if status == "blocked" {
				return blockOut("step %s: %s%s", s.ID, str(reply["summary"]), questions(reply)), nil
			}
			return stepOut{outcome: status}, nil
		}
		if try >= retry.Max {
			st.Retry = nil
			rec["status"] = "failed"
			if reply != nil {
				rec["reply"] = merge(zeroReply(s.Out), reply)
			}
			return blockOut("step %s: the reply still missed %s after %d tries", s.ID, miss, try+1), nil
		}
		st.Retry = map[string]any{"reason": miss, "n": try + 1}
		if retry.TierUp {
			tier = tierUp(tier)
		}
		if err := e.save(st); err != nil {
			return stepOut{}, err
		}
	}
}

// lastLine is the last line of s that isn't blank.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func questions(reply map[string]any) string {
	qs, _ := reply["questions"].([]any)
	if len(qs) == 0 {
		return ""
	}
	parts := make([]string, len(qs))
	for i, q := range qs {
		parts[i] = str(q)
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// retros joins the retro lines of every prompt step, in step order.
func retros(p *playbook.Playbook, st *State) string {
	var out []string
	for _, s := range p.Steps {
		if r, ok := st.Steps[s.ID]["reply"].(map[string]any); ok {
			if line := strings.TrimSpace(str(r["retro"])); line != "" {
				out = append(out, line)
			}
		}
	}
	return strings.Join(out, "; ")
}

// memories are the prompt replies' memory fields, by step id.
func memories(p *playbook.Playbook, st *State) ([]string, map[string]any) {
	m := map[string]any{}
	var ids []string
	for _, s := range p.Steps {
		if r, ok := st.Steps[s.ID]["reply"].(map[string]any); ok && r["memory"] != nil && r["memory"] != "" {
			m[s.ID] = r["memory"]
			ids = append(ids, s.ID)
		}
	}
	sort.Strings(ids)
	return ids, m
}
