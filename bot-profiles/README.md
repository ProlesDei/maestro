# Bot profiles

Bot definitions meant for tools outside this repo. A bot (for example Grok) reads a profile and creates the bot it describes. Nothing in this repo loads these files.

## Format

One bot per file: `bot-profiles/<name>.md`. Copy `_template.md` to start.

- **Frontmatter**: metadata the reader uses to set the bot up. Required: `name`, `description`. Add fields (model, tools) only when a reader needs them.
- **Body**: the bot's instructions. Paste it as is; it must make sense with no other context.

## Rules

- Keep each profile self-contained. No links to other files, no "see above".
- Write instructions in second person ("You review pull requests...").
- File names: lowercase, hyphens, matching the `name` field.
- Files starting with `_` are not bots. Readers skip them.
