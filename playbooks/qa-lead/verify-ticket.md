---
name: verify-ticket
description: Prove a reviewed ticket does what it says by running the tests and the app, and send failures back with a clear report.
role: qa-lead
trigger: new-work
claims: in_qa
---

# Verify a ticket

## Goal

Each acceptance criterion is shown to work in the running app, or the engineer gets a failure report they can reproduce in one go.

## When to use

An `in_qa` ticket.

## Steps

### 1. Set up

- **Goal:** A separate checkout of exactly what was reviewed.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** `git worktree add --detach $(git rev-parse --git-common-dir)/agent-team/worktrees/<id>-qa <sha>` exits `0`. `<sha>` is the commit in the latest handoff note.
- **Evidence:** exit code; path and sha.

### 2. Run the checks

- **Goal:** The project's checks pass on what you'll test.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** Build and test from `.agent-team/project-setup.md` all exit `0` in the QA worktree.
- **Evidence:** each command, exit code and test counts.

A failure here is a QA fail. Skip to wrap-up with the output.

### 3. Verify each criterion

- **Goal:** Each acceptance criterion tried in the running app.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** The app was run the way `.agent-team/project-setup.md` says, and each criterion has: steps taken, expected, actual, pass or fail. The QA worktree, and the PID and command of anything started, are in the scratchpad so clean-up can stop it.
- **Evidence:** commands run and exit codes; output or screenshots by path; the per-criterion results.

Read-only code. Don't edit or fix anything, even a one-line fix.

### 4. Decide

- **Goal:** Pass or fail.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** Pass if and only if the checks passed and every criterion passed.
- **Evidence:** the count of failed criteria.

## Stop and block when

- The app can't be run the way `.agent-team/project-setup.md` says (missing service, access, data). Block with what's missing. That's not the engineer's failure.
- A criterion can't be tested as written: block and ask the Planner in the note.

## Wrap-up

- Status: pass → `ready_to_release`; fail → `in_progress` (the role stays the author's).
- Handoff note: `Handoff: QA passed` with the per-criterion results, or `Handoff: QA failed` with, for each failure, the criterion, steps to reproduce, expected, actual, and where the output is.
