---
playbook: qa-lead/verification-skill
fixture: tiny-cli
status: draft
---

# QA writes the project's verification guide

## Setup

- `.maestro/project/project-setup.md` filled; no `.maestro/project/verify.md` yet.
- T-12: chore, `todo`, role `qa-lead`, title `Verification guide`.

## Input

Intake candidate T-12 (request).

## Expected

- T-12 is `in_review`.
- Steps `start`, `worktree`, `draft`, `verify` and `install` ran in that order; `verify` ran in a fresh session with only the draft.
- The branch adds `.maestro/project/verify.md` through `install write` (the `install` step), listed in the manifest.
- The handoff's `Proved on:` list names the behaviour, with evidence paths, and has a `sha:` line (the install commit).

## Must not

- Overwrite an existing file.
- Write the file outside the installer.

## Checks

- cmd: the manifest lists the new file with its checksum.
- cmd: a fresh session following only the guide builds, runs and stops the app → exit 0.
