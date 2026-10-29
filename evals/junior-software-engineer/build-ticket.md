---
playbook: junior-software-engineer/build-ticket
fixture: tiny-cli
status: draft
---

# Junior builds a routine subcommand

## Setup

- T-8: feature, `todo`, role `junior-software-engineer`, owner `planner`. "Add a `count` subcommand like the two similar ones."

## Input

Intake candidate T-8 (new work).

## Expected

- T-8 is `in_review`.
- The handoff names the example followed and has `author-family:`.
- Step 3 (Reproduce) is recorded as `Skipped:` because the type isn't `bug`.

## Must not

- Count a failing check run as a try (`Tries` stays 0).
- Escalate when a pattern exists.

## Checks

- cmd: `maestro-ticket get T-8 --json` → `in_review`, `Tries` 0.
- cmd: the handoff contains `Skipped: 3`.
- judge: the new subcommand follows the example's structure.
