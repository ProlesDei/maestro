# AGENTS.md

Instructions for coding agents (Claude Code, Codex, Grok Build). This file is the single source of truth. Other harness files point here; do not copy content into them.

## Project

Maestro: tooling that takes a ticket to a merged PR with coding agents. See README.md and `docs/decisions.md`. Stack: Markdown for skills, personas and prompts; YAML for the playbook; `tools/` is Go (1.26+, no cgo).

## What we're building

A kit a team installs into its own repo so coding agents can take work from a goal to a merged PR, with a person or one bot in charge. It is tooling only: files, a ticket tool, a playbook engine, skills and personas. Nothing runs on its own: no loop, scheduler, dispatcher, watcher or webhook. A person, or a bot such as Grok Bot, starts every job (the owner's ruling, `docs/decisions.md`).

How work moves (drawn in `docs/team.mermaid`):
1. The chief of staff (a bot such as Grok Bot) talks to the owner, one decision at a time, and starts each job with one command, listed in [its persona](bot-profiles/chief-of-staff/persona.md#how-you-drive-claude). It never touches code.
2. The planner turns a goal into tickets in `maestro-ticket`.
3. The engineer runs the work playbook. `maestro-play` takes one ticket to an open PR: worktree, plan, build, the project's own checks, verify, PR. Only prompt steps spend tokens.
4. The QA lead tests the PR in the running app and comments `QA: pass` or `QA: fail` on the ticket.
5. The reviewer, from another model family than the author when one is installed, runs `/maestro-review`. GO merges the PR and closes the ticket; NO-GO sends it back with a `Kickback:` note.
6. A blocked run leaves an `Escalation:` note. The engineer or the chief of staff answers it and runs it again.

What we hold to:
- Least friction for the team that installs it. The step sessions `maestro-play` starts run with all permissions; teams set limits as [tools/maestro-play](tools/maestro-play/README.md) shows.
- The engine, not a prompt, decides what runs next. Put a rule in code, a schema or a lint before you put it in prose.
- Proof over claims. A verdict covers one head sha; a worker's report is a claim until a run shows it.
- Never clash with a team's setup. Every shipped skill is named `maestro-*`, and the installer never overwrites a file and hides its skill copies from git ([decisions](docs/decisions.md)).
- Small and plain: cut what only exists to look impressive. But never cut a skill, bot, role or diagram without the owner's yes. An audit or plan is advice, not a ruling.

## Layout

- `bot-profiles/`: the five personas (`chief-of-staff`, `planner`, `engineer`, `qa-lead`, `reviewer`) and `team-rules.md`. Format in `bot-profiles/README.md`.
- `playbooks/`: `engineer/work.yaml`, its step templates in `playbooks/prompts/`, and `playbooks/rules.md`, the rules every session gets. Format in `playbooks/README.md`.
- `skills/`: Maestro's 18 `maestro-*` skills and the user's own; `skills/use.yaml` names them. List in `skills/README.md`.
- `templates/project-setup.md`: copied to `.maestro/project/project-setup.md` on install.
- `scripts/install.sh`: the installer. `scripts/install_test.sh` is its test.
- `tools/maestro-play/`: the playbook engine. `tools/maestro-ticket/`: the ticket store and CLI.
- `docs/decisions.md`: the rules still in force.
- `docs/team.mermaid`, `docs/bot-anatomy.mermaid`: who does what, and how a bot works. README.md embeds a copy of each; edit the source and its embed together. `docs/design/self-improvement.mermaid`: a future design, not built.

One rule, one file: when a rule already lives somewhere, link to it instead of restating it.

## Commands

From `tools/maestro-play/` or `tools/maestro-ticket/`:
- Test: `go test -race ./...` (`go test ./...` in `tools/maestro-play/` also lints `playbooks/` and `skills/use.yaml`; run it after changing either)
- Lint/format: `gofmt -l . && go vet ./...` (gofmt must print nothing)
- Install: `go install ./cmd/<tool>`

Installer: `bash scripts/install_test.sh` and `shellcheck scripts/*.sh`. Run both after changing `scripts/` or the layout of `skills/`.

## Working rules

- Read the code before changing it. Match the style of the code around it.
- Keep changes scoped to the task. No drive-by refactors.
- Run tests and lint before calling work done. Report failures as they are.
- Never commit secrets. Ask before destructive or outward-facing actions (force push, deleting branches, publishing).
- Commit messages: say what changed and why, in plain words.

## Maintaining this file

- Edit AGENTS.md only. CLAUDE.md imports it.
- Keep it short. Add a rule when an agent gets something wrong twice.
