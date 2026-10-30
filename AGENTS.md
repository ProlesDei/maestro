# AGENTS.md

Instructions for coding agents (Claude Code, Codex, Grok Build). This file is the single source of truth. Other harness files point here; do not copy content into them.

## Project

Maestro: tooling that takes a ticket to a merged PR with coding agents. Nothing in it runs on its own. See README.md and `docs/decisions.md`. Stack: Markdown for skills, personas and prompts; YAML for the playbook; `tools/` is Go (1.26+, no cgo).

Layout:
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
