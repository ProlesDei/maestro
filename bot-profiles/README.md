# Bot profiles

Bot definitions meant for tools outside this repo. A bot runtime (for example Grok Bot) reads a profile and creates the bot it describes. Nothing in this repo loads these files.

Every bot gets, in this order: its `persona.md`, `team-rules.md` (rules shared by every bot), any skills `skills/use.yaml` lists for it under `bots` (none by default), and its role's playbooks in `playbooks/<name>/`, which `maestro-play` runs. Bots never touch the target repo themselves; they reach it through the runner (`docs/runner.md`).

## Format

One bot per folder: `bot-profiles/<name>/`. The persona is `bot-profiles/<name>/persona.md`. Copy `_template/` to start, then set `name` in `persona.md` to the folder name.

- **`persona.md`**: the bot definition.
  - **Frontmatter**: metadata the reader uses to set the bot up. Required: `name`, `description`. `name` matches the folder name. Add fields (model, tools) only when a reader needs them.
  - **Body**: the bot's instructions. Paste it as is, followed by `team-rules.md`. Together they must make sense with no other context.
- **Other files** in the folder support that persona (checklists, examples, reference). A reader copies the whole folder, so those files stay with the persona.

## Rules

- `persona.md` holds the instructions. Other files in the same folder are there for that persona to use. Don't split the instructions with "see above".
- Write instructions in second person ("You review pull requests...").
- Never restate a team rule in a persona. Name the rule and let `team-rules.md` define it.
- Personas say what the bot wants from its playbooks' steps and how to judge the result, at `ask` steps and escalations. Never tell a bot to read code, edit files or run commands.
- Never name a skill. Skills are the user's, and `skills/use.yaml` adds them (`skills/README.md`).
- Folder names: lowercase, hyphens, matching the `name` field in `persona.md`.
- Folders and files starting with `_` are not bots. Readers skip them.
