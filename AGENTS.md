# AGENTS.md

Instructions for coding agents (Claude Code, Codex, Grok Build). This file is the single source of truth. Other harness files point here; do not copy content into them.

## Project

maestro: an installable, automated agentic workflow that takes products from plan to ship. It packages agent roles, skills, and tools so a user can install it, add project context, and run the workflow. See README.md.

Stack: Markdown for roles, skills and playbooks. `tools/maestro-ticket/` is Go (1.26+, no cgo).

Layout:
- `docs/team.mermaid`: diagram of the agent roles and how work flows between them.
- `docs/bot-anatomy.mermaid`: diagram of how each bot works. README.md embeds copies of both diagrams; update each source and its embed together.
- `bot-profiles/`: one folder per bot (`bot-profiles/<name>/persona.md`) that outside tools (e.g. Grok) read and copy to create bots. Each folder holds the persona and any supporting files for it. Not loaded by this repo. Format in `bot-profiles/README.md`.
- `skills/`: one folder per skill (`skills/<name>/SKILL.md`). `maestro-*` skills are harness skills, installed into the target repo under `.maestro/`; the rest are bot skills loaded by the bot runtime. Format in `skills/README.md`.
- `playbooks/`: one folder per role (`playbooks/<role>/<job>.md`, role names match `bot-profiles/`). The bot runtime loads them; never installed into the target repo. Format in `playbooks/README.md`.
- `tools/maestro-ticket/`: the ticket system CLI and store (Go module). Design and guarantees in `tools/maestro-ticket/README.md`.
- `tools/maestro-usage/`: zero-token usage recording, budget checks and breakers (Go module). See its README.

## Commands

<!-- Agents run these to verify their work. Run from tools/maestro-ticket/. -->
- Install: `go install ./cmd/maestro-ticket`
- Build: `go build -o bin/maestro-ticket ./cmd/maestro-ticket`
- Test: `go test -race ./...` (`-short` skips the 200-process race test)
- Lint/format: `gofmt -l . && go vet ./...` (gofmt must print nothing)

## Working rules

- Read the code before changing it. Match the style of the code around it.
- Keep changes scoped to the task. No drive-by refactors.
- Run tests and lint before calling work done. Report failures as they are.
- Never commit secrets. Ask before destructive or outward-facing actions (force push, deleting branches, publishing).
- Commit messages: say what changed and why, in plain words.

## Maintaining this file

- Edit AGENTS.md only. CLAUDE.md imports it.
- Keep it short. Add a rule when an agent gets something wrong twice.
