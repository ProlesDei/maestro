# Bot profiles

One folder per role. Grok Bot runs outside this repo and reads a folder to take on that role. Nothing in Maestro loads them.

- `<name>/persona.md`: the bot's instructions. Frontmatter holds `name` (the folder name) and `description`. Give the bot the body as is, then [team-rules.md](team-rules.md). `scripts/bot-bundle.sh` joins them into one block to paste, and refuses one over Grok's 20,000-character limit, so keep every persona plus the team rules under 19,700 bytes, as `scripts/install_test.sh` checks, to leave room for the repo path.
- `<name>/memory.md`, optional: starting memories, the short standing rules a team tunes (a limit, a threshold). `bot-bundle.sh` puts them in a message apart from the block; the bot saves each bullet as one memory. Grok Bot may show a bot only its newest memories, so keep each short, and put a rule that must always hold in the persona.
- Other files in a folder support its persona. Copy the whole folder.
- The folders are six profiles for the five ticket roles: `chief-of-staff` (talks to the owner, leads through the planner), `planner` (runs the team day to day and `/maestro-planner`), `senior-engineer` and `junior-engineer` (both build tickets as the `engineer` role, on different models), `qa-lead` (runs `/maestro-qa-lead`) and `reviewer` (runs `/maestro-code-review`). For more agents on one profile, run several Grok Bot sessions off one persona; don't add a folder.
- A persona is the bot's context: who it is, how it thinks and what it runs. What a Claude session does when the bot runs a command lives in that command's skill under `skills/`. Name the skill; never restate it.
- A persona may name Maestro's commands and skills (`/maestro-planner`, `/maestro-create-ticket`, `/maestro-software-engineer`, `/maestro-qa-lead`, `/maestro-code-review`, `/maestro-chief-of-staff`) and the commands a bot runs (`claude -p`, `maestro-ticket`, `gh`).
- Write in second person. Name a team rule; never restate it.
- No links. Grok Bot never reads repo files, so a persona or `team-rules.md` holds every command its bot runs.
