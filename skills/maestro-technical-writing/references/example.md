# One rewrite

Before:

> Configuration of the migration check script parameters is performed via checks.json. It's important to note that running with --fix, which rewrites the migration files to match the current schema, should only be done on a clean branch, as it is a double-edged sword. If any are found, CI fails.

After:

> `migrate-check.sh` reads its settings from `checks.json` and finds migrations that don't match the schema. If it finds one, CI fails. Run `migrate-check.sh --fix` only on a clean branch, because it rewrites the migration files.

What changed, by rule:

- **Lead with the point:** the first sentence now says what the script does, not how to set it up.
- **Active voice:** "configuration is performed" became "`migrate-check.sh` reads".
- **Cut words:** "It's important to note that" went.
- **No stock figures:** "a double-edged sword" became the plain fact: it rewrites the migration files.
- **Concrete:** "the migration check script" became `migrate-check.sh`; "If any are found" now says what it finds.
