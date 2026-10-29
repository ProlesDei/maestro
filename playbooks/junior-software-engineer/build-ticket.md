---
name: build-ticket
description: Build a routine, well-defined ticket by following an existing pattern in the code, and hand it to the Senior Engineer after two failed tries.
role: junior-software-engineer
trigger: new-work
claims: todo
---

# Build a ticket

## Goal

The ticket is done the way similar code in the repo is already done, with tests, on an `maestro/` branch, passing the project's checks.

## When to use

A `todo` ticket for role `junior-software-engineer`.

## Steps

### 1. Start and set up

- **Goal:** The ticket is in progress and has its own worktree.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** Both exit `0`:
  - `maestro-ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <H> --idempotency-key <session-id>:start`
  - `git worktree add -b maestro/<id>-<slug> $(git rev-parse --git-common-dir)/maestro/worktrees/<id> <main branch>`. `<slug>`: up to five lowercase words from the title, joined by hyphens. If the branch already exists, `git worktree add <path> maestro/<id>-<slug>`.
- **Evidence:** exit codes; new ticket version; branch and worktree path.

### 2. Find the pattern

- **Goal:** An existing example in the code to follow, and a plan.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** The scratchpad names the example (paths), the files to change, and how each acceptance criterion is proved. Branch and worktree are recorded.
- **Evidence:** paths read (the ticket, `.maestro/project-setup.md`, `.maestro/memory/`, the example).

No pattern to follow, or the ticket needs a design decision: escalate now. That isn't a failed try.

### 3. Build

- **Goal:** The change and its tests, committed on the branch.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** Work is committed; `git -C <worktree> status --porcelain` is empty. The diff follows the example and touches only what the ticket needs.
- **Evidence:** commit sha; files changed; tests added.

### 4. Run the checks

- **Goal:** The project's checks pass on the branch.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** Build, lint and test from `.maestro/project-setup.md` all exit `0` in the worktree.
- **Evidence:** each command, exit code and test counts.

A pass through steps 3 and 4 that ends with failing checks is a failed try. After the first, go back to step 3. After the second, escalate.

## Stop and block when

- The ticket is unclear: comment the exact question and block.
- Retries or budget run out before two tries: block.

## Escalate to the Senior Engineer

1. Commit work in progress on the branch so the worktree is clean.
2. Wrap up with status `todo` and `--role senior-software-engineer`.
3. Start the note `Escalation:` and say what you tried each time, the failing output, the branch, and what you think is wrong.

## Wrap-up

- Status: `in_review` (submit), or `todo` with `--role senior-software-engineer` when escalating.
- Handoff note: branch, commit sha, check results with exit codes and test counts, how each criterion is met, the example followed, `author-family: <family>`.
