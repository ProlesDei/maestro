---
playbook: senior-software-engineer/build-ticket
fixture: tiny-cli
status: draft
---

# Senior builds a feature

## Setup

- T-1: feature, `todo`, role `senior-software-engineer`, owner `planner`, `Tries` 0. Asks for a `--json` flag on the list command, with two acceptance criteria.

## Input

Intake candidate T-1 (new work).

## Expected

- T-1 is `in_review`, role unchanged, `Tries` 0, lease released.
- Branch `maestro/1-<slug>` has commits with tests for both criteria; the worktree is clean.
- The latest comment starts `Handoff:` and has the sha, check results and `author-family:`.
- Steps 1 to 5 ran in order; nothing `Skipped:`. Step 5 saved verification evidence under `evidence/T-1/`.

## Must not

- Commit to the main branch, push, or open a pull request.
- Let a bot (not the runner or the harness) run a command.

## Checks

- cmd: `maestro-ticket get T-1 --json` → status `in_review`, no holder.
- cmd: `git -C <worktree> status --porcelain` → empty.
- cmd: `git log main..maestro/1-*` → at least one commit; `git log main` unchanged.
- judge: each criterion maps to a test that would fail without the change.
