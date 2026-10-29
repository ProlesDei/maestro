---
name: infrastructure-request
description: Carry out an infrastructure request from the Chief of Staff, such as an environment, service or pipeline change, safely and with a rollback.
role: operations-manager
trigger: request
claims: todo
---

# Infrastructure request

## Goal

The infrastructure the team asked for is in place and verified, with a known way back.

## When to use

A `todo` ticket for role `operations-manager`, usually from the Chief of Staff.

## Steps

### 1. Understand and plan

- **Goal:** A plan with risk, cost and rollback.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** The scratchpad has: what's needed and why, where it's done (per `.agent-team/project-setup.md`), the exact commands, the check that proves it works, the rollback, and whether it touches production.
- **Evidence:** ticket id and version read; paths read.

Needs code changes in the repo: it's an engineer ticket. Create one for `senior-software-engineer` with the plan as its body, then wrap up this ticket as `cancelled` with the note `Replaced by T-<n>`.

### 2. Start

- **Goal:** The ticket is in progress under your lease.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** `at-ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <H> --idempotency-key <session-id>:start` exits `0`.
- **Evidence:** exit code; new version.

### 3. Make the change (Gate)

- **Goal:** The change is applied.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** Each planned command exits `0`. Anything touching production has an `Approved:` comment from the Chief of Staff first.
- **Evidence:** each command and exit code.

Refer to secrets by name only. Never put a secret in a command, comment or log.

### 4. Verify

- **Goal:** It works.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** The check from step 1 exits `0`.
- **Evidence:** the command, exit code and output summary.

### 5. Close

- **Goal:** The ticket is ready to close.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** It is still `in_progress` under your lease, ready for the no-code close in the playbooks README (`in_progress` → `done` at wrap-up).
- **Evidence:** each transition's exit code and version.

## Stop and block when

- It touches production and has no approval: comment `Escalation:` with the plan, risk, cost and rollback, and block.
- A command fails: run the rollback, then block with the output.

## Wrap-up

- Status: `done`, or `cancelled` when replaced by an engineer ticket.
- Handoff note: what changed, where, how it was verified, and how to roll it back.
