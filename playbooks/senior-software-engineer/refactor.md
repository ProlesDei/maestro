---
name: refactor
description: Restructure code without changing what it does, by capturing its current behaviour in tests first and keeping them passing throughout.
role: senior-software-engineer
trigger: new-work
claims: todo
types: chore
---

# Refactor

## Goal

The code is restructured as the ticket asks, and tests written before the change prove its behaviour is the same after it.

## When to use

A `todo` ticket for role `senior-software-engineer` whose body has the line `Playbook: refactor`. A change that is meant to alter behaviour is not a refactor: use `build-ticket`.

## Steps

### 1. Start and set up

- **Goal:** The ticket is in progress and has its own worktree.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <holder> --token <token> --idempotency-key <session-id>:start`, then `worktree add <id> --from <main branch>`
- **Done when:** Both exit `0`.
- **Evidence:** exit codes; new ticket version; branch and worktree path.

### 2. Capture current behaviour

- **Goal:** Tests that pin down what the code does today, at its public edges, committed before any change.
- **Role:** harness
- **Skill:** `maestro-capture-baseline`
- **Model:** Frontier
- **Done when:** The new tests pass on the unchanged code and cover every caller-visible behaviour the refactor could touch. Gaps that can't be tested are listed.
- **Evidence:** test paths; the command and its passing exit code; commit sha; the gap list.

### 3. Prove the tests can fail

- **Goal:** The captured tests catch a change in behaviour.
- **Role:** harness
- **Skill:** `maestro-capture-baseline`
- **Model:** Mid-tier
- **Done when:** A deliberate, uncommitted break in the code makes at least one captured test fail, and the break is reverted.
- **Evidence:** what was broken; the failing test name; the worktree is clean afterwards.

### 4. Refactor

- **Goal:** The restructure, in small commits, with the captured tests passing after each.
- **Role:** harness
- **Skill:** `maestro-build-change`
- **Model:** Frontier
- **Done when:** The ticket's structure is reached, the captured tests are unchanged and passing, and the worktree is clean.
- **Evidence:** commit shas; files changed; `git diff` of the test files from step 2 is empty.

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

- Keeping behaviour the same needs a captured test to change: that's a behaviour change. Block with an `Escalation:` note to the owner.
- The step budget runs out: commit work in progress, then block with an `Escalation:` note to the owner.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: `in_review` (submit).
- Handoff note: branch, commit shas, the captured tests and the gap list, proof they can fail, check results, verification evidence paths, any `Skipped:` steps, `author-family: <family>`.
