---
name: fix-sent-back
description: Fix a ticket sent back by failed checks, a code review or QA, address each item, and resubmit.
role: senior-software-engineer
trigger: sent-back
claims: in_progress
---

# Fix work sent back

## Goal

Every item that came back is fixed, or answered with evidence, and the ticket is back in review.

## When to use

An `in_progress` ticket for role `senior-software-engineer` with no live lease, whose latest `Handoff:` is one of:

- checks failed
- changes requested by the Code Reviewer
- QA failed

## Steps

### 1. Read what came back

- **Goal:** A numbered list of what must change.
- **Skill:** none
- **Tier:** Frontier
- **Done when:** Each item has its source (reviewer, QA, checks), the file or acceptance criterion, and the fix you plan. Items you disagree with are marked, with the reason.
- **Evidence:** `at-ticket get <id> --json` version; the comment the items came from.

### 2. Set up the worktree

- **Goal:** A worktree on the ticket's existing branch.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** `$(git rev-parse --git-common-dir)/agent-team/worktrees/<id>` exists on `at/<id>-<slug>`. If it's missing, `git worktree add <path> at/<id>-<slug>` exits `0`.
- **Evidence:** exit code; branch and path.

### 3. Fix

- **Goal:** Each item addressed, nothing else changed.
- **Skill:** none
- **Tier:** Frontier
- **Done when:** Fixes and any new tests are committed; `git -C <worktree> status --porcelain` is empty. Branch and worktree are in the scratchpad.
- **Evidence:** commit sha; for each item, the files changed or the reason it wasn't.

### 4. Run the checks

- **Goal:** The project's checks pass on the branch.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** Build, lint and test from `.agent-team/project-setup.md` all exit `0` in the worktree.
- **Evidence:** each command, exit code and test counts.

On a failure, go back to step 3. That counts as a retry.

## Stop and block when

- A requested change conflicts with the ticket or another reviewer's ask: block with an `Escalation:` note that states both sides and your recommendation.
- Retries or budget run out. Commit work in progress first.

## Wrap-up

- Status: `in_review` (resubmit).
- Handoff note: each item and how it was fixed, or why not with evidence; commit sha; check results; `author-family: <family>`.
