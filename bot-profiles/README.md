# Bot profiles

One folder per role. Grok Bot runs outside this repo and reads a folder to take on that role. Nothing in Maestro loads them.

- `<name>/persona.md`: the bot's instructions. Frontmatter holds `name` (the folder name) and `description`. Give the bot the body as is, then [team-rules.md](team-rules.md).
- `<name>/memory.md`, optional: starting memories. Edit the defaults before you set the bot up; it saves each bullet as one memory.
- Other files in a folder support its persona. Copy the whole folder.
- The folders are six profiles for the five ticket roles: `chief-of-staff` (talks to the owner, leads through the planner), `planner` (runs the team day to day and `/maestro-planner`), `senior-engineer` and `junior-engineer` (both run the `work` playbook as the `engineer` role, on different models), `qa-lead` (runs `/maestro-qa-lead`) and `reviewer` (runs `/maestro-code-review`). For more agents on one profile, run several Grok Bot sessions off one persona; don't add a folder.
- A persona is the bot's context: who it is, how it thinks, what it runs, and links. What a Claude session does when the bot runs a command lives in that command's skill under `skills/`. Link to the skill; never restate it.
- A persona may name Maestro's commands and skills (`/maestro-planner`, `/maestro-create-ticket`, `/maestro-software-engineer`, `/maestro-qa-lead`, `/maestro-code-review`, `/maestro-chief-of-staff`) and the commands a bot runs (`claude -p`, `maestro-play run`, `maestro-ticket`, `gh`). Only playbooks and prompt templates stay off the user's own skills; `skills/use.yaml` names those.
- Write in second person. Link to a team rule; never restate it.
