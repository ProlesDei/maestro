---
name: harness-cli
description: How to health-check, start, resume and close a headless harness session (Claude Code, Grok Build, Codex) with launch flags and role permissions; use it at preflight and for every prompt you send.
---

# Harness CLI

You drive the harness only through its command line. Everything a session needs (team rules, role permissions, model, budget) goes in launch flags. The user's config files stay untouched: never edit their settings, rules or skills to make a session work.

## Preflight

Stop if the kill switch in `.agent-team/environment.md` is set. Then all of these must pass, or the session doesn't start:

- Harness logged in.
  - Claude Code: `claude auth status` prints JSON with `"loggedIn": true`.
  - Grok Build: `grok models` prints `You are not authenticated.` when signed out. Check the output, not the exit code.
  - Codex: `codex login status` (unverified; check `codex --help`).
- Ticket CLI works: `at-ticket doctor` exits `0`.
- Git clean: `git status --porcelain` prints nothing.

## Start, resume, close

Make a new UUID per ticket. Holder is `<bot-id>/<that-uuid>`. Pass the same launch flags on every call; only the prompt, model and budget change between steps.

**Claude Code**

```sh
claude -p "<prompt>" --session-id <uuid> --output-format json \
  --model <model> --append-system-prompt "<team rules from .agent-team/rules.md>" \
  --permission-mode dontAsk --allowedTools <allow rules> --disallowedTools <deny rules> \
  --max-budget-usd <step budget> --json-schema '<reply schema>'
claude -p "<next prompt>" -r <uuid> <same flags>
```

- `--add-dir <dir>` when the work is outside the working directory (for example a worktree).
- `--settings <file-or-json>` for session-only settings; `--setting-sources <sources>` to choose which of their settings load. Neither changes their files.
- `--effort <level>` and `--fallback-model <model>` per `model-selection`.
- `--output-format stream-json` if you watch progress live.
- Don't use `--no-session-persistence` for playbooks: the session can't be resumed.

**Grok Build**

```sh
grok -p "<prompt>" -s <uuid> --output-format json -m <model> \
  --rules "<team rules from .agent-team/rules.md>" --permission-mode dontAsk \
  --allow <rule> --deny <rule> --max-turns <n> --json-schema '<reply schema>' --cwd <repo>
grok -p "<next prompt>" -r <uuid> <same flags>
```

- `--prompt-file <path>` for long prompts. `--tools` / `--disallowed-tools` to limit built-in tools. `--sandbox <profile>` to limit filesystem and network. `--add-dir`, `--reasoning-effort` as needed.
- `--output-format streaming-json` or `streaming-messages-json` to watch progress.

**Codex** (unverified: the Codex CLI isn't installed where this was written; confirm with `codex exec --help`)

```sh
codex exec --json --model <model> --sandbox workspace-write "<prompt>"
codex exec resume <session-id> "<next prompt>"
```

- Read the session id from the `--json` output. For team rules and tool allow/deny rules, check `codex exec --help`; if there's no flag, point to `.agent-team/rules.md` at the top of the first prompt.
- `--sandbox read-only` blocks ticket writes, so only use it for steps that write nothing.

**Close:** a headless call exits when its prompt is done. After clean-up, send no more prompts to that session id.

## Role permissions

Use `--permission-mode dontAsk` so anything not allowed is denied without asking. Deny rules are a backstop in case their own settings allow more. Rules below use Claude Code syntax; Grok Build's `--allow` / `--deny` accept the same strings (they alias `--allowedTools` / `--disallowedTools`). Write paths in the harness's own path syntax. `<scratch>` and `<logs>` are under `$(git rev-parse --git-common-dir)/agent-team/`.

| Role | Diagram | Allow | Deny |
|---|---|---|---|
| Every role | | `Bash(at-ticket *)`, `Bash(git rev-parse *)`, `Bash(git status *)`, `Read(.agent-team/**)`, Read/Edit/Write on `<scratch>/<bot-id>/**` and `<logs>/**`; for clean-up `Bash(ps *)`, `Bash(kill *)`, `Bash(git worktree remove *)`, `Bash(git worktree prune)` | `Bash(git push --force *)`, `Bash(git push -f *)`, `Bash(git reset --hard *)`, `Bash(git clean *)` |
| chief-of-staff | Read summaries · write its own tickets | nothing more | `Edit`/`Write` elsewhere, `Bash(git commit *)`, `Bash(git push *)`, `Bash(git merge *)` |
| planner | Full ticket system · read-only code | `Read`, `Grep`, `Glob`, `Bash(git log *)`, `Bash(git diff *)`, `Bash(git show *)` | `Bash(git commit *)`, `Bash(git push *)`, `Bash(git merge *)` |
| senior/junior-software-engineer | Edit code on at/ branches | `Read`, `Grep`, `Glob`, `Edit`/`Write` in the ticket worktree, `Bash(git worktree add *)`, `Bash(git add *)`, `Bash(git commit *)`, `Bash(git diff *)`, `Bash(git log *)`, the build and test commands from `.agent-team/project-setup.md` | `Bash(git push *)`, `Bash(git merge *)`, `Bash(git checkout main*)` |
| code-reviewer | Read-only code and diffs | `Read`, `Grep`, `Glob`, `Bash(git diff *)`, `Bash(git log *)`, `Bash(git show *)` | `Bash(git commit *)`, `Bash(git push *)`, `Bash(git merge *)` |
| qa-lead | Run tests and the app · read-only code | `Read`, `Grep`, `Glob`, `Bash(git diff *)`, `Bash(git log *)`, `Bash(git worktree add --detach *)`, test and run commands from `.agent-team/project-setup.md` | `Bash(git commit *)`, `Bash(git push *)`, `Bash(git merge *)` |
| operations-manager | Merge · deploy · run the improvement loop | `Read`, `Grep`, `Glob`, `Bash(git log *)`, `Bash(git diff *)`, `Bash(git checkout main)`, `Bash(git merge *)`, `Bash(git push *)`, deploy commands from `.agent-team/project-setup.md` | `Edit`/`Write` elsewhere |

## Rules

- Never pass secrets on the command line: not in prompts, flags or rules. Flags show up in process lists and shell history. Let the harness read credentials from its own login or environment.
- Never use bypass or skip-permission flags: no `--dangerously-skip-permissions`, `--allow-dangerously-skip-permissions`, `--permission-mode bypassPermissions` or `--always-approve`. If a step can't run without one, set Blocked.
- Never edit their config files (`.claude/`, `~/.grok/`, Codex config, `AGENTS.md`, `CLAUDE.md`) to change a session.
- One session per ticket. Don't resume a session id that belonged to another ticket.
