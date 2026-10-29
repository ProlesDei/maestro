# maestro-usage

Records what every harness call spent, checks budgets and trips circuit breakers. Every check is arithmetic over a local SQLite file, so it costs no tokens. Bots call it as a gate and act on the exit code. Go port of `docs/design/maestro-usage.py`.

## Commands

```
maestro-usage run --ticket T-12 --workstream auth --role junior-engineer -- claude -p "..." --output-format json
maestro-usage run --ticket T-12 --role senior-engineer --model gpt-5.5 -- codex exec --json "..."
maestro-usage record --ticket T-12 < output.json        # parse harness output from stdin
maestro-usage record --manual --role chief-of-staff --input-tokens 900 --cost-usd 0.01
maestro-usage check --ticket T-12 --workstream auth --role junior-engineer
maestro-usage breaker --role junior-engineer
maestro-usage report --by workstream --since 24h [--json]
```

- `run` streams the harness's stdout through unchanged, passes stderr and stdin through, forwards Ctrl-C, and exits with the harness's code. It records **one row per invocation**, never per session, since resumed sessions reuse their ID. `--record-key` makes a retried step replace its row.
- `record` keys a row on a hash of the output, so piping the same output twice counts once.
- `check` prints `usd_remaining`, the smallest dollar budget left, to pass on as a hard cap (Claude Code's `--max-budget-usd`).
- `--harness claude|codex|grok` picks the parser; by default it's detected from the output.

Exit codes: `check` 0 ok, 1 warn, 2 over. `breaker` 0 ok, 2 tripped. `run` exits with the harness's code. 64 is bad usage or config and 70 an internal error, so a typo never reads as "over budget".

## Parsers

| Harness | Output | Notes |
|---|---|---|
| Claude Code | `--output-format json`, its `--verbose` array, or `stream-json` | Prefers `modelUsage`, which includes subagents. Cost from `total_cost_usd`. |
| Codex | `exec --json` | Sums every `turn.completed`; `input_tokens` includes cached, so they're split out. No cost or model: pass `--model` and add it to the price table. |
| Grok Build | `json`, `streaming-json`, `streaming-messages-json` (use `--harness grok`) | Written from Grok's bundled docs (1.0.67), **not yet checked against a real run**. Any stop reason but `end_turn` counts as an error. |

Statuses are `success`, `error` and `unparsed` (no usage found). The breaker counts the last two as failures.

## Storage and config

- DB: `--db`, else `$MAESTRO_USAGE_DB`, else `$(git rev-parse --git-common-dir)/maestro/usage.db`, shared by every worktree. Outside git, `.maestro/local/usage.db`. SQLite in WAL mode, `BEGIN IMMEDIATE` writes, behind `usage.Store`.
- Budgets: `--budgets`, else `$MAESTRO_BUDGETS`, else `.maestro/project/budgets.json` at the repo root. A missing project file means no limits; a missing explicit path, an unknown field, a bad limit key or a negative limit or price is an error.

```json
{
  "warn_at": 0.8,
  "prices_per_million_tokens": {"gpt-5.5": {"input": 2.0, "cached_input": 0.5, "output": 10.0}},
  "global": {"usd_24h": 50, "tokens_24h": 50000000},
  "workstreams": {"auth": {"usd_total": 20}},
  "tickets_default": {"usd_total": 2},
  "tickets": {"T-12": {"usd_total": 5}},
  "roles": {"junior-engineer": {"usd_24h": 10}},
  "breaker": {"window": 20, "min_samples": 5, "max_failure_rate": 0.5}
}
```

Limit keys are `usd_<period>` or `tokens_<period>`, where the period is `total` or a window such as `30m`, `24h` or `7d`. A limit of 0 means no limit and sets no `usd_remaining` cap. The breaker trips when, of a role's newest `window` calls, at least `min_samples` exist and more than `max_failure_rate` failed.

## Develop

Run from this folder: `go test -race ./...`, `go vet ./...`, `gofmt -l .` (must print nothing).
