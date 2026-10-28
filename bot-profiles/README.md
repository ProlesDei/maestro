# Bot profiles

Bot definitions meant for tools outside this repo. A bot (for example Grok) reads a profile and creates the bot it describes. Nothing in this repo loads these files.

## Format

One bot per folder: `bot-profiles/<name>/`. The persona is `bot-profiles/<name>/persona.md`. Copy `_template/` to start, then set `name` in `persona.md` to the folder name.

- **`persona.md`**: the bot definition.
  - **Frontmatter**: metadata the reader uses to set the bot up. Required: `name`, `description`. `name` matches the folder name. Add fields (model, tools) only when a reader needs them.
  - **Body**: the bot's instructions. Paste it as is; it must make sense with no other context.
- **Other files** in the folder support that persona (checklists, examples, reference). A reader copies the whole folder, so those files stay with the persona.

## Rules

- `persona.md` holds the instructions. Other files in the same folder are there for that persona to use. Don't split the instructions with "see above".
- Write instructions in second person ("You review pull requests...").
- Folder names: lowercase, hyphens, matching the `name` field in `persona.md`.
- Folders and files starting with `_` are not bots. Readers skip them.
