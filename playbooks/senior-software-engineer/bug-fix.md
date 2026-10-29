---
name: bug-fix
description: Fix a bug by reproducing it first with a failing test, then fixing it on a maestro/ branch and submitting it for review.
role: senior-software-engineer
trigger: new-work
claims: todo
types: bug
---

# Fix a bug

## Goal

The bug is shown failing before the fix and passing after it, on a `maestro/` branch, with nothing else changed.

## When to use

A `todo` bug ticket for role `senior-software-engineer`. Routine bugs for the Junior Engineer go through their `build-ticket`.

## Steps

### 1. Start and set up

- **Goal:** The ticket is in progress and has its own worktree.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <holder> --token <token> --idempotency-key <session-id>:start`, then `worktree add <id> --from <main branch>`
- **Done when:** Both exit `0`.
- **Evidence:** exit codes; new ticket version; branch and worktree path.

### 2. Reproduce

- **Goal:** A test or script that fails because of the bug, committed on the branch.
- **Role:** harness
- **Skill:** `maestro-capture-baseline`
- **Model:** Frontier
- **Done when:** The repro fails on the unfixed code for the reason the ticket describes, and the failure output is saved.
- **Evidence:** the repro's path; the command; its failing exit code and output path; commit sha.

Can't reproduce: don't guess a fix. Block with an `Escalation:` note to the owner saying what was tried and what's needed.

### 3. Find the cause

- **Goal:** The root cause, not just where it shows.
- **Role:** harness
- **Skill:** `maestro-plan-change`
- **Model:** Frontier
- **Done when:** The scratchpad names the cause with `file:line`, the fix, and any other place the same cause appears.
- **Evidence:** paths read; the scratchpad path.

### 4. Fix

- **Goal:** The fix, committed, with the repro now passing.
- **Role:** harness
- **Skill:** `maestro-build-change`
- **Model:** Frontier
- **Done when:** The repro from step 2 passes, the worktree is clean, and the diff touches only the cause.
- **Evidence:** commit sha; files changed; the repro's passing exit code.

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

- The bug can't be reproduced, or the ticket describes behaviour that is by design: block with an `Escalation:` note to the owner.
- The step budget runs out: commit work in progress, then block with an `Escalation:` note to the owner.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: `in_review` (submit).
- Handoff note: branch, commit sha, the repro (path, failing output before, passing after), the cause, check results, verification evidence paths, any `Skipped:` steps, `author-family: <family>`.
