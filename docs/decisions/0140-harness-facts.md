# 0140 Harness facts, verified

- **Context:** The brief's "Facts to verify before relying on them" list. Checked 2026-10-28 on the box: Claude Code 2.1.300 and grok 1.0.67 (Grok Build) are installed, and Codex is not (`which codex`: not found), so Codex comes from web docs only. No model calls were made (0141).
- **Choice:** Record what was verified, with a source key per cell (listed below). Anything not seen in a source or a run is marked UNVERIFIED.
- **Alternatives:** Trust the brief's guesses, or wait for Phase 3's live runs.
- **Why:** Phases 3 and 4 and the installer depend on these flags and paths.

## Harnesses

| Fact | Claude Code | Codex | Grok Build |
|---|---|---|---|
| Project skill folder | `.claude/skills/<n>/SKILL.md` [C-sk] | `.agents/skills/`, scanned in each directory from cwd up to the repo root. Not `.codex/skills` [X-sk] | `.grok/skills/` and `.agents/skills/` at each level from cwd to the repo root, plus `./.claude/skills/` [G-sk]. Probe loaded both `.grok/` and `.agents/` [G-probe] |
| Hidden via `.git/info/exclude` still loads? | UNVERIFIED (docs silent [C-sk]) | UNVERIFIED | Yes. Discovery ignores gitignore rules [G-sk], and the probe loaded both skills while they were excluded [G-probe]. The older `~/.grok/README.md` says the opposite, but the user guide and the probe agree |
| Manual-only skill | Frontmatter `disable-model-invocation: true` [C-sk] | `agents/openai.yaml` with `policy.allow_implicit_invocation: false`. SKILL.md frontmatter has no such key [X-sk] | Frontmatter `disable-model-invocation: true` [G-sk] |
| Naming a skill in a headless prompt | `/skill-name` in the `-p` prompt is expanded [C-hl] | `$skill-name` in the prompt [X-sk]. Works in `codex exec`: UNVERIFIED | `/skill-name` documented for the TUI [G-sk]. Headless: UNVERIFIED (init lists user-invocable skills as slash commands [G-hl]) |
| Headless | `claude -p` [C-help] | `codex exec "<prompt>"`, read-only by default [X-ni] | `grok -p` / `--prompt-file` [G-help] |
| JSON output | `--output-format json\|stream-json`. Result has `result`, `session_id`, `num_turns`, `usage` (top loop only), `modelUsage` (includes subagents), `total_cost_usd`, `structured_output` [C-hl, C-cost] | `--json`: JSONL with `thread.started`, `turn.started`, `turn.completed{usage:{input_tokens,cached_input_tokens,output_tokens,reasoning_output_tokens}}`, `turn.failed`, `item.*`, `error`. No cost field [X-ni] | `--output-format json\|streaming-json\|streaming-messages-json`. `json`: `text`, `stopReason`, `sessionId`, `num_turns`, `usage{input_tokens (uncached),cache_read_…,output_tokens,reasoning_tokens,total_tokens}`, `modelUsage{<model>:{…,costUSD}}`, `total_cost_usd` (left out when cost is partial) [G-hl] |
| Structured output | `--json-schema <schema>` → `structured_output` [C-hl] | `--output-schema <path>`, `-o <file>` [X-ni] | `--json-schema <schema>` [G-help] |
| Extra instructions | `--append-system-prompt[-file]`, `--system-prompt[-file]` [C-cli] | `-c developer_instructions=…`, `-c model_instructions_file=…` (replaces built-ins) [X-cfg] | `--rules <text>` (appends), `--system-prompt-override` [G-help] |
| Skip user config | `--bare` (no CLAUDE.md, hooks, skills, auto memory; API key only), `--setting-sources` [C-cli, C-hl] | `--ignore-user-config`, `--ignore-rules`. Turning off AGENTS.md: UNVERIFIED [X-ni] | Project skills, rules and AGENTS.md load headless only with `--trust` or an earlier grant [G-perm] |
| Permissions | `--permission-mode`, `--allowedTools`/`--disallowedTools` (rule syntax), `--tools`, `--permission-prompts none` [C-help, C-hl] | `-s read-only\|workspace-write\|danger-full-access`, `-a on-request\|never`, `--approve-for-me`. `--full-auto` removed. No allowed-tools list except per-MCP `enabled_tools` [X-dc, X-cfg] | `--permission-mode`, `--allow`/`--deny` rules, `--tools`/`--disallowed-tools`, `--always-approve`, `--sandbox` [G-help] |
| Model | `--model`, `--effort` [C-cli] | `-m`, `-c model_reasoning_effort=` [X-dc] | `-m`, `--reasoning-effort`/`--effort`. Models: grok-4.7 (default), -build-fast, 4.6, 4.5 [G-help, G-models] |
| Budget cap | `--max-budget-usd` (`-p` only, checked against the client-side estimate, includes subagents) [C-cli] | None [X-cfg]. `features.rollout_budget` is under development and off by default | None: "grok has no budget feature" [G-hl] |
| Turn cap | `--max-turns` (`-p` only). Missing from `--help` but present in the binary [C-cli, C-bin] | None [X-cfg] | `--max-turns`, headless only [G-hl] |

Notes for Phase 3: a resumed Claude session reports the whole session's spend since v2.1.277, so take the difference from the previous row, don't add them up [C-cost]. Whether Grok `-s` totals are per-session: UNVERIFIED. From v2.1.302, a Claude `-p` run that sets no permission mode starts in Manual mode, and the box has 2.1.300 [C-hl].

## Backlog.md and Grok Bot

| Fact | Answer | Source |
|---|---|---|
| Backlog.md folder inside `.maestro/` | Yes. Either `backlog_directory` in a root `backlog.config.yml` (that file must be at the project root, so it lands outside `.maestro/`), or make `.maestro/` the Backlog project root: `--cwd .maestro` / `BACKLOG_CWD`. Root discovery walks up from cwd. See 0142 | [B-cfg], [B-root] |
| Backlog.md init without instruction files | `backlog init "<name>" --defaults --integration-mode none` (or `--agent-instructions none`). Untested here | [B-cli] |
| Grok Bot wake: webhook | A routine's webhook: POST with `Authorization: Bearer <key>` and an optional JSON body passed to the Bot. A 200 means the run started. Each routine must first be created through the Bot | [GB-r] |
| Grok Bot wake: other | Schedules (5 minutes apart at least), Slack, GitHub, Linear, Sentry, PagerDuty, email to the Bot, bot-to-bot messages, @-tags on X. Limits: 200 runs/h per routine, 300/h total, 50 routines per Bot | [GB-r], [GB-c] |
| Grok Bot wake: API | No api.x.ai endpoint to message a Bot: UNVERIFIED (none found) | [GB-api] |

Sources: [C-help] `claude --help` · [C-bin] `grep -a -- --max-turns` on the claude binary · [C-cli] https://code.claude.com/docs/en/cli-reference · [C-hl] https://code.claude.com/docs/en/headless · [C-sk] https://code.claude.com/docs/en/skills · [C-cost] https://code.claude.com/docs/en/agent-sdk/cost-tracking · [X-sk] https://learn.chatgpt.com/docs/build-skills · [X-ni] https://learn.chatgpt.com/docs/non-interactive-mode · [X-dc] https://learn.chatgpt.com/docs/developer-commands?surface=cli · [X-cfg] https://learn.chatgpt.com/docs/config-file/config-reference · [G-help] `grok --help`, `grok help` · [G-sk] `~/.grok/docs/user-guide/08-skills.md` · [G-hl] `…/14-headless-mode.md` · [G-perm] `…/22-permissions-and-safety.md:577` · [G-models] `grok models` · [G-probe] `grok --trust inspect --json` in a scratch repo with `.grok/` and `.agents/` in `.git/info/exclude` · [B-cfg] https://github.com/MrLesk/Backlog.md/blob/main/ADVANCED-CONFIG.md · [B-root] `src/utils/find-backlog-root.ts` (npm 1.55.3 source) · [B-cli] `src/cli.ts`, CLI-INSTRUCTIONS.md · [GB-r] https://docs.x.ai/grok-bot/skills-routines-and-automations · [GB-c] https://docs.x.ai/grok-bot/chat-and-collaboration · [GB-api] https://docs.x.ai/developers/rest-api-reference/inference. The docs.x.ai and learn.chatgpt.com pages were read through a summarising fetch, so the facts are right but the wording isn't an exact quote.
