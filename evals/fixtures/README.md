# Fixtures

Starting points for golden tasks. Not built yet: each entry says what the fixture must contain so tasks can be written against it now.

## `tiny-cli`

A small repo in one language with fast checks (build, lint, test in under 30 seconds), Maestro installed at trust level 1, and a filled `.maestro/project/project-setup.md`.

It must contain:

- A list command with a known off-by-one bug when the list is empty (for bug tasks).
- A module with no tests and two callers (for refactor tasks).
- A lookup that scans a list where a map would do, slow at 10,000 items (for performance tasks).
- A pattern to copy: two similar subcommands (for routine junior tasks).
- A CI config, a pull request template and a CONTRIBUTING file (for setup and definition-of-done tasks).
- A fake code host that records pull requests, so `open pr` and `pr status` work offline.

## `tiny-cli-bare`

`tiny-cli` before install: no `.maestro/`, but with its own `AGENTS.md`, `CLAUDE.md` and harness settings, so setup tasks can prove those stay byte-for-byte unchanged.

## `board-*`

Board states loaded into the ticket store on top of `tiny-cli`, named in each task's Setup. They are written as `maestro-ticket` commands so they replay exactly.
