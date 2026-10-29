---
name: build-ticket
description: Build a complex or risky ticket on its own maestro/ branch, with tests that prove each acceptance criterion, and submit it for review.
role: senior-software-engineer
trigger: new-work
claims: todo
---

# Build a ticket

## Goal

Clean, well-tested code that does exactly what the ticket asks, on an `maestro/` branch, passing the project's checks.

## When to use

A `todo` ticket for role `senior-software-engineer`. For a ticket escalated by the Junior Engineer, use `take-escalation`.

## Steps

### 1. Start and set up

- **Goal:** The ticket is in progress and has its own worktree.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** Both exit `0`:
  - `maestro-ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <H> --idempotency-key <session-id>:start`
  - `git worktree add -b maestro/<id>-<slug> $(git rev-parse --git-common-dir)/maestro/worktrees/<id> <main branch>`. `<slug>`: up to five lowercase words from the title, joined by hyphens. If the branch already exists, `git worktree add <path> maestro/<id>-<slug>`.
- **Evidence:** exit codes; new ticket version; branch and worktree path.

### 2. Plan

- **Goal:** A plan that maps every acceptance criterion to a test or check, and names the risks.
- **Skill:** none
- **Tier:** Frontier
- **Done when:** The plan is in the scratchpad, with the branch and worktree recorded. It lists files to change, the approach, each criterion with its proof, and each risk with how it's covered.
- **Evidence:** paths read (the ticket, any spec it points to, `.maestro/project-setup.md`, `.maestro/memory/`, the code); the scratchpad path.

### 3. Build

- **Goal:** The change and its tests, committed on the branch.
- **Skill:** none
- **Tier:** Frontier
- **Done when:** Work is committed in the worktree; `git -C <worktree> status --porcelain` is empty. The diff touches only what the ticket needs and matches the code around it.
- **Evidence:** commit sha; files changed; tests added.

Heartbeat the lease during long builds.

### 4. Run the checks

- **Goal:** The project's checks pass on the branch.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** Build, lint and test from `.maestro/project-setup.md` all exit `0` in the worktree.
- **Evidence:** each command, exit code and test counts.

On a failure, go back to step 3 with a pointer to the failing output. That counts as a retry.

## Stop and block when

- The ticket is unclear or contradicts the code or another ticket: comment the exact question and block. If it needs a decision above your authority, start the comment `Escalation:`.
- Retries or budget run out. Commit work in progress first so the next engineer can see it.

## Wrap-up

- Status: `in_review` (submit).
- Handoff note: branch, commit sha, check results with exit codes and test counts, how each criterion is met, risks and how they're covered, `author-family: <family>`.
