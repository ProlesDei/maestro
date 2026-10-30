# Bot profiles

One folder per bot, for tools outside this repo (such as Grok Bot) to read and copy. Nothing in Maestro loads them.

- `<name>/persona.md`: the bot's instructions. Frontmatter holds `name` (the folder name) and `description`. Give the bot the body as is, then [team-rules.md](team-rules.md).
- Other files in a folder support its persona. Copy the whole folder.
- The folders are the five roles: `chief-of-staff` (talks to the owner, drives Claude), `planner` (runs `/maestro-planner`), `engineer` (runs `/maestro-work`), `qa-lead` (runs `/maestro-qa-lead`) and `reviewer` (runs `/maestro-review`). For more bots, run several off one persona; don't add a folder.
- A persona is the bot's context: who it is, how it thinks, what it runs, and links. What a Claude session does when the bot runs a command lives in that command's skill under `skills/`. Link to the skill; never restate it.
- A persona may name Maestro's commands and skills (`/maestro-planner`, `/maestro-create-ticket`, `/maestro-work`, `/maestro-software-engineer`, `/maestro-qa-lead`, `/maestro-review`, `/maestro-chief-of-staff`) and the commands a bot runs (`claude -p`, `maestro-ticket`, `gh`). Only playbooks and prompt templates stay off the user's own skills; `skills/use.yaml` names those.
- Write in second person. Link to a team rule; never restate it.
