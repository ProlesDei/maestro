package usage

import (
	"encoding/json"
	"strings"
)

// Harness names.
const (
	HarnessClaude  = "claude"
	HarnessCodex   = "codex"
	HarnessGrok    = "grok"
	HarnessManual  = "manual"
	HarnessUnknown = "unknown"
)

// Harnesses lists the names Parse accepts as a hint.
var Harnesses = []string{HarnessClaude, HarnessCodex, HarnessGrok}

// Parse reads usage from a harness's output. hint names the harness; ""
// detects it from the output. It returns the harness and what it reported.
// Negative counts or cost read as unparsed, since they would cancel out real
// spending.
func Parse(text, hint string) (string, Usage) {
	harness, u := parse(text, hint)
	if u.InputTokens < 0 || u.CachedTokens < 0 || u.OutputTokens < 0 || u.CostUSD != nil && *u.CostUSD < 0 {
		return harness, Empty()
	}
	return harness, u
}

func parse(text, hint string) (string, Usage) {
	text = strings.TrimSpace(text)
	var u Usage
	var ok bool
	switch hint {
	case HarnessClaude:
		u, ok = parseClaude(text)
	case HarnessCodex:
		u, ok = parseCodex(lines(text))
	case HarnessGrok:
		u, ok = parseGrok(text)
	case "":
		return detect(text)
	default:
		return HarnessUnknown, Empty()
	}
	if !ok {
		return hint, Empty()
	}
	return hint, u
}

func detect(text string) (string, Usage) {
	if text == "" {
		return HarnessUnknown, Empty()
	}
	var whole event
	if json.Unmarshal([]byte(text), &whole) == nil {
		if whole.Type == "result" {
			u, _ := parseClaude(text)
			return HarnessClaude, u
		}
		if whole.Type == "" && whole.SessionID != "" {
			u, _ := parseGrok(text)
			return HarnessGrok, u
		}
	}
	if strings.HasPrefix(text, "[") {
		if u, ok := parseClaude(text); ok {
			return HarnessClaude, u
		}
	}
	evs := lines(text)
	for _, e := range evs {
		switch e.Type {
		case "thread.started", "turn.completed", "turn.failed":
			u, _ := parseCodex(evs)
			return HarnessCodex, u
		}
	}
	for _, e := range evs {
		if e.Type == "end" && e.SessionID != "" {
			u, _ := parseGrok(text)
			return HarnessGrok, u
		}
	}
	if u, ok := parseClaude(text); ok {
		return HarnessClaude, u
	}
	return HarnessUnknown, Empty()
}

// event is the part of any JSON line needed to tell formats apart.
type event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"` // Grok
	raw       []byte
}

// lines decodes every line that is a JSON object, skipping the rest.
func lines(text string) []event {
	var out []event
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e event
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		e.raw = []byte(line)
		out = append(out, e)
	}
	return out
}

// tokenUsage is the snake_case usage block Claude Code and Grok share.
type tokenUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

// modelStats is one entry of the camelCase modelUsage map.
type modelStats struct {
	InputTokens              int64   `json:"inputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

func (m modelStats) tokens() int64 {
	return m.InputTokens + m.CacheCreationInputTokens + m.CacheReadInputTokens + m.OutputTokens
}

// spend fills tokens and model from modelUsage when present, since unlike
// usage it includes subagents, and from usage otherwise.
func spend(u *Usage, usage *tokenUsage, models map[string]modelStats) {
	if len(models) > 0 {
		for _, m := range models {
			u.InputTokens += m.InputTokens + m.CacheCreationInputTokens
			u.CachedTokens += m.CacheReadInputTokens
			u.OutputTokens += m.OutputTokens
		}
		// Name the model that cost the most (helper models are usually
		// cheap), then the one that used the most tokens, then by name, so
		// the choice doesn't depend on map order.
		for name, m := range models {
			if u.Model == "" {
				u.Model = name
				continue
			}
			best := models[u.Model]
			if m.CostUSD > best.CostUSD ||
				m.CostUSD == best.CostUSD && (m.tokens() > best.tokens() ||
					m.tokens() == best.tokens() && name < u.Model) {
				u.Model = name
			}
		}
		return
	}
	if usage != nil {
		u.InputTokens = usage.InputTokens + usage.CacheCreationInputTokens
		u.CachedTokens = usage.CacheReadInputTokens
		u.OutputTokens = usage.OutputTokens
	}
}

type claudeResult struct {
	Type         string                `json:"type"`
	SessionID    string                `json:"session_id"`
	DurationMS   *int64                `json:"duration_ms"`
	IsError      bool                  `json:"is_error"`
	TotalCostUSD *float64              `json:"total_cost_usd"`
	Usage        *tokenUsage           `json:"usage"`
	ModelUsage   map[string]modelStats `json:"modelUsage"`
}

// parseClaude reads Claude Code's `--output-format json` object, its
// `--verbose` array, or the last `result` line of `stream-json`.
func parseClaude(text string) (Usage, bool) {
	var r claudeResult
	found := false
	if json.Unmarshal([]byte(text), &r) == nil && r.Type == "result" {
		found = true
	} else if strings.HasPrefix(text, "[") {
		var arr []json.RawMessage
		if json.Unmarshal([]byte(text), &arr) == nil {
			for _, raw := range arr {
				var c claudeResult
				if json.Unmarshal(raw, &c) == nil && c.Type == "result" {
					r, found = c, true
				}
			}
		}
	} else {
		for _, e := range lines(text) {
			var c claudeResult
			if e.Type == "result" && json.Unmarshal(e.raw, &c) == nil {
				r, found = c, true
			}
		}
	}
	if !found {
		return Empty(), false
	}
	u := Usage{SessionID: r.SessionID, DurationMS: r.DurationMS, Status: StatusSuccess}
	if r.IsError {
		u.Status = StatusError
	}
	spend(&u, r.Usage, r.ModelUsage)
	if r.TotalCostUSD != nil {
		u.CostUSD, u.CostSource = r.TotalCostUSD, CostHarness
	}
	return u, true
}

type codexEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Usage    *struct {
		InputTokens       int64 `json:"input_tokens"`
		CachedInputTokens int64 `json:"cached_input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
	} `json:"usage"`
}

// parseCodex reads Codex `exec --json`, summing every turn.completed. Codex
// reports no cost and no model, so pricing needs --model and the price table.
func parseCodex(evs []event) (Usage, bool) {
	u := Empty()
	found, sawTurn := false, false
	for _, e := range evs {
		var c codexEvent
		if json.Unmarshal(e.raw, &c) != nil {
			continue
		}
		switch c.Type {
		case "thread.started":
			found = true
			u.SessionID = c.ThreadID
		case "turn.completed":
			found, sawTurn = true, true
			if c.Usage != nil {
				// Codex counts cached tokens inside input_tokens; store them apart.
				u.InputTokens += max(c.Usage.InputTokens-c.Usage.CachedInputTokens, 0)
				u.CachedTokens += c.Usage.CachedInputTokens
				u.OutputTokens += c.Usage.OutputTokens
			}
		case "turn.failed", "error":
			found = true
			u.Status = StatusError
		}
	}
	if sawTurn && u.Status != StatusError {
		u.Status = StatusSuccess
	}
	return u, found
}

type grokResult struct {
	Type         string                `json:"type"`
	SessionID    string                `json:"sessionId"`
	StopReason   string                `json:"stopReason"`
	TotalCostUSD *float64              `json:"total_cost_usd"`
	Usage        *tokenUsage           `json:"usage"`
	ModelUsage   map[string]modelStats `json:"modelUsage"`
}

// parseGrok reads Grok Build's `--output-format json` object, the `end` or
// `error` line of `streaming-json`, or the Claude-shaped `result` line of
// `streaming-messages-json`.
//
// TODO(phase 3, part 2): verify against a real headless run. This follows
// Grok's bundled docs (user-guide/14-headless-mode.md, grok 1.0.67), not
// captured output. Per those docs input_tokens is uncached only, cost is
// omitted when partial (the price table then applies), and a stop reason
// other than end_turn (max_tokens, max_turn_requests, refusal, cancelled)
// means the step didn't finish, which we count as an error.
func parseGrok(text string) (Usage, bool) {
	var r grokResult
	found := false
	if json.Unmarshal([]byte(text), &r) == nil && (r.Type == "" || r.Type == "error") {
		found = r.Type == "error" || r.SessionID != ""
	} else {
		for _, e := range lines(text) {
			var g grokResult
			if (e.Type == "end" || e.Type == "error") && json.Unmarshal(e.raw, &g) == nil {
				r, found = g, true
			}
		}
	}
	if !found {
		return parseClaude(text)
	}
	u := Usage{SessionID: r.SessionID, Status: StatusSuccess}
	if r.Type == "error" || r.StopReason != "end_turn" {
		u.Status = StatusError
	}
	spend(&u, r.Usage, r.ModelUsage)
	if r.TotalCostUSD != nil {
		u.CostUSD, u.CostSource = r.TotalCostUSD, CostHarness
	}
	return u, true
}
