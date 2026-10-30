# Bot profiles

One folder per bot, for tools outside this repo (such as Grok Bot) to read and copy. Nothing in Maestro loads them.

- `<name>/persona.md`: the bot's instructions. Frontmatter holds `name` (the folder name) and `description`. Give the bot the body as is, then [team-rules.md](team-rules.md).
- Other files in a folder support its persona. Copy the whole folder.
- The folders are the three roles `maestro-ticket` knows: `chief-of-staff` (talks to the owner, drives Claude), `engineer` (runs `/work`) and `reviewer` (runs `/review`). For more bots, run several off one persona; don't add a folder.
- A persona may name Maestro's commands and skills (`/work`, `/create-ticket`, `/review`) and the commands a bot runs (`claude -p`, `maestro-ticket`, `gh`). Only playbooks and prompt templates stay off the user's own skills; `skills/use.yaml` names those.
- Write in second person. Link to a team rule; never restate it.
