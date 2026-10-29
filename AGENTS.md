# AGENTS.md

Instructions for coding agents (Claude Code, Codex, Grok Build). This file is the single source of truth. Other harness files point here; do not copy content into them.

## Project

maestro: an installable, automated agentic workflow that takes products from plan to ship. It packages agent roles, skills, and tools so a user can install it, add project context, and run the workflow. See README.md.

Stack: Markdown for roles and prompt templates, YAML for playbooks. `tools/` is Go (1.26+, no cgo).

Layout:
- `docs/team.mermaid`: diagram of the agent roles and how work flows between them.
- `docs/bot-anatomy.mermaid`: diagram of how each bot works. README.md embeds copies of both diagrams; update each source and its embed together.
- `bot-profiles/`: one folder per bot (`bot-profiles/<name>/persona.md`) that outside tools (e.g. Grok) read and copy to create bots. Each folder holds the persona and any supporting files for it. `bot-profiles/team-rules.md` holds the rules every bot follows. Not loaded by this repo. Format in `bot-profiles/README.md`.
- `skills/`: the user's own skills (`skills/<name>/SKILL.md`; today Paul's harness skills and commands, decision 0780) and the optional `skills/use.yaml` that names them. No playbook, prompt template or persona may name one. See `skills/README.md`.
- `playbooks/`: one YAML workflow per job (`playbooks/<role>/<job>.yaml`, role names match `bot-profiles/`), the step templates in `playbooks/prompts/`, and `playbooks/rules.md`, the rules every harness session gets in its launch flags. Run by `maestro-play`; never installed into the target repo. Format in `playbooks/README.md`.
- `tools/maestro-play/`: the playbook engine (Go module): runs, resumes, renders and lints playbooks.
- `tools/maestro-ticket/`: the ticket system CLI and store (Go module). Design and guarantees in `tools/maestro-ticket/README.md`.
- `tools/maestro-usage/`: zero-token usage recording, budget checks and breakers (Go module). See its README.
- `docs/runner.md`: the runner contract: how bots reach a repo without touching it.
- `evals/`: one golden task per playbook (`evals/<role>/<job>.md`) and `evals/check.py`, the docs check: golden tasks, the skills slot, skill names, links, repo paths and README's diagram embeds. Format in `evals/README.md`.
- `docs/decisions/`: the decision log. Format in `docs/decisions/README.md`.

One rule, one file: when a rule already lives somewhere, link to it instead of restating it.

## Commands

Docs check, from the repo root: `python3 evals/check.py` (exits 0 when clean). Run it after changing any Markdown or YAML. After changing `evals/check.py`, also run `python3 evals/test_check.py`.

Playbook check: `go test ./...` in `tools/maestro-play/` lints `playbooks/` (or `maestro-play lint playbooks`). Run it after changing a playbook, a prompt template or `skills/use.yaml`.

Run these from `tools/maestro-ticket/`. The same commands work in `tools/maestro-play/` and `tools/maestro-usage/` with the tool's name in place of `maestro-ticket`.

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
