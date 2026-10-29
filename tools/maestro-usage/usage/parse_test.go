package usage

import (
	"os"
	"path/filepath"
	"testing"
)

func fptr(f float64) *float64 { return &f }
func iptr(i int64) *int64     { return &i }

func TestParseFixtures(t *testing.T) {
	tests := []struct {
		file, hint, harness string
		want                Usage
	}{
		{"claude-json.json", "", HarnessClaude, Usage{SessionID: "6f1c2b9e-1111-4c3e-9a7a-0e5b3c2d1a00",
			Model: "claude-opus-5-5", InputTokens: 4112, CachedTokens: 38000, OutputTokens: 950,
			CostUSD: fptr(0.1834), CostSource: CostHarness, Status: StatusSuccess, DurationMS: iptr(15234)}},
		// modelUsage includes the subagent's tokens; usage doesn't.
		{"claude-subagents.json", "", HarnessClaude, Usage{SessionID: "sess-subagents",
			Model: "claude-opus-5-5", InputTokens: 5010, CachedTokens: 21000, OutputTokens: 900,
			CostUSD: fptr(0.154), CostSource: CostHarness, Status: StatusSuccess, DurationMS: iptr(61000)}},
		{"claude-usage-only.json", "", HarnessClaude, Usage{SessionID: "sess-old",
			InputTokens: 100, CachedTokens: 200, OutputTokens: 50, Status: StatusSuccess, DurationMS: iptr(800)}},
		{"claude-error.json", "", HarnessClaude, Usage{SessionID: "sess-err", Model: "claude-sonnet-5-5",
			InputTokens: 40, OutputTokens: 60, CostUSD: fptr(0.02), CostSource: CostHarness,
			Status: StatusError, DurationMS: iptr(30000)}},
		{"claude-stream.jsonl", "", HarnessClaude, Usage{SessionID: "sess-stream", Model: "claude-sonnet-5-5",
			InputTokens: 5, CachedTokens: 100, OutputTokens: 2, CostUSD: fptr(0.003), CostSource: CostHarness,
			Status: StatusSuccess, DurationMS: iptr(900)}},
		{"claude-verbose.json", "", HarnessClaude, Usage{SessionID: "sess-verbose", Model: "claude-sonnet-5-5",
			InputTokens: 307, OutputTokens: 20, CostUSD: fptr(0.01), CostSource: CostHarness,
			Status: StatusSuccess, DurationMS: iptr(1200)}},
		// Every turn.completed is summed, with cached tokens taken out of input.
		{"codex.jsonl", "", HarnessCodex, Usage{SessionID: "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b",
			InputTokens: 1315, CachedTokens: 24448, OutputTokens: 152, Status: StatusSuccess}},
		{"codex.jsonl", HarnessCodex, HarnessCodex, Usage{SessionID: "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b",
			InputTokens: 1315, CachedTokens: 24448, OutputTokens: 152, Status: StatusSuccess}},
		{"codex-failed.jsonl", "", HarnessCodex, Usage{SessionID: "thread-fail", Status: StatusError}},
		{"grok-json.json", "", HarnessGrok, Usage{SessionID: "abc123", Model: "grok-4.6",
			InputTokens: 7210, CachedTokens: 41000, OutputTokens: 1893, CostUSD: fptr(0.01268905),
			CostSource: CostHarness, Status: StatusSuccess}},
		// Partial cost is omitted, so no cost; the busiest model is named.
		{"grok-partial.json", "", HarnessGrok, Usage{SessionID: "g-partial", Model: "grok-4.6",
			InputTokens: 500, CachedTokens: 9000, OutputTokens: 300, Status: StatusError}},
		{"grok-streaming.jsonl", "", HarnessGrok, Usage{SessionID: "g-stream", Model: "grok-4.6",
			InputTokens: 812, CachedTokens: 3000, OutputTokens: 45, CostUSD: fptr(0.0021),
			CostSource: CostHarness, Status: StatusSuccess}},
		{"grok-error.json", HarnessGrok, HarnessGrok, Usage{Status: StatusError}},
		{"grok-error.json", "", HarnessUnknown, Usage{Status: StatusUnparsed}},
		// streaming-messages-json is Claude-shaped: the hint names the harness.
		{"grok-messages.jsonl", HarnessGrok, HarnessGrok, Usage{SessionID: "g-msgs", InputTokens: 812,
			OutputTokens: 210, CostUSD: fptr(0.0127), CostSource: CostHarness, Status: StatusSuccess, DurationMS: iptr(0)}},
		{"grok-messages.jsonl", "", HarnessClaude, Usage{SessionID: "g-msgs", InputTokens: 812,
			OutputTokens: 210, CostUSD: fptr(0.0127), CostSource: CostHarness, Status: StatusSuccess, DurationMS: iptr(0)}},
		{"plain.txt", "", HarnessUnknown, Usage{Status: StatusUnparsed}},
		{"plain.txt", HarnessClaude, HarnessClaude, Usage{Status: StatusUnparsed}},
		{"codex.jsonl", HarnessClaude, HarnessClaude, Usage{Status: StatusUnparsed}},
	}
	for _, tc := range tests {
		t.Run(tc.file+"/"+tc.hint, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			harness, got := Parse(string(data), tc.hint)
			if harness != tc.harness {
				t.Errorf("harness = %q, want %q", harness, tc.harness)
			}
			assertUsage(t, got, tc.want)
		})
	}
}

func TestParseEdgeCases(t *testing.T) {
	tests := []struct {
		name, text, harness string
		want                Usage
	}{
		{"empty", "", HarnessUnknown, Usage{Status: StatusUnparsed}},
		{"whitespace", " \n\t\n", HarnessUnknown, Usage{Status: StatusUnparsed}},
		{"other json", `{"hello":"world"}`, HarnessUnknown, Usage{Status: StatusUnparsed}},
		{"truncated", `{"type":"result","session_id":"x"`, HarnessUnknown, Usage{Status: StatusUnparsed}},
		{"codex thread only", `{"type":"thread.started","thread_id":"t"}`, HarnessCodex,
			Usage{SessionID: "t", Status: StatusUnparsed}},
		{"codex negative input", `{"type":"turn.completed","usage":{"input_tokens":5,"cached_input_tokens":9,"output_tokens":1}}`,
			HarnessCodex, Usage{CachedTokens: 9, OutputTokens: 1, Status: StatusSuccess}},
		{"modelUsage tie picks by name", `{"type":"result","session_id":"s","modelUsage":{
			"b":{"inputTokens":1,"costUSD":0.5},"a":{"inputTokens":1,"costUSD":0.5}}}`,
			HarnessClaude, Usage{SessionID: "s", Model: "a", InputTokens: 2, Status: StatusSuccess}},
		{"negative cost", `{"type":"result","session_id":"s","total_cost_usd":-1000}`,
			HarnessClaude, Usage{Status: StatusUnparsed}},
		{"negative tokens", `{"type":"result","session_id":"s","usage":{"output_tokens":-5}}`,
			HarnessClaude, Usage{Status: StatusUnparsed}},
		{"last result wins", "{\"type\":\"result\",\"session_id\":\"one\"}\n{\"type\":\"result\",\"session_id\":\"two\",\"is_error\":true}",
			HarnessClaude, Usage{SessionID: "two", Status: StatusError}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			harness, got := Parse(tc.text, "")
			if harness != tc.harness {
				t.Errorf("harness = %q, want %q", harness, tc.harness)
			}
			assertUsage(t, got, tc.want)
		})
	}
}

func assertUsage(t *testing.T, got, want Usage) {
	t.Helper()
	if got.SessionID != want.SessionID || got.Model != want.Model ||
		got.InputTokens != want.InputTokens || got.CachedTokens != want.CachedTokens ||
		got.OutputTokens != want.OutputTokens || got.CostSource != want.CostSource ||
		got.Status != want.Status {
		t.Errorf("usage =\n %+v\nwant\n %+v", got, want)
	}
	if (got.CostUSD == nil) != (want.CostUSD == nil) || got.CostUSD != nil && *got.CostUSD != *want.CostUSD {
		t.Errorf("cost = %v, want %v", deref(got.CostUSD), deref(want.CostUSD))
	}
	if (got.DurationMS == nil) != (want.DurationMS == nil) || got.DurationMS != nil && *got.DurationMS != *want.DurationMS {
		t.Errorf("duration = %v, want %v", got.DurationMS, want.DurationMS)
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
