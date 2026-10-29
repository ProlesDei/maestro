# The runner

The runner is a deterministic program and the bots' only way into a target repo. A bot sends it a named request. The runner checks the request against an allowlist for the bot's role and the repo's trust level, runs it, and replies with an exit code and trimmed output. Bots never read files or run commands themselves (decision 0011).

This is the contract. It isn't built yet: `tools/maestro-usage run` (Phase 3) covers the usage recording, and the rest lands with the wake loop and the installer. Bots learn to use it from `skills/bot/runner`.

## Every request

1. **Stop file.** If `$(git rev-parse --git-common-dir)/maestro/STOP` exists, refuse with `status: stopped` and run nothing. This is the kill switch: creating the file pauses every bot at its next step, and deleting it resumes them. Only the user creates or deletes it, by hand or by asking the Chief of Staff (`stop`, `resume`). `resume` is the one request that runs while the file exists.
2. **Allowlist.** Refuse with `status: refused` unless the role and trust level allow the request (table below).
3. **Run** it in the ticket's worktree, or the main checkout when there's no ticket, with a timeout.
4. **Log** one line per request to `$(git rev-parse --git-common-dir)/maestro/logs/runner-<YYYY-MM-DD>.jsonl`: time, bot, role, ticket, request, exit code, duration. Never the output, never a secret.
5. **Reply:**

```yaml
status: ok | failed | refused | stopped
exit_code: 0
output: last 40 lines of stdout and stderr, secrets masked
usage: {tokens: 48210, cost_usd: 0.41}   # session requests only
```

## Requests

| Request | Does | Roles | From trust level |
|---|---|---|---|
| `preflight` | The checks below | every role | 1 |
| `session start`, `session resume` | Launch or resume a headless harness session (below) and return its structured reply | every role | 1 |
| `session close` | Stop the session's processes and forget the session id | every role | 1 |
| `ticket <subcommand>` | `maestro-ticket get`, `list`, `events`, `doctor`; `create`, `comment` and `transition` only with an idempotency key; `comment` and `transition` on a ticket the caller holds only with its own holder and lease token | every role | 1 |
| `usage check [<id>]`, `usage check --all` | `maestro-usage check` for the ticket, workstream and role, or every scope. Exit 1 warns; exit 2 means don't start the step | every role | 1 |
| `trust <id>` | The trust level for the ticket's risk class | every role | 1 |
| `summary [--since <seq>]` | Goal and workstream summaries computed from ticket events, with spend and `next_seq` | chief-of-staff, planner | 1 |
| `worktree add <id> [--branch <b>] [--from <ref>] [--detach <sha>]` | `git worktree add` for the ticket's `maestro/<id>-<slug>` branch, under `$(git rev-parse --git-common-dir)/maestro/worktrees/<id>`, or reuse it. `--detach` makes a checkout of `<sha>` at `<id>-qa` | engineers, qa-lead; operations-manager for `maestro/memory-*` and `maestro/tuning-*` | 1 |
| `check <name>`, `check all` | One command, or every command, from "Automated checks" in `.maestro/project/project-setup.md`, in the ticket's worktree (`--in <id>-qa` for the QA checkout, `--at <sha>` for a throwaway checkout of a commit). Exit code, test counts and an output path per check | engineers, qa-lead, operations-manager | 1 |
| `install detect`, `install status`, `install write <path> --from <file>` | The installer (Phase 7): find the harnesses, compare the manifest with the files, write one footprint file and record it. `write` refuses to overwrite a file, except with `--branch <b>`, which commits it on a new `maestro/` branch for a pull request; `--commit` commits it in the ticket's worktree | chief-of-staff (setup, definition of done), qa-lead (`.maestro/project/verify.md`) | 1 |
| `open pr <id>`, `open pr --branch <b>` | Push the ticket's `maestro/` branch, or a named `maestro/` branch, and open or update its pull request with the handoff note and evidence. Never pushes to the main branch | operations-manager; chief-of-staff only for a branch its own `install write --branch` made | 1 |
| `pr status <id>`, `pr status --branch <b>` | The pull request's state: `open`, `merged` with the merge sha, or `closed` | operations-manager | 1 |
| `merge <id>` | Land the pull request through the merge queue, on the latest main, with the checks re-run | operations-manager | 2 for low-risk classes (docs, tests, chores), 3 for all but high-risk |
| `deploy <env>`, `rollback <env>` | The deploy or rollback command for `<env>` from "Release" in `.maestro/project/project-setup.md` | operations-manager | 4. Migrations, auth and payments still need the user unless they allowed them |
| `smoke <env>` | The read-only smoke check for `<env>` from "Release" | operations-manager | 1 |
| `logs --since <cursor>` | Count log lines newer than the cursor and list their files | operations-manager | 1 |
| `memory scan` | Memory inbox proposals, memories whose files changed, and size against the budget | operations-manager | 1 |
| `housekeep`, `housekeep --apply <plan>` | List, or remove, Maestro's own leftovers from `done` and `cancelled` tickets: worktrees, scratchpads, temp files, and local `maestro/` branches whose pull request merged or closed. Rotate logs; snapshot the store (`maestro-ticket backup`). Never remote branches or anything outside Maestro's paths | operations-manager | 1 |
| `evals <task>... [--with <change>]` | Run golden tasks from `evals/` in scratch copies; pass or fail per task | operations-manager | 1 |
| `stop`, `resume` | Create or delete the stop file, when the user asks | chief-of-staff | 1 |

Nothing else runs. A step that needs something the table doesn't allow is a reason to block and escalate, never to run it some other way.

## Preflight

Before every session, in this order. Any failure means the session doesn't start.

1. Stop file absent.
2. Harness logged in.
   - Claude Code: `claude auth status` prints JSON with `"loggedIn": true`.
   - Grok Build: `grok models` prints `You are not authenticated.` when signed out. Check the output, not the exit code.
   - Codex: `codex login status` (UNVERIFIED, decision 0140).
3. `maestro-ticket doctor` exits `0`.
4. Clean git where the session will work: `git status --porcelain --untracked-files=no` prints nothing.
5. Reply with what the bot needs from the environment: the harnesses, the model ID and family per tier, the trust level per risk class, concurrency limits, budgets left (`usage check`), and the limits and thresholds in `.maestro/project/budgets.json` (retries, step budgets, memory size, recap and stale-work thresholds, how often a pattern must repeat).

## Sessions

The bot sends: role, ticket id, session id, model tier, effort, step budget, the skills to name, and the prompt. The runner:

- reads the model ID for the tier from `.maestro/local/environment.md`,
- passes `.maestro/rules.md` and the role's permissions as launch flags, so the user's own sessions never see them,
- names the skills the harness's way, at the top of the prompt,
- wraps the call in `maestro-usage run`, so every invocation is recorded,
- passes the smallest budget left as a hard cap where the harness has one, and a turn cap otherwise,
- returns the structured reply. A reply that doesn't parse is `status: failed`.

Make a new session UUID per ticket. Holder is `<bot-id>/<session-uuid>`. Pass the same launch flags on every call of one session; only the prompt, model and budget change.

| | Claude Code | Grok Build | Codex (UNVERIFIED in `exec`, decision 0140) |
|---|---|---|---|
| Start | `claude -p "<prompt>" --session-id <uuid> --output-format json` | `grok -p "<prompt>" -s <uuid> --output-format json --cwd <dir>` (`--prompt-file` for long prompts) | `codex exec --json "<prompt>"`; read the session id from the output |
| Resume | `claude -p "<prompt>" -r <uuid>` | `grok -p "<prompt>" -r <uuid>` | `codex exec resume <session-id> "<prompt>"` |
| Rules | `--append-system-prompt-file .maestro/rules.md` | `--rules "<contents of .maestro/rules.md>"` | `-c developer_instructions="<contents>"` |
| Model, effort | `--model`, `--effort` | `-m`, `--reasoning-effort` | `-m`, `-c model_reasoning_effort=` |
| Permissions | `--permission-mode dontAsk`, `--allowedTools`, `--disallowedTools` | `--permission-mode dontAsk`, `--allow`, `--deny` (same rule strings) | `-s workspace-write -a never`, or `-s read-only` for steps that write nothing (it blocks ticket writes too). No per-tool rules, so the rules file carries the limits |
| Reply schema | `--json-schema '<schema>'` | `--json-schema '<schema>'` | `--output-schema <file>` |
| Caps | `--max-budget-usd`, `--max-turns` | `--max-turns` (no budget flag) | none: check `usage check` between steps |
| Naming a skill | `/maestro-claim` in the prompt | the path: `Read and follow .agents/skills/maestro-claim/SKILL.md` | `$maestro-claim` in the prompt |

- Claude Code: `--add-dir <worktree>` when the work is outside the working directory. `--settings` and `--setting-sources` change a session without touching the user's files. Don't use `--no-session-persistence`: the session can't be resumed.
- Grok Build loads project skills, rules and `AGENTS.md` headless only with `--trust`, which writes `~/.grok/trusted_folders.toml`. Never pass `--trust` and never edit that file: name skills by path instead (decision 0143).
- Harness auto-memory is off, or pointed at `.maestro/project/memory/`. The flag or setting for each harness is UNVERIFIED.

## Role permissions

What a session may do, by role. `dontAsk` denies anything not allowed. Deny rules are a backstop in case the user's own settings allow more. Rules use Claude Code syntax; Grok Build's `--allow` and `--deny` take the same strings. `<common>` is `$(git rev-parse --git-common-dir)/maestro`.

| Role | Allow | Deny |
|---|---|---|
| Every role | `Bash(maestro-ticket *)`, `Bash(git rev-parse *)`, `Bash(git status *)`, `Read(.maestro/**)`, Read/Edit/Write on `<common>/scratch/<bot-id>/**`, `<common>/logs/**` and `<common>/memory-inbox/**`; for clean-up `Bash(ps *)`, `Bash(kill *)`, `Bash(git worktree remove *)`, `Bash(git worktree prune)` | `Bash(maestro-ticket heartbeat *)`, `Bash(git push *)`, `Bash(git merge *)`, `Bash(git reset --hard *)`, `Bash(git clean *)`, `Bash(gh pr merge *)`, Edit/Write on `.maestro/rules.md`, `.maestro/local/environment.md` and the stop file |
| chief-of-staff | `Read`, `Grep`, `Glob`, for setup drafts | `Edit`/`Write` elsewhere, `Bash(git commit *)` |
| planner | `Read`, `Grep`, `Glob`, `Bash(git log *)`, `Bash(git diff *)`, `Bash(git show *)` | `Edit`/`Write` elsewhere, `Bash(git commit *)` |
| senior/junior-software-engineer | `Read`, `Grep`, `Glob`, `Edit`/`Write` in the ticket worktree, `Bash(git add *)`, `Bash(git commit *)`, `Bash(git diff *)`, `Bash(git log *)`, the build, lint and test commands from `.maestro/project/project-setup.md`, Write under `<common>/evidence/<ticket-id>/**` | `Bash(git checkout main*)` |
| code-reviewer | `Read`, `Grep`, `Glob`, `Bash(git diff *)`, `Bash(git log *)`, `Bash(git show *)` | `Edit`/`Write` elsewhere, `Bash(git commit *)` |
| qa-lead | `Read`, `Grep`, `Glob`, `Bash(git diff *)`, `Bash(git log *)`, run commands from `.maestro/project/project-setup.md`, Write under `<common>/evidence/<ticket-id>/**` | `Edit` elsewhere, `Bash(git commit *)` |
| operations-manager | `Read`, `Grep`, `Glob`, `Bash(git log *)`, `Bash(git diff *)`, Edit/Write on `.maestro/project/memory/**` in a `maestro/memory-*` worktree and on `.maestro/project/budgets.json` in a `maestro/tuning-*` worktree, `Bash(git add *)`, `Bash(git commit *)` there | `Edit`/`Write` elsewhere |

Pushing, opening pull requests, merging and deploying are runner requests, never session tools, so a session can't go past its trust level.

## Rules

- Never put a secret in a flag or in the rules text. Flags show up in process lists and shell history. Harnesses read credentials from their own login or environment.
- Never use a bypass flag: no `--dangerously-skip-permissions`, `--allow-dangerously-skip-permissions`, `--permission-mode bypassPermissions` or `--always-approve`.
- Never edit the user's `AGENTS.md`, `CLAUDE.md`, `.claude/`, `~/.grok/` or Codex config to make a session work.
- Never render `Backlog.md` into a ticket worktree. `maestro-ticket render` writes it next to the store, at `$(git rev-parse --git-common-dir)/maestro/Backlog.md` (decision 0223); read it there.
