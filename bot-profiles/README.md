# Bot profiles

Bot definitions meant for tools outside this repo. A bot (for example Grok) reads a profile and creates the bot it describes. Nothing in this repo loads these files.

## Format

One bot per folder: `bot-profiles/<name>/bot.md`, for example `bot-profiles/chief-of-staff/bot.md`. Copy `_template/` to start.

- **Frontmatter**: metadata the reader uses to set the bot up. Required: `name`, `description`. Add fields (model, tools) only when a reader needs them.
- **Body**: the bot's instructions. Paste it as is; it must make sense with no other context.

## Rules

- Keep each profile self-contained. No links to other files, no "see above".
- Write instructions in second person ("You review pull requests...").
- Folder names: lowercase, hyphens, matching the `name` field.
- Folders starting with `_` are not bots. Readers skip them.
