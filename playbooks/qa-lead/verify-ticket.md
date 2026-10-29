---
name: verify-ticket
description: Prove a reviewed ticket does what it says by running the checks and the real app, and send failures back with a clear report.
role: qa-lead
trigger: new-work
claims: in_qa
types: feature, bug, chore, docs
---

# Verify a ticket

## Goal

Each acceptance criterion is shown to work in the running app, or the engineer gets a failure report they can reproduce in one go.

## When to use

An `in_qa` ticket.

## Steps

### 1. Set up

- **Goal:** A separate checkout of exactly what was reviewed.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `worktree add <id> --detach <sha>`, where `<sha>` is the commit in the latest handoff note. It lands at `<id>-qa`.
- **Done when:** Exit `0`.
- **Evidence:** exit code; path and sha.

### 2. Run the checks

- **Goal:** The project's checks pass on what you'll test.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `check all --in <id>-qa`
- **Done when:** Every check exits `0`.
- **Evidence:** each check, exit code and test counts.

A failure here is a QA fail. Record steps 3 and 4 as `Skipped: checks failed`, and wrap up with the output.

### 3. Verify each criterion

- **Goal:** Each acceptance criterion tried in the running app.
- **Role:** harness
- **Skill:** `maestro-verify`
- **Model:** Mid-tier
- **Done when:** The app was run the way `.maestro/project/verify.md` says, and each criterion has: steps taken, expected, actual, pass or fail. Anything started is in the scratchpad so clean-up can stop it.
- **Evidence:** commands run and exit codes; output or screenshots by path; the per-criterion results.

Read-only code. Don't edit or fix anything, even a one-line fix.

### 4. Decide

- **Goal:** Pass or fail.
- **Role:** bot
- **Skill:** `none`
- **Model:** Bot
- **Done when:** Pass if and only if the checks passed and every criterion passed.
- **Evidence:** the count of failed criteria.

## Stop and block when

- The app can't be run the way the project says (missing service, access, data). That's not the engineer's failure: block with an `Escalation:` note to the owner saying what's missing.
- A criterion can't be tested as written: block with an `Escalation:` note to the owner asking for a testable one.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: pass → `ready_to_release`; fail → `in_progress` (maestro-ticket adds one to `Tries`; the role stays the author's).
- Handoff note: `Handoff: QA passed` with the per-criterion results and the sha tested, or `Handoff: QA failed` with, for each failure, the criterion, steps to reproduce, expected, actual, and where the output is. Any `Skipped:` steps.
