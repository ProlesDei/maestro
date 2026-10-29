---
name: take-escalation
description: Take over a ticket the Junior Engineer escalated, find what went wrong, finish it and submit it.
role: senior-software-engineer
trigger: request
claims: todo
types: feature, bug, chore, docs
---

# Take an escalation

## Goal

The escalated ticket is finished and in review, and the reason it was hard is written down so the next one goes faster.

## When to use

A `todo` ticket for role `senior-software-engineer` whose latest comment starts `Escalation:`, from the Junior Engineer: `Tries` reached 2, the step budget ran out, or the ticket needed a design decision.

## Steps

### 1. Start

- **Goal:** The ticket is in progress under your lease, with the takeover recorded.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <holder> --token <token> --idempotency-key <session-id>:start --note "tries-at-takeover: <Tries>"`
- **Done when:** Exit `0`.
- **Evidence:** exit code; new version; `Tries`.

### 2. Read the escalation

- **Goal:** Know why it failed and whether the existing branch is worth keeping.
- **Role:** harness
- **Skill:** `maestro-read-feedback`
- **Model:** Frontier
- **Done when:** The cause is named, and you've decided: continue on `maestro/<id>-<slug>`, or start over from the main branch. Plan and decision are in the scratchpad.
- **Evidence:** the escalation comment; the branch log and diff read; the failing output it points to.

### 3. Set up the worktree

- **Goal:** A worktree on the branch you chose.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** continue: `worktree add <id>`; start over: `worktree add <id> --branch maestro/<id>-<new-slug> --from <main branch>`, leaving the old branch as it is.
- **Done when:** Exit `0`.
- **Evidence:** exit code; branch and path; the old branch, if you started over.

### 4. Build

- **Goal:** The change and its tests, committed.
- **Role:** harness
- **Skill:** `maestro-build-change`
- **Model:** Frontier
- **Done when:** Work is committed, the worktree is clean, and every acceptance criterion has a test or check.
- **Evidence:** commit sha; files changed; tests added.

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

- The ticket itself is wrong or too big: block with an `Escalation:` note to the owner saying what should change.
- The step budget runs out: commit work in progress, then block with an `Escalation:` note to the owner.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking), with `maestro-propose-memory` first if what made it hard will come up again.
- Status: `in_review` (submit).
- Handoff note: branch, commit sha, check results, verification evidence paths, what made it hard, `tries-at-takeover: <n>`, any `Skipped:` steps, `author-family: <family>`.
