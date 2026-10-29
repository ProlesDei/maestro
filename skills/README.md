# Skills

Reusable instructions for one task. Every skill lives in this repo at `skills/<name>/SKILL.md`. There are two kinds, and the prefix tells them apart.

## Harness skills (`at-*`)

- Installed into the target repo as part of maestro's footprint, at `.agent-team/skills/<name>/SKILL.md`.
- For harnesses that only discover skills at their standard project path, also installed at `.agents/skills/<name>/SKILL.md`.
- The harness (Claude Code, Codex, Grok Build) loads one only when the bot's prompt names it.
- They do the hands-on work in the repo: ticket CLI, files, git, commands.
- They reply in the structured format from `prompt-contract`.

## Bot skills (no prefix)

- Never installed into the target repo. The bot runtime loads them as part of the bot profile.
- Shared bot skills: `harness-cli`, `prompt-contract`, `model-selection`. Every profile gets them.
- Role-specific bot skills teach one role how to prompt and judge its steps. They sit next to that role's playbooks.
- They teach the bot runtime how to think and prompt. The bot never touches files itself.

## Format

One skill per folder: `skills/<name>/SKILL.md`. Copy `_template/` to start.

- **Frontmatter**: required `name` and `description`.
  - `name` matches the folder name.
  - `description` is one sentence: what the skill does and when to use it. Harnesses read it to decide whether to load the skill, so make it specific.
  - Add other fields only when a reader needs them.
- **Body**: what the skill is for, inputs, steps, the structured reply or output, rules.
- **Other files** in the folder support that skill (examples, reference). Installers copy the whole folder.

## Rules

- Folder names: lowercase, hyphens, matching `name`.
- Use the `at-` prefix if and only if it's a harness skill.
- Folders starting with `_` are not skills. Readers and installers skip them.
- The body must stand alone. Don't point to "see above" or to another skill for a step it needs.
- Never put secrets in a skill: no keys, tokens, passwords or private URLs.
- Harness skills reply in the prompt-contract structured format.

## Current skills

Step names are from the session loop in `docs/bot-anatomy.mermaid`.

| Skill | Kind | Used at |
|---|---|---|
| `at-check-intake` | Harness | Check intake |
| `at-claim` | Harness | Pick the playbook (claim the work) |
| `at-wrap-up` | Harness | Wrap up |
| `at-clean-up` | Harness | Clean up, when the harness has no cleanup hook |
| `harness-cli` | Shared bot | Preflight; Start a harness session; Next step; Clean up (close the session) |
| `prompt-contract` | Shared bot | Next step; Check the structured reply; Result |
| `model-selection` | Shared bot | Next step (pick the model tier) |
