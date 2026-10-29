# The runner

The runner is a deterministic program and the bots' only way into a target repo. A bot sends it a named request. The runner checks the request against an allowlist for the bot's role and the repo's trust level, runs it, and replies with an exit code and trimmed output. Bots never read files or run commands themselves (decision 0011).

This is the contract. It isn't all built yet: `tools/maestro-usage run` (Phase 3) covers the usage recording, `tools/maestro-play` runs playbooks (decision 0700), and the rest lands with the wake loop and the installer. A bot mostly sends one request, `play`: the playbook run sends the others itself and stops only to ask or to block (see Playbook runs).

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
| `play run <role>/<job> [--ticket <id>] [--input <text>] --bot <bot-id>`, `play run <role>[/<job>] --next --bot <bot-id>`, `play resume <run-id> [--choice <c>] [--text <t>]` | Run a playbook on a ticket, or a scheduled one with no ticket; take the next matching ticket; or continue a run that stopped to ask or crashed (Playbook runs, below). `<bot-id>` must be the caller, `<role>` its role and `<job>` a file name in that role's folder; `resume` only takes a run whose state names the same bot. `--bot user` is the human's own session (`/maestro-work`): it may run any role's playbook, and each step is checked against that role's allowlist and trust level, like a bot's. Refused from any process a run started (`MAESTRO_RUN_ID` is set), so a step can never start a run (decision 0787) | every role; `user` for any role | 1 |
| `play render <role>/<job> --step <id> --ticket <id>`, `play lint <dir>` | Print one prompt step as it would be sent, without sending it; check playbooks, templates and `skills/use.yaml` | operations-manager | 1 |
| `ticket <subcommand>` | `maestro-ticket get`, `list` (with `--latest-prefix`, `--not-latest-prefix`), `events`, `doctor`. Read only: every ticket write comes from a playbook run, which runs `create`, `update`, `comment` and `transition` itself with the idempotency key, holder and token. So every note a gate reads came from a playbook's template, never from a bot typing it (decision 0792) | every role | 1 |
| `usage check [<id>]`, `usage check --all` | `maestro-usage check` for the ticket, workstream and role, or every scope. Exit 1 warns; exit 2 means don't start the step | every role | 1 |
| `trust <id>` | The trust level for the ticket's risk class | every role | 1 |
| `head <id>` | The head sha of the ticket's `maestro/` branch, read by the runner, never reported by a session (decision 0751) | every role | 1 |
| `summary [--since <seq>]` | Goal and workstream summaries computed from ticket events, with spend and `next_seq` | chief-of-staff, planner | 1 |
| `worktree add <id> [--branch <b>] [--from <ref>] [--detach <sha>]` | `git worktree add` for the ticket's `maestro/<id>-<slug>` branch, under `$(git rev-parse --git-common-dir)/maestro/worktrees/<id>`, or reuse it. `--detach` makes a checkout of `<sha>` at `<id>-qa`; a reused `<id>-qa` checkout is reset to `<sha>` if it's clean, and refused if it isn't | engineers, qa-lead; operations-manager for `maestro/memory-*` and `maestro/tuning-*` | 1 |
| `check <name>`, `check all` | One command, or every command, from "Automated checks" in `.maestro/project/project-setup.md`, in the ticket's worktree (`--in <id>-qa` for the QA checkout, `--at <sha>` for a throwaway checkout of a commit). Exit code, test counts and an output path per check | engineers, qa-lead, operations-manager | 1 |
| `install detect`, `install status`, `install write <path> --from <file>` | The installer (Phase 7): find the harnesses, compare the manifest with the files, write one footprint file and record it. `write` refuses to overwrite a file, except with `--branch <b>`, which commits it on a new `maestro/` branch for a pull request; `--commit` commits it in the ticket's worktree, and may replace a file that an earlier commit on the same ticket's branch wrote, so a guide that comes back from review can be revised | chief-of-staff (setup, definition of done), qa-lead (`.maestro/project/verify.md`) | 1 |
| `open pr <id>`, `open pr --branch <b>` | Push the ticket's `maestro/` branch, or a named `maestro/` branch, and open or update its pull request with the handoff note and evidence. Never pushes to the main branch | operations-manager; chief-of-staff only for a branch its own `install write --branch` made | 1 |
| `pr status <id>`, `pr status --branch <b>` | The pull request's state: `open`, `merged` with the merge sha, or `closed` | operations-manager | 1 |
| `merge <id>` | Land the pull request through the merge queue, on the latest main, with the checks re-run | operations-manager | 2 for low-risk classes (docs, tests, chores), 3 for all but high-risk |
| `deploy <env>`, `rollback <env>` | The deploy or rollback command for `<env>` from "Release" in `.maestro/project/project-setup.md` | operations-manager | 4. Migrations, auth and payments still need the user unless they allowed them |
| `smoke <env>` | The read-only smoke check for `<env>` from "Release" | operations-manager | 1 |
| `logs --since <cursor>` | Count log lines newer than the cursor, list their files, and list adopted changes due for measuring | operations-manager | 1 |
| `memory scan` | Memory inbox proposals, memories whose files changed, and size against the budget | operations-manager | 1 |
| `housekeep`, `housekeep --apply <plan>` | List, or remove, Maestro's own leftovers from `done` and `cancelled` tickets: worktrees, scratch files, run states, temp files, and local `maestro/` branches whose pull request merged or closed. Rotate logs; snapshot the store (`maestro-ticket backup`). Never remote branches or anything outside Maestro's paths | operations-manager | 1 |
| `evals <task>... [--with <change>]` | Run golden tasks from `evals/` in scratch copies; pass or fail per task | operations-manager | 1 |
| `stop`, `resume` | Create or delete the stop file, when the user asks | chief-of-staff | 1 |

Nothing else runs. A step that needs something the table doesn't allow is a reason to block and escalate, never to run it some other way.

The runner reads its own flags (`--json`, `--bot`, `--role`, `--ticket`, `--run`) only before the request words, so a value inside a request can never set one.

### Reply fields

Besides `status`, `exit_code` and `output`, these requests put their fields in the reply, so a playbook's gates can compare them without a prompt (decision 0751). `tools/maestro-play/playbook/requests.go` holds the same list; keep the two in step.

| Request | Fields |
|---|---|
| `play` | `run` (below) |
| `usage check` | `level`, `scopes` |
| `trust` | `level`, `risk`, `risk_class`; `merge`, `deploy` and `needs_user` (migrations, auth and payments) as booleans for this ticket |
| `head` | `sha` |
| `summary` | `goals`, `workstreams`, `spend`, `next_seq` |
| `worktree add` | `path`, `branch` |
| `check` | `summary`, `output_path`, `checks` |
| `install detect`, `install status`, `install write` | `harnesses`; `files`; `path`, `branch`, `commit` |
| `open pr` | `url`, `number` |
| `pr status` | `state`, `sha`, `merge_sha`, `url` |
| `merge` | `sha`, `merge_sha` |
| `logs` | `count`, `files`, `cursor`, `new_lines`, `due` (adopted changes due for measuring), `next_cursor` |
| `memory scan` | `proposals`, `changed`, `flagged`, `size`, `budget`, `over_budget` |
| `housekeep` | `plan`, `items`, `snapshot_recent`; with `--apply`, `snapshot` |
| `evals` | `results` (each task to `pass` or `fail`), `passed`, `failed` |

## Preflight

Before every session, in this order. Any failure means the session doesn't start.

1. Stop file absent.
2. Harness logged in.
   - Claude Code: `claude auth status` prints JSON with `"loggedIn": true`.
   - Grok Build: `grok models` prints `You are not authenticated.` when signed out. Check the output, not the exit code.
   - Codex: `codex login status` (UNVERIFIED, decision 0140).
3. `maestro-ticket doctor` exits `0`.
4. Clean git where the session will work: `git status --porcelain --untracked-files=no` prints nothing.
5. Reply with what the bot and the playbook run need from the environment: the harnesses, the model ID and family per tier, the trust level per risk class, concurrency limits, budgets left (`usage check`), and the limits and thresholds in `.maestro/project/budgets.json` (retries, step budgets, step visits per run, memory size, recap and stale-work thresholds, how often a pattern must repeat).

## Playbook runs

`play` runs `tools/maestro-play` (decision 0700). A playbook is a YAML file of typed steps; the engine renders each prompt step from a template and sends every other step as a request from the table above. `<common>` is `$(git rev-parse --git-common-dir)/maestro`, and `<maestro>` is the Maestro install the runner runs from.

- **Same checks.** Each step the run sends goes through "Every request" like one a bot sent: the stop file, then the allowlist for the role and trust level (0011, 0403). A refused step blocks the run. `ticket:` steps are the exception in transport only: the engine runs `maestro-ticket` itself, checks the stop file first and allows only the subcommands `tools/maestro-play/playbook/requests.go` lists, writes included, so they reach the store's event log but not the runner log. The stop file stops the run with `state: stopped` and keeps its state, so `play resume` continues after `resume`.
- **Who starts it.** A bot sends `play run`, and so can the wake loop, if decision 0700 question 2 is approved. Either way `--bot` names the bot whose holder, allowlist and inbox the run uses, and the checks are the same: a bot names only itself, the wake loop only the bot it runs for, and a run state's bot is the only one that may resume it. Nothing in a playbook depends on which one started it.
- **Intake.** `--next` picks the first of the role's playbooks whose `intake:` matches, in category order request, sent-back, new-work, then ticket order (priority, then id). Scheduled playbooks, and request playbooks with no status, are never picked by `--next`: the bot runs them by name with `play run <role>/<job>` when they're due, because nothing in the store says a duty is due (decision 0713). With nothing to do the run's state is `idle` and `maestro-play` exits 5; the reply's `status` is still `ok`.
- **Reply.** `exit_code` and `run.state` are `maestro-play`'s, passed through (`tools/maestro-play/README.md`). `status` is `ok` when the run did its job, whatever it ended in: `done`, `blocked`, `waiting` or `idle`. It's `failed` for `failed` and `lost`, and `stopped` for `stopped`, so a quiet queue or an escalation is never a failure (decision 0725).
- **How the engine calls the runner.** `<runner> --json --bot <bot> --role <role> [--ticket <id>] [--run <run-id>] <request words...>`, with `MAESTRO_RUN_ID=<run-id>` in the environment once the run has an id. Sessions are `session start|resume --session <uuid> --tier <tier> --prompt-file <f> --schema-file <f> --rules <maestro>/playbooks/rules.md [--not-family <family>] [--cwd <worktree>]`, and the reply carries the harness's structured reply under `reply` and the model family under `family`. The runner passes `MAESTRO_RUN_ID` to every session it launches, which is how it refuses `play` from inside a run.
- **Run state** lives in `<common>/runs/<run-id>.json`: the claim's holder and token, each step's outcome and reply, and the skips. It replaces the scratchpad's bookkeeping. After a crash, `play resume <run-id>` starts again at the first unfinished step, with the same idempotency keys (`<run-id>:<step>`). A `run:` step that crashed mid-way is sent again, so `merge`, `deploy`, `rollback`, `open pr` and `install write` must be safe to repeat: a merged pull request, a deployed sha or a written file replies `ok` the second time (decision 0793).
- **Ask and block.** An `ask` step pauses the run with `state: waiting` and hands the bot the question, the choices and the evidence paths; the bot answers with `play resume <run-id> --choice <c>`. A run that blocks posts its own `Escalation:` note and releases the ticket; the bot handles it like any escalation. `refused` and `stopped` are stops, never a reason to try another way.

```yaml
status: ok | failed | refused | stopped
exit_code: 6
run:
  id: <run-id>
  playbook: senior-software-engineer/build-ticket
  ticket: T-12
  state: done | blocked | waiting | idle | lost | failed | stopped
  step: verify                        # where it ended or paused
  question: "Which API should it call?" # waiting only
  choices: [v1, v2]                   # waiting only
  evidence: [<common>/evidence/T-12/1-login.txt]
usage: {tokens: 48210, cost_usd: 0.41}  # every session in the run
```

## Sessions

A playbook run sends `session start` and `session resume`; a bot can too, for work outside a playbook. The request carries: role, ticket id, session id, model tier, effort, step budget, and the rendered prompt. The runner:

- reads the model ID for the tier from `.maestro/local/environment.md`,
- passes `playbooks/rules.md` from the Maestro install and the role's permissions as launch flags. The rules are never copied into the repo, so the user's own sessions never see them,
- wraps the call in `maestro-usage run`, so every invocation is recorded,
- passes the smallest budget left as a hard cap where the harness has one, and a turn cap otherwise,
- returns the structured reply. A reply that doesn't parse is `status: failed`.

Make a new session UUID per ticket. Holder is `<bot-id>/<session-uuid>`. Pass the same launch flags on every call of one session; only the prompt, model and budget change.

| | Claude Code | Grok Build | Codex (UNVERIFIED in `exec`, decision 0140) |
|---|---|---|---|
| Start | `claude -p "<prompt>" --session-id <uuid> --output-format json` | `grok -p "<prompt>" -s <uuid> --output-format json --cwd <dir>` (`--prompt-file` for long prompts) | `codex exec --json "<prompt>"`; read the session id from the output |
| Resume | `claude -p "<prompt>" -r <uuid>` | `grok -p "<prompt>" -r <uuid>` | `codex exec resume <session-id> "<prompt>"` |
| Rules | `--append-system-prompt-file <maestro>/playbooks/rules.md` | `--rules "<contents of rules.md>"` | `-c developer_instructions="<contents>"` |
| Model, effort | `--model`, `--effort` | `-m`, `--reasoning-effort` | `-m`, `-c model_reasoning_effort=` |
| Permissions | `--permission-mode dontAsk`, `--allowedTools`, `--disallowedTools` | `--permission-mode dontAsk`, `--allow`, `--deny` (same rule strings) | `-s workspace-write -a never`, or `-s read-only` for steps that write nothing (it blocks ticket writes too). No per-tool rules, so the rules file carries the limits |
| Reply schema | `--json-schema '<schema>'` | `--json-schema '<schema>'` | `--output-schema <file>` |
| Caps | `--max-budget-usd`, `--max-turns` | `--max-turns` (no budget flag) | none: check `usage check` between steps |
| Naming a skill from `skills/use.yaml` | `/<name>` in the prompt | the path: `Read and follow .agents/skills/<name>/SKILL.md` | `$<name>` in the prompt |

- Skills: Maestro ships none, and no playbook, template or request names one. A prompt names a skill only when `skills/use.yaml` lists it under `prompts.all` or `prompts.<template>`; the engine names those, the harness's way, right after `_contract.md`. With no `use.yaml`, or an empty one, a prompt names none. The installer copies only the skills listed under `prompts` and `commands` into each harness's skill folder (0143, 0781); a `commands` skill is the user's to start and is never named in a prompt. `use.yaml` is global, never per project (decision 0701).
- Claude Code: `--add-dir <worktree>` when the work is outside the working directory. `--settings` and `--setting-sources` change a session without touching the user's files. Don't use `--no-session-persistence`: the session can't be resumed.
- Grok Build loads project skills, rules and `AGENTS.md` headless only with `--trust`, which writes `~/.grok/trusted_folders.toml`. Never pass `--trust` and never edit that file: name skills by path instead (decision 0143).
- Harness auto-memory is off, or pointed at `.maestro/project/memory/`. The flag or setting for each harness is UNVERIFIED.

## Role permissions

What a session may do, by role. `dontAsk` denies anything not allowed. Deny rules are a backstop in case the user's own settings allow more. Rules use Claude Code syntax; Grok Build's `--allow` and `--deny` take the same strings. `<common>` is `$(git rev-parse --git-common-dir)/maestro`.

| Role | Allow | Deny |
|---|---|---|
| Every role | `Bash(maestro-ticket get *)`, `Bash(maestro-ticket list *)`, `Bash(maestro-ticket events *)`, `Bash(git rev-parse *)`, `Bash(git status *)`, `Read(.maestro/**)`, Read/Edit/Write on `<common>/scratch/<bot-id>/**`, `<common>/logs/**` and `<common>/memory-inbox/**`; for clean-up `Bash(ps *)`, `Bash(kill *)`, `Bash(git worktree remove *)`, `Bash(git worktree prune)` | every other `maestro-ticket` subcommand (sessions read tickets and never write them; the engine makes every write, decision 0732), `Bash(maestro-play *)`, `Bash(git push *)`, `Bash(git merge *)`, `Bash(git reset --hard *)`, `Bash(git clean *)`, `Bash(gh pr merge *)`, Edit/Write on `.maestro/local/environment.md`, `<common>/runs/**` and the stop file |
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
