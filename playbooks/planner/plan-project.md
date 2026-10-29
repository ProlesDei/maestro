---
name: plan-project
description: Turn a workstream's planning ticket from the Chief of Staff into a short spec and ordered, testable tickets with dependencies, each routed to the right role.
role: planner
trigger: request
claims: todo
types: investigation
---

# Plan a workstream

## Goal

Engineers can finish every ticket without asking a question. Each ticket is small, testable, routed to the right role, and waits on others only through dependencies.

## When to use

A `todo` investigation ticket for role `planner` titled `Plan: <workstream>`, written by the Chief of Staff for a new workstream, or for new work in an existing one. This Planner session owns that one workstream, and you are the ticket's owner: it stands in for the workstream until the store has workstreams (decision 0504).

## Steps

### 1. Start

- **Goal:** The ticket is in progress under your lease.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <holder> --token <token> --idempotency-key <session-id>:start`
- **Done when:** Exit `0`.
- **Evidence:** exit code; new version.

### 2. Understand the goal

- **Goal:** Know what success looks like, the constraints, and what's already there.
- **Role:** harness
- **Skill:** `maestro-investigate`
- **Model:** Mid-tier
- **Done when:** The reply lists the outcome, constraints, affected areas of the code and open questions. Each open question is marked "I can decide" or "above my authority".
- **Evidence:** paths read in `.maestro/project/project-setup.md`, `.maestro/project/memory/` and the code (read only).

Any question above your authority: stop and block.

### 3. Write the spec

- **Goal:** One short spec the tickets can point to.
- **Role:** harness
- **Skill:** `maestro-write-report`
- **Model:** Mid-tier
- **Done when:** A `Report:` comment on this ticket holds the spec: outcome, scope and non-goals, approach, risks, order of work.
- **Evidence:** comment exit code.

### 4. Write the tickets

- **Goal:** Tickets that each fit in one engineer session.
- **Role:** harness
- **Skill:** `maestro-write-tickets`
- **Model:** Mid-tier
- **Done when:** Each ticket is created with a type, role, priority, `--owner planner`, the code areas it touches (`--areas`), and its dependencies (`--deps`). Each body has a goal, testable acceptance criteria, pointers (files, the spec comment), constraints, and `Playbook: refactor` or `Playbook: performance` where that applies. A ticket that only waits on others goes in `todo` with `--deps`; maestro-ticket won't hand it out before they're `done`. Tickets that aren't ready for another reason go in `backlog`. No ticket is created `blocked`.
- **Evidence:** every new ticket id, type, role, priority and dependencies.

### 5. Check the board

- **Goal:** The plan is on the board as intended.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket list --json`
- **Done when:** Exit `0`; every ticket from step 4 is listed with its dependencies.
- **Evidence:** exit code; ticket count.

## Stop and block when

- A question is above your authority (scope, cost, risk, a product decision): block with an `Escalation (chief-of-staff):` note (`maestro-escalate`) with the question, options and your recommendation. You are the owner here, so it goes straight to the Chief of Staff.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: `in_review`, then `done`: you are this scout's owner and have read the report (decision 0009). The workstream's progress is tracked from its tickets' events, not by keeping this ticket open.
- Handoff note: `Handoff: plan for <workstream>: T-<a>..T-<b>`, the order of work, any `Skipped:` steps.
