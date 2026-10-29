---
name: verification-skill
description: Write the project's verification guide, `.maestro/project/verify.md`, which says how to build, start, drive and observe the real app, prove it on one behaviour, and submit it for review.
role: qa-lead
trigger: request
claims: todo
types: chore
---

# Write the verification guide

## Goal

The project has a verification guide, `.maestro/project/verify.md`, that `maestro-verify` follows to run the real app and check a behaviour, and it has been shown to work once.

## When to use

A `todo` ticket for role `qa-lead` titled `Verification guide`, created by `chief-of-staff/setup-project`, or when the existing guide no longer runs the app.

## Steps

### 1. Start and set up

- **Goal:** The ticket is in progress and has its own worktree.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <holder> --token <token> --idempotency-key <session-id>:start`, then `worktree add <id> --from <main branch>`
- **Done when:** Both exit `0`.
- **Evidence:** exit codes; branch and worktree path.

### 2. Find how the app runs

- **Goal:** The build, start, drive and observe commands, from the repo itself.
- **Role:** harness
- **Skill:** `maestro-draft-setup`
- **Model:** Mid-tier
- **Done when:** The scratchpad lists, each with its source path: how to build, start and stop the app, what it needs (services, seed data, env vars by name), how to drive it (browser, CLI, HTTP), and how to observe results (logs, screenshots, output).
- **Evidence:** paths read; the list.

A service or access the app needs isn't available: block.

### 3. Draft the guide

- **Goal:** `verify.md` drafted in the scratch dir.
- **Role:** harness
- **Skill:** `maestro-draft-setup`
- **Model:** Mid-tier
- **Done when:** The draft has the commands from step 2, says where evidence goes, and how to stop everything it starts.
- **Evidence:** the draft's path.

### 4. Prove it once

- **Goal:** The draft verifies one existing behaviour end to end.
- **Role:** harness
- **Skill:** `maestro-verify`
- **Model:** Mid-tier
- **Done when:** A fresh session, given only the draft in place of `verify.md`, built, started and drove the app, observed one behaviour named in `.maestro/project/project-setup.md` or the README, and stopped it.
- **Evidence:** commands and exit codes; evidence paths; nothing left running.

### 5. Install it on the branch

- **Goal:** The guide is in the worktree and in the manifest.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `install write .maestro/project/verify.md --from <draft> --commit`
- **Done when:** Exit `0`; the worktree is clean.
- **Evidence:** exit code; commit sha; the manifest entry.

## Stop and block when

- The app needs a service, account or data the team can't get: block with an `Escalation:` note to the owner saying exactly what.
- `install write` refuses because the file exists and differs: block with an `Escalation:` note; never overwrite.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: `in_review` (submit). It ships like code, and at trust level 1 as a pull request.
- Handoff note: branch, commit sha, the behaviour it was proved on with evidence paths, any `Skipped:` steps, `author-family: <family>`.
