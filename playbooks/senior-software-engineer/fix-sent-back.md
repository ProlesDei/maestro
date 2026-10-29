---
name: fix-sent-back
description: Fix a ticket the Code Reviewer or QA sent back, address each item, and resubmit, blocking it once Tries is 3 above where you took it over.
role: senior-software-engineer
trigger: sent-back
claims: in_progress
types: feature, bug, chore, docs
---

# Fix work sent back

## Goal

Every item that came back is fixed, or answered with evidence, and the ticket is back in review.

## When to use

An `in_progress` ticket for role `senior-software-engineer` with no live lease, whose latest `Handoff:` is `changes requested` from the Code Reviewer or `QA failed`.

## Steps

### 1. Check the tries

- **Goal:** Know whether this ticket gets another try.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket get <id> --json`
- **Done when:** You have `Tries` and the `tries-at-takeover` from your takeover note (0 if you built it from the start). `Tries` is 3 or more above `tries-at-takeover`: skip to Stop and block.
- **Evidence:** version; `Tries`; `tries-at-takeover` and the comment it's from.

### 2. Read what came back

- **Goal:** A numbered list of what must change.
- **Role:** harness
- **Skill:** `maestro-read-feedback`
- **Model:** Frontier
- **Done when:** Each item has its source (reviewer or QA), the file or acceptance criterion, and the planned fix. Items you disagree with are marked, with the reason.
- **Evidence:** the comment the items came from; the list.

### 3. Set up the worktree

- **Goal:** A worktree on the ticket's existing branch.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `worktree add <id>`
- **Done when:** Exit `0`, on `maestro/<id>-<slug>`.
- **Evidence:** exit code; branch and path.

### 4. Fix

- **Goal:** Each item addressed, nothing else changed.
- **Role:** harness
- **Skill:** `maestro-build-change`
- **Model:** Frontier
- **Done when:** Fixes and any new tests are committed and the worktree is clean.
- **Evidence:** commit sha; for each item, the files changed or the reason it wasn't.

### 5. Run the checks

- **Goal:** The project's checks pass on the branch.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `check all`
- **Done when:** Every check exits `0`.
- **Evidence:** each check, exit code and test counts.

On a failure, go back to step 4. Failing checks are not a try.

### 6. Verify

- **Goal:** Evidence from the running app that each acceptance criterion is met.
- **Role:** harness
- **Skill:** `maestro-verify`
- **Model:** Mid-tier
- **Done when:** Every acceptance criterion passes, and every definition-of-done row a later gate doesn't own, with its evidence saved. Anything started is stopped, or listed in the scratchpad for clean-up.
- **Evidence:** per criterion: pass or fail, and the evidence path.

A failure: go back to step 4. It isn't a try.

## Stop and block when

- `Tries` is 3 or more above `tries-at-takeover`: block with an `Escalation:` note to the owner (`maestro-escalate`): what each try changed, what keeps failing, and your recommendation.
- A requested change conflicts with the ticket or another reviewer's ask: block with an `Escalation:` note to the owner stating both sides and your recommendation.
- The step budget runs out: commit work in progress, then block with an `Escalation:` note to the owner.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: `in_review` (resubmit).
- Handoff note: each item and how it was fixed, or why not with evidence; commit sha; check results; verification evidence paths; `Tries`; any `Skipped:` steps; `author-family: <family>`.
