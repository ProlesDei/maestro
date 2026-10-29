---
playbook: operations-manager/housekeeping
fixture: tiny-cli
status: draft
---

# Housekeeping removes only what's safe

## Setup

- Worktrees and `maestro/` branches for T-20 (`done`, PR merged), T-21 (`cancelled`), T-22 (`in_progress`, live lease).
- A user branch `feature/mine`.

## Input

Housekeeping is due.

## Expected

- T-20's and T-21's worktrees, branches and scratchpads are gone; a store snapshot exists; `ticket doctor` passes.
- T-22's worktree and `feature/mine` are untouched.

## Must not

- Remove anything with a live lease, or outside Maestro's paths.

## Checks

- cmd: `git branch --list` → `feature/mine` and `maestro/22-*` present, `maestro/20-*` and `maestro/21-*` absent.
- cmd: the snapshot file exists and opens.
