---
name: fix-sent-back
description: Fix a routine ticket sent back by failed checks, a code review or QA, and resubmit, handing it to the Senior Engineer after two failed tries.
role: junior-software-engineer
trigger: sent-back
claims: in_progress
---

# Fix work sent back

## Goal

Every item that came back is fixed, or answered with evidence, and the ticket is back in review.

## When to use

An `in_progress` ticket for role `junior-software-engineer` with no live lease, whose latest `Handoff:` is one of:

- checks failed
- changes requested by the Code Reviewer
- QA failed

## Steps

### 1. Read what came back

- **Goal:** A numbered list of what must change.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** Each item has its source, the file or acceptance criterion, and the fix you plan.
- **Evidence:** `maestro-ticket get <id> --json` version; the comment the items came from.

If an item needs a design decision, escalate now. That isn't a failed try.

### 2. Set up the worktree

- **Goal:** A worktree on the ticket's existing branch.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** `$(git rev-parse --git-common-dir)/maestro/worktrees/<id>` exists on `maestro/<id>-<slug>`. If it's missing, `git worktree add <path> maestro/<id>-<slug>` exits `0`.
- **Evidence:** exit code; branch and path.

### 3. Fix

- **Goal:** Each item addressed, nothing else changed.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** Fixes and any new tests are committed; `git -C <worktree> status --porcelain` is empty. Branch and worktree are in the scratchpad.
- **Evidence:** commit sha; for each item, the files changed.

### 4. Run the checks

- **Goal:** The project's checks pass on the branch.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** Build, lint and test from `.maestro/project-setup.md` all exit `0` in the worktree.
- **Evidence:** each command, exit code and test counts.

A pass through steps 3 and 4 that ends with failing checks is a failed try. After the first, go back to step 3. After the second, escalate.

## Stop and block when

- You disagree with a requested change: don't skip it silently. Escalate, with the reason and evidence.
- Retries or budget run out before two tries: block.

## Escalate to the Senior Engineer

1. Commit work in progress on the branch so the worktree is clean.
2. Wrap up with status `todo` and `--role senior-software-engineer`.
3. Start the note `Escalation:` and list the items, what you tried, the failing output and the branch.

## Wrap-up

- Status: `in_review` (resubmit), or `todo` with `--role senior-software-engineer` when escalating.
- Handoff note: each item and how it was fixed; commit sha; check results; `author-family: <family>`.
