//go:build unix

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const streamMarker = "STREAM-MARKER-41c7"

// streamHead is a stream's lines before its result, with a marker in
// every field that holds text: hooks, the init line's cwd, MCP servers,
// slash commands, skills and plugins, tool input and output, a team's
// own skill and file names, and the model's text.
func streamHead() string {
	m := streamMarker
	lines := []string{
		`{"type":"system","subtype":"hook_started","hook_id":"h","hook_name":"SessionStart:` + m + `","hook_event":"SessionStart","uuid":"u","session_id":"s"}`,
		`{"type":"system","subtype":"hook_response","hook_id":"h","hook_name":"` + m + `","output":"` + m + `","stdout":"` + m + `","stderr":"` + m + `","exit_code":0,"outcome":"success","uuid":"u","session_id":"s"}`,
		`{"type":"system","subtype":"init","cwd":"/home/` + m + `","session_id":"11111111-2222-4333-8444-555555555555","tools":["Bash"],"mcp_servers":[{"name":"` + m + `","status":"connected"}],"model":"claude-sonnet-5-5","slash_commands":["` + m + `"],"claude_code_version":"2.1.322","skills":["` + m + `"],"plugins":[{"name":"` + m + `","path":"/` + m + `"}],"scratchpad_path":"/tmp/` + m + `"}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"},"uuid":"u","session_id":"s"}`,
		streamUse("m1", "2026-11-16T01:00:00.000Z", `{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"cd /w/`+m+` && maestro-ticket pickup","description":"`+m+`"}}`),
		streamResult("t1", "2026-11-16T01:00:00.400Z", false, m),
		streamUse("m2", "2026-11-16T01:00:01.000Z", `{"type":"tool_use","id":"t2","name":"Skill","input":{"skill":"`+m+`"}}`),
		streamResult("t2", "2026-11-16T01:00:01.100Z", true, m),
		streamUse("m3", "2026-11-16T01:00:02.000Z", `{"type":"tool_use","id":"t3","name":"Read","input":{"file_path":"/w/.claude/skills/maestro-`+m+`/SKILL.md"}}`),
		streamResult("t3", "2026-11-16T01:00:02.100Z", false, m),
		streamUse("m4", "2026-11-16T01:00:03.000Z", `{"type":"tool_use","id":"t4","name":"mcp__`+m+`__q","input":{"q":"`+m+`"}}`),
		streamResult("t4", "2026-11-16T01:00:03.300Z", false, m),
		streamUse("m5", "2026-11-16T01:00:04.000Z", `{"type":"tool_use","id":"t5","name":"Read","input":{"file_path":"/w/.claude/skills/maestro-code-review/references/passes.md"}}`),
		streamResult("t5", "2026-11-16T01:00:04.050Z", false, m),
		streamUse("m6", "2026-11-16T01:00:05.000Z", `{"type":"text","text":"`+m+`"}`),
	}
	return strings.Join(lines, "\n") + "\n"
}

func streamUse(id, at, block string) string {
	return `{"type":"assistant","message":{"model":"claude-sonnet-5-5","id":"` + id + `","type":"message","role":"assistant","content":[` + block +
		`],"usage":{"input_tokens":2,"cache_creation_input_tokens":10,"cache_read_input_tokens":1000,"output_tokens":5}},"parent_tool_use_id":null,"session_id":"s","uuid":"u","timestamp":"` + at + `"}`
}

func streamResult(id, at string, isErr bool, text string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []map[string]any{
		{"type": "tool_result", "content": text, "is_error": isErr, "tool_use_id": id}}},
		"parent_tool_use_id": nil, "session_id": "s", "uuid": "u", "timestamp": at, "tool_use_result": text})
	return string(b)
}

// streamRow is run id's stream facts in stats.db.
func streamRow(t *testing.T, dir, id string) (stream string, calls, fails int, tail, open, claude string) {
	t.Helper()
	statsQuery(t, dir, `SELECT coalesce(s.name, '-'), coalesce(f.tool_calls, -1), coalesce(f.tool_fails, -1), coalesce(f.tail, '-'),
		coalesce(o.name, '-'), coalesce(f.v_claude, '-')
		FROM run_facts f LEFT JOIN codes s ON s.id = f.stream LEFT JOIN codes o ON o.id = f.open_code WHERE f.run = '`+id+`'`,
		&stream, &calls, &fails, &tail, &open, &claude)
	return
}

// A supervisor killed after Claude printed its result but before it read
// the stream: pickup reads the stream as it would have, so the run's
// cost is logged and its row has the counts, as json's .out would have
// let them be.
func TestOrphanReadsStream(t *testing.T) {
	h, id := orphanScene(t)
	prepareStatsAt(t, h.dir)
	dir := filepath.Join(h.dir, "runs")
	writeSide(dir, id, &sideFacts{Stream: true})
	res := `{"duration_api_ms":5,"session_id":"11111111-2222-4333-8444-555555555555","total_cost_usd":2.5,"modelUsage":{"claude-sonnet-5-5":{"inputTokens":3,"outputTokens":4,"costUSD":2.5}},"result":"job: done","subtype":"success","is_error":false,"num_turns":2,"type":"result"}`
	os.WriteFile(filepath.Join(dir, id+".out"), nil, 0o600)
	os.WriteFile(filepath.Join(dir, id+".stream"), []byte(streamHead()+res+"\n"), 0o600)
	h.run("", "pickup")
	if b, _ := os.ReadFile(filepath.Join(dir, id+".out")); string(b) != res+"\n" {
		t.Fatalf(".out %q", b)
	}
	if _, err := os.Lstat(filepath.Join(dir, id+".stream")); err == nil {
		t.Fatal("stream file left")
	}
	if _, out, _ := h.run("", "costs"); !strings.Contains(out, "2.50") {
		t.Fatalf("the result's cost wasn't logged:\n%s", out)
	}
	if stream, calls, _, _, _, _ := streamRow(t, h.dir, id); stream != "ok" || calls != 5 {
		t.Fatalf("row: %s, %d calls", stream, calls)
	}
	// With its stream file gone, its counts are lost.
	h, id = orphanScene(t)
	prepareStatsAt(t, h.dir)
	writeSide(filepath.Join(h.dir, "runs"), id, &sideFacts{Stream: true})
	h.run("", "pickup")
	if stream, calls, _, _, _, _ := streamRow(t, h.dir, id); stream != "lost" || calls != -1 {
		t.Fatalf("row with no stream file: %s, %d calls", stream, calls)
	}
}
