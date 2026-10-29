---
name: fix-sent-back
description: Fix a routine ticket the Code Reviewer or QA sent back and resubmit it, or hand it to the Senior Engineer once Tries reaches 2.
role: junior-software-engineer
trigger: sent-back
claims: in_progress
types: feature, bug, chore, docs
---

# Fix work sent back

## Goal

Every item that came back is fixed, or answered with evidence, and the ticket is back in review. After two send-backs it goes to the Senior Engineer instead.

## When to use

An `in_progress` ticket for role `junior-software-engineer` with no live lease, whose latest `Handoff:` is `changes requested` from the Code Reviewer or `QA failed`.

## Steps

### 1. Check the tries

- **Goal:** Know whether this ticket gets another try from you.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket get <id> --json`
- **Done when:** You have `Tries`. At 2 or more: skip to Escalate to the Senior Engineer.
- **Evidence:** version; `Tries`.

### 2. Read what came back

- **Goal:** A numbered list of what must change.
- **Role:** harness
- **Skill:** `maestro-read-feedback`
- **Model:** Mid-tier
- **Done when:** Each item has its source, the file or acceptance criterion, and the planned fix.
- **Evidence:** the comment the items came from; the list.

An item needs a design decision, or you disagree with it: escalate now, with the reason.

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
- **Model:** Mid-tier
- **Done when:** Fixes and any new tests are committed and the worktree is clean.
- **Evidence:** commit sha; for each item, the files changed.

### 5. Run the checks

- **Goal:** The project's checks pass on the branch.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `check all`
- **Done when:** Every check exits `0`.
- **Evidence:** each check, exit code and test counts.

On a failure, go back to step 4. Failing checks are not a try; the step budget is the limit.

### 6. Verify

- **Goal:** Evidence from the running app that each acceptance criterion is met.
- **Role:** harness
- **Skill:** `maestro-verify`
- **Model:** Mid-tier
- **Done when:** Every acceptance criterion passes, and every definition-of-done row a later gate doesn't own, with its evidence saved. Anything started is stopped, or listed in the scratchpad for clean-up.
- **Evidence:** per criterion: pass or fail, and the evidence path.

A failure: go back to step 4. It isn't a try.

## Stop and block when

- Nothing here blocks. Anything you can't finish goes to the Senior Engineer.

## Escalate to the Senior Engineer

When `Tries` is 2 or more, an item needs a design decision, you disagree with an item, or the step budget runs out:

1. Commit work in progress on the branch so the worktree is clean.
2. Wrap up with status `todo` and `--role senior-software-engineer`.
3. The note starts `Escalation:` (`maestro-escalate`): the items, what each try changed, the failing output, the branch and `Tries`.

## Wrap-up

- Skill: `maestro-handoff-note`, or `maestro-escalate` when blocking or handing the ticket to the Senior Engineer.
- Status: `in_review` (resubmit), or `todo` with `--role senior-software-engineer` when escalating.
- Handoff note: each item and how it was fixed; commit sha; check results; verification evidence paths; `Tries`; any `Skipped:` steps; `author-family: <family>`.
