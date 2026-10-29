# 0121 Grok Build parser written from its bundled docs

- **Context:** The brief lists Grok Build's JSON output under "facts to verify". A real headless run spends tokens, so this slice didn't make one. Grok 1.0.67 on the box ships `~/.grok/docs/user-guide/14-headless-mode.md`, which documents `json`, `streaming-json` and `streaming-messages-json` with spend fields.
- **Choice:** Implement the parser from those docs instead of a stub, with a TODO in `parseGrok` and fixtures copied from the doc examples. Prefer `modelUsage` as for Claude. Treat any stop reason other than `end_turn` (`max_tokens`, `max_turn_requests`, `refusal`, `cancelled`) as an error. `streaming-messages-json` is Claude-shaped, so it's only labelled Grok with `--harness grok`.
- **Alternatives:** A stub that records `unparsed` (every Grok call would trip the breaker); run Grok now (spends tokens, outside this slice).
- **Why:** The docs are specific (uncached `input_tokens`, cost omitted when partial), and the shapes are already covered by tests. Part 2 must confirm them with a real run before relying on Grok costs.
