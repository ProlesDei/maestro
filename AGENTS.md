# AGENTS.md

Instructions for coding agents (Claude Code, Codex, Grok Build). This file is the single source of truth. Other harness files point here; do not copy content into them.

## Project

maestro: an installable, automated agentic workflow that takes products from plan to ship. It packages agent roles, skills, and tools so a user can install it, add project context, and run the workflow. See README.md.

Stack: TBD.

Layout:
- `docs/agent-team.mermaid`: diagram of the agent roles and how work flows between them. README.md embeds a copy; update both together.
- `bot-profiles/`: one folder per bot (`bot-profiles/<name>/`) that outside tools (e.g. Grok) read and copy to create bots. Each folder holds `profile.md` and any supporting files for that persona. Not loaded by this repo. Format in `bot-profiles/README.md`.

## Commands

<!-- Fill in once the stack exists. Agents run these to verify their work. -->
- Install: TBD
- Build: TBD
- Test: TBD
- Lint/format: TBD

## Working rules

- Read the code before changing it. Match the style of the code around it.
- Keep changes scoped to the task. No drive-by refactors.
- Run tests and lint before calling work done. Report failures as they are.
- Never commit secrets. Ask before destructive or outward-facing actions (force push, deleting branches, publishing).
- Commit messages: say what changed and why, in plain words.

## Maintaining this file

- Edit AGENTS.md only. CLAUDE.md imports it.
- Keep it short. Add a rule when an agent gets something wrong twice.
