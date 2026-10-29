---
name: infrastructure-request
description: Plan an infrastructure request, such as an environment, service or pipeline change, route it to an engineer or to the user to apply, and close it once verified.
role: operations-manager
trigger: request
claims: todo
types: chore
---

# Infrastructure request

## Goal

The infrastructure the team asked for is in place and verified, with a known way back. No runner request applies infrastructure, so Maestro never does: a change to files in the repo goes to an engineer, and anything else is applied by the user.

## When to use

A `todo` scout ticket (`Type: chore`) for role `operations-manager`, owned by `operations-manager`, that changes infrastructure: an environment, a service, a secret or a pipeline setting. Also the same ticket when it comes back to `todo` with an `Approved:` saying the user applied the plan.

## Steps

### 1. Plan

- **Goal:** A plan with risk, cost, the check that proves it and the rollback.
- **Role:** harness
- **Skill:** `maestro-plan-change`
- **Model:** Mid-tier
- **Skip when:** the latest comment is an `Approved:` saying the user applied the plan in the escalation note.
- **Done when:** The scratchpad has: what's needed and why, where it's done (per "Release" in `.maestro/project/project-setup.md`), whether it changes files in the repo, the exact commands for anything else, the runner request that proves it works (`check <name>` or `smoke <env>`), the rollback, and whether it touches production.
- **Evidence:** ticket version; paths read.

Changes files in the repo (CI config, infrastructure code): it's an engineer ticket. Create one for `senior-software-engineer` with the plan as its body, `Type: chore` and `--owner planner` (runner `ticket create`), then wrap up this ticket as `cancelled` with the note `Replaced by T-<n>`. Anything else, not yet applied: stop and block.

### 2. Start

- **Goal:** The ticket is in progress under your lease.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <holder> --token <token> --idempotency-key <session-id>:start`
- **Done when:** Exit `0`.
- **Evidence:** exit code; new version.

### 3. Verify

- **Goal:** It works.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** the request the plan names: `check <name>` or `smoke <env>`
- **Skip when:** the plan names no runner request. The user's `Approved:` must then include their evidence, and the handoff says it was verified by the user only.
- **Done when:** Exit `0`.
- **Evidence:** the request, its exit code and output path.

## Stop and block when

- The plan isn't applied yet: block with an `Escalation (chief-of-staff):` note (`maestro-escalate`) holding the plan: the exact commands for the user to run, risk, cost, rollback, and whether it touches production. Refer to secrets by name only.
- Step 3 fails: block with an `Escalation (chief-of-staff):` note with the output and the rollback for the user to run.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: `in_review`, then `done` with `--actor operations-manager`, after step 3 passed or was skipped with the user's evidence (it ships no code, so it's a scout ticket you own); `cancelled` when replaced by an engineer ticket; `blocked` with `--role chief-of-staff` while the user applies the plan.
- Handoff note: what changed, where, who applied it, how it was verified, how to roll it back, any `Skipped:` steps.
