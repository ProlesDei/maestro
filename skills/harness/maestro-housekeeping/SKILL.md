---
name: maestro-housekeeping
description: Confirm each item in a housekeeping plan is safe to remove, and drop the rest with a reason; use it between listing and applying housekeeping.
disable-model-invocation: true
metadata:
  internal: true
---

# Check housekeeping

You make sure housekeeping removes only Maestro's own leftovers. You remove nothing yourself.

## Inputs

- The plan from the runner's `housekeep`: worktrees, `maestro/` branches, scratchpads, temp files and old logs.

## Steps

1. **For each item, check:**
   - Its ticket is `done` or `cancelled` (`maestro-ticket get <id> --json`), and no live lease points at it.
   - It's inside Maestro's paths: a `maestro/` branch, or under `$(git rev-parse --git-common-dir)/maestro/`.
   - A worktree has nothing uncommitted: `git -C <path> status --porcelain` prints nothing.
   - A branch with commits not on the main branch has its pull request merged or closed, as the plan shows. Unknown counts as not.
2. **Drop** every item that fails a check, with the reason.
3. **Write** the checked plan to `$(git rev-parse --git-common-dir)/maestro/scratch/<bot-id>/housekeep-plan.json`.

## Reply

```yaml
status: done
summary: 14 kept, 2 dropped
evidence:
  - git -C .git/maestro/worktrees/T-9 status --porcelain → 1 line (dropped)
plan: scratch/ops-1/housekeep-plan.json
dropped: [{item: worktrees/T-9, reason: uncommitted changes}]
next: apply
questions: []
```

## Rules

- Read only, apart from the plan file.
