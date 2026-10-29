---
name: plan-project
description: Turn a planning ticket from the Chief of Staff into a short spec and ordered, testable tickets, each routed to the right role.
role: planner
trigger: request
claims: todo
---

# Plan a project

## Goal

Engineers can finish every ticket without asking a question. Each ticket is small, ordered, testable and routed to the right role.

## When to use

A `todo` ticket for role `planner`, usually titled `Plan: <goal>`, written by the Chief of Staff.

## Steps

### 1. Understand the goal

- **Goal:** Know what success looks like, the constraints, and what's already there.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** The reply lists the outcome, constraints, affected areas of the code and open questions. Each open question is marked "I can decide" or "above my authority".
- **Evidence:** ticket id and version read; paths read in `.maestro/project-setup.md`, `.maestro/memory/` and the code (read only).

If any question is above your authority, stop and block.

### 2. Write the spec

- **Goal:** One short spec the tickets can point to.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** `maestro-ticket comment <id> --body "Spec: ..." --idempotency-key <session-id>:spec` is posted. The spec covers: outcome, scope and non-goals, approach, risks, order of work.
- **Evidence:** comment exit code.

### 3. Write the tickets

- **Goal:** Ordered tickets that each fit in one engineer session.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** Each ticket is created with `maestro-ticket create --title ... --body-file <file> --role <R> --priority <P> --idempotency-key <session-id>:create-<n>`. Write the body files in the scratch temp dir. Each body has: goal, testable acceptance criteria, pointers (files, the spec comment on this ticket), constraints.
  - Complex or risky: `senior-software-engineer`. Routine, with an existing pattern to follow: `junior-software-engineer`. Infrastructure: `operations-manager`.
  - Order by priority. A ticket that must wait for another is created with `--status blocked` and a body line `Waiting on: T-<n>`.
- **Evidence:** every new ticket id, role and priority.

### 4. Check the board

- **Goal:** The plan is on the board as intended.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** `maestro-ticket list --json` shows every ticket from step 3; `maestro-ticket render` exits `0`.
- **Evidence:** both exit codes; ticket count.

## Stop and block when

- A question is above your authority (scope, cost, risk, a product decision). Comment `Escalation: <question, options, your recommendation>` and block. The Chief of Staff picks it up.

## Wrap-up

- Status: `blocked`, as a tracking ticket. Groom-board closes it when all its tickets are done.
- Handoff note: `Handoff: tracking T-<a>..T-<b>; closes when they're done`, plus the order of work.
