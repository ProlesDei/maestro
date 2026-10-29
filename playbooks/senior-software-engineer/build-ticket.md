---
name: build-ticket
description: Build a complex or risky ticket on its own maestro/ branch, with tests that prove each acceptance criterion, and submit it for review.
role: senior-software-engineer
trigger: new-work
claims: todo
types: feature, chore, docs
---

# Build a ticket

## Goal

Clean, well-tested code that does exactly what the ticket asks, on a `maestro/` branch, passing the project's checks.

## When to use

A `todo` ticket for role `senior-software-engineer`. Not for bugs (`bug-fix`), a body line `Playbook: refactor` or `Playbook: performance`, investigations (`scout-investigation`), or an `Escalation:` from the Junior Engineer (`take-escalation`).

## Steps

### 1. Start and set up

- **Goal:** The ticket is in progress and has its own worktree.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <holder> --token <token> --idempotency-key <session-id>:start`, then `worktree add <id> --from <main branch>`
- **Done when:** Both exit `0`.
- **Evidence:** exit codes; new ticket version; branch and worktree path.

### 2. Plan

- **Goal:** A plan that maps every acceptance criterion to a test or check, and names the risks.
- **Role:** harness
- **Skill:** `maestro-plan-change`
- **Model:** Frontier
- **Done when:** The plan is in the scratchpad: files to change, approach, each criterion with its proof, each risk with how it's covered.
- **Evidence:** paths read; the scratchpad path.

### 3. Build

- **Goal:** The change and its tests, committed on the branch.
- **Role:** harness
- **Skill:** `maestro-build-change`
- **Model:** Frontier
- **Done when:** Work is committed in the worktree and it is clean. The diff touches only what the ticket needs.
- **Evidence:** commit sha; files changed; tests added.

### 4. Run the checks

- **Goal:** The project's checks pass on the branch.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `check all`
- **Done when:** Every check exits `0`.
- **Evidence:** each check, exit code and test counts.

On a failure, go back to step 3 with the output path. Failing checks are not a try; the step budget is the limit.

### 5. Verify

- **Goal:** Evidence from the running app that each acceptance criterion is met.
- **Role:** harness
- **Skill:** `maestro-verify`
- **Model:** Mid-tier
- **Done when:** Every acceptance criterion passes, and every definition-of-done row a later gate doesn't own, with its evidence saved. Anything started is stopped, or listed in the scratchpad for clean-up.
- **Evidence:** per criterion: pass or fail, and the evidence path.

A failure: go back to step 3. It isn't a try.

## Stop and block when

- The ticket is unclear or contradicts the code or another ticket: block with an `Escalation:` note to the owner (`maestro-escalate`) with the exact question.
- The step budget runs out: commit work in progress, then block with an `Escalation:` note to the owner.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: `in_review` (submit).
- Handoff note: branch, commit sha, check results, verification evidence paths, how each criterion is met, risks and how they're covered, any `Skipped:` steps, `author-family: <family>`.
