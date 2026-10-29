---
name: take-escalation
description: Take over a ticket the Junior Engineer escalated after two failed tries, find what went wrong, finish it and submit it.
role: senior-software-engineer
trigger: request
claims: todo
---

# Take an escalation

## Goal

The escalated ticket is finished and in review, and the reason it was hard is written down so the next one goes faster.

## When to use

A `todo` ticket for role `senior-software-engineer` whose latest comment starts `Escalation:`, usually from the Junior Engineer after two failed tries.

## Steps

### 1. Start

- **Goal:** The ticket is in progress under your lease.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** `maestro-ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <H> --idempotency-key <session-id>:start` exits `0`.
- **Evidence:** exit code; new version.

### 2. Read the escalation

- **Goal:** Know why it failed and whether the existing branch is worth keeping.
- **Skill:** none
- **Tier:** Frontier
- **Done when:** The cause is named, and you've decided: continue on `maestro/<id>-<slug>`, or start over from the main branch. Plan and decision are in the scratchpad.
- **Evidence:** the escalation comment; `git log` and `git diff <main branch>...maestro/<id>-<slug>` exit codes; the failing check output it points to.

### 3. Set up the worktree

- **Goal:** A worktree on the branch you chose.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** `$(git rev-parse --git-common-dir)/maestro/worktrees/<id>` exists. Continue: `git worktree add <path> maestro/<id>-<slug>`. Start over: leave the old branch as it is and run `git worktree add -b maestro/<id>-<new-slug> <path> <main branch>`. Name the old branch in the handoff note.
- **Evidence:** exit code; branch and path.

### 4. Build

- **Goal:** The change and its tests, committed.
- **Skill:** none
- **Tier:** Frontier
- **Done when:** Work is committed; `git -C <worktree> status --porcelain` is empty; every acceptance criterion has a test or check.
- **Evidence:** commit sha; files changed; tests added.

### 5. Run the checks

- **Goal:** The project's checks pass on the branch.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** Build, lint and test from `.maestro/project-setup.md` all exit `0` in the worktree.
- **Evidence:** each command, exit code and test counts.

On a failure, go back to step 4. That counts as a retry.

## Stop and block when

- The ticket itself is wrong or too big: comment `Escalation:` with what should change and block. The Chief of Staff picks it up.
- Retries or budget run out. Commit work in progress first.

## Wrap-up

- Status: `in_review` (submit).
- Handoff note: branch, commit sha, check results, what made it hard, `author-family: <family>`.
- Memory proposal: if what made it hard will come up again, propose it.
