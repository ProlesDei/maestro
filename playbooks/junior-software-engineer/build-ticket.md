---
name: build-ticket
description: Build a routine, well-defined ticket by following an existing pattern in the code, reproducing bugs first, and submit it for review.
role: junior-software-engineer
trigger: new-work
claims: todo
types: feature, bug, chore, docs
---

# Build a ticket

## Goal

The ticket is done the way similar code in the repo is already done, with tests, on a `maestro/` branch, passing the project's checks.

## When to use

A `todo` ticket for role `junior-software-engineer`.

## Steps

### 1. Start and set up

- **Goal:** The ticket is in progress and has its own worktree.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <holder> --token <token> --idempotency-key <session-id>:start`, then `worktree add <id> --from <main branch>`
- **Done when:** Both exit `0`.
- **Evidence:** exit codes; new ticket version; branch and worktree path.

### 2. Find the pattern

- **Goal:** An existing example in the code to follow, and a plan.
- **Role:** harness
- **Skill:** `maestro-plan-change`
- **Model:** Mid-tier
- **Done when:** The scratchpad names the example (paths), the files to change, and how each acceptance criterion is proved.
- **Evidence:** paths read; the example.

No pattern to follow, or the ticket needs a design decision: escalate to the Senior Engineer now.

### 3. Reproduce

- **Goal:** A test that fails because of the bug, committed on the branch.
- **Role:** harness
- **Skill:** `maestro-capture-baseline`
- **Model:** Mid-tier
- **Skip when:** the ticket type isn't `bug`.
- **Done when:** The repro fails on the unfixed code for the reason the ticket describes.
- **Evidence:** the repro's path; the command; its failing exit code and output path; commit sha.

Can't reproduce: escalate to the Senior Engineer.

### 4. Build

- **Goal:** The change and its tests, committed on the branch.
- **Role:** harness
- **Skill:** `maestro-build-change`
- **Model:** Mid-tier
- **Done when:** Work is committed and the worktree is clean. The diff follows the example and touches only what the ticket needs. For a bug, the repro now passes.
- **Evidence:** commit sha; files changed; tests added.

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

- The ticket is unclear: block with an `Escalation:` note to the owner (`maestro-escalate`) with the exact question.

## Escalate to the Senior Engineer

When there's no pattern, a design decision is needed, the bug won't reproduce, or the step budget runs out:

1. Commit work in progress on the branch so the worktree is clean.
2. Wrap up with status `todo` and `--role senior-software-engineer`.
3. The note starts `Escalation:` (`maestro-escalate`): what you tried, the failing output, the branch, `Tries`, and what you think is wrong.

## Wrap-up

- Skill: `maestro-handoff-note`, or `maestro-escalate` when blocking or handing the ticket to the Senior Engineer.
- Status: `in_review` (submit), or `todo` with `--role senior-software-engineer` when escalating.
- Handoff note: branch, commit sha, check results, verification evidence paths, how each criterion is met, the example followed, any `Skipped:` steps, `author-family: <family>`.
