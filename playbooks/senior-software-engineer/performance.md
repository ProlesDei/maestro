---
name: performance
description: Make code faster or leaner by tracing it before the change, changing only what the trace shows, and tracing again with the same measure after.
role: senior-software-engineer
trigger: new-work
claims: todo
types: feature, bug, chore
---

# Performance

## Goal

A measured improvement against the ticket's target, shown by the same trace before and after, with behaviour unchanged.

## When to use

A `todo` ticket for role `senior-software-engineer` whose body has the line `Playbook: performance` and a target (for example "p95 under 200 ms on the list endpoint").

## Steps

### 1. Start and set up

- **Goal:** The ticket is in progress and has its own worktree.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <holder> --token <token> --idempotency-key <session-id>:start`, then `worktree add <id> --from <main branch>`
- **Done when:** Both exit `0`.
- **Evidence:** exit codes; new ticket version; branch and worktree path.

### 2. Trace before

- **Goal:** A repeatable measure of the slow path and where its time or memory goes.
- **Role:** harness
- **Skill:** `maestro-capture-baseline`
- **Model:** Frontier
- **Done when:** A benchmark or trace script is committed, run several times on the unchanged code, and its results and profile are saved. The hot spots are named with `file:line`.
- **Evidence:** the script's path and command; the runs' numbers with spread; the profile path; commit sha.

No target in the ticket, or the trace shows it's already met: block with an `Escalation:` note to the owner and the numbers.

### 3. Change

- **Goal:** A change aimed only at the hot spots the trace found.
- **Role:** harness
- **Skill:** `maestro-build-change`
- **Model:** Frontier
- **Done when:** Committed; the worktree is clean; existing tests are unchanged.
- **Evidence:** commit sha; files changed; which hot spot each change targets.

### 4. Trace after

- **Goal:** The same measure on the changed code.
- **Role:** harness
- **Skill:** `maestro-capture-baseline`
- **Model:** Mid-tier
- **Done when:** The step 2 script ran the same number of times on the same machine, and the result meets the target beyond the spread. Not met: back to step 3.
- **Evidence:** before and after numbers side by side; the profile path.

### 5. Run the checks

- **Goal:** The project's checks pass on the branch.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `check all`
- **Done when:** Every check exits `0`.
- **Evidence:** each check, exit code and test counts.

On a failure, go back to step 3. Failing checks are not a try.

### 6. Verify

- **Goal:** Evidence from the running app that each acceptance criterion is met.
- **Role:** harness
- **Skill:** `maestro-verify`
- **Model:** Mid-tier
- **Done when:** Every acceptance criterion passes, and every definition-of-done row a later gate doesn't own, with its evidence saved. Anything started is stopped, or listed in the scratchpad for clean-up.
- **Evidence:** per criterion: pass or fail, and the evidence path.

A failure: go back to step 3. It isn't a try.

## Stop and block when

- The target needs a design change beyond the ticket (a new cache, a schema change, a new dependency): block with an `Escalation:` note to the owner and the trace.
- The step budget runs out: commit work in progress, then block with an `Escalation:` note to the owner.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: `in_review` (submit).
- Handoff note: branch, commit shas, the trace script and how to run it, before and after numbers, check results, verification evidence paths, any `Skipped:` steps, `author-family: <family>`.
