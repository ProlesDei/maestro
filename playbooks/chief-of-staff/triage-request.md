---
name: triage-request
description: Turn a user request into the right work, a ticket for a small task or a planning ticket for a big project, and tell the user what happens next.
role: chief-of-staff
trigger: request
claims: none
---

# Triage a request

## Goal

Every user request ends up as exactly one clear piece of work on the board, or a direct answer, without the user having to explain it twice.

## When to use

A request from the user arrives in the prompt. Not for escalations or approval answers on an existing ticket: use `handle-escalation`.

## Steps

### 1. Understand the request

- **Goal:** Know what the user wants, whether it's already on the board, and how big it is.
- **Skill:** none
- **Tier:** Frontier
- **Done when:** The request is classed as one of: answer only, small task, big project, infrastructure request. Related tickets are listed.
  - Small task: one ticket, one role, done is easy to test.
  - Big project: needs a spec or several tickets.
- **Evidence:** `maestro-ticket list --json` exit code; ids of related or duplicate tickets; `.maestro/Backlog.md` read.

Read summaries only: the backlog, tickets, `.maestro/project-setup.md`. Don't read code. If the request is unclear in a way that changes the work, stop and ask the user one short question with a recommended answer.

### 2. Write the ticket

- **Goal:** Put the work on the board for the right role.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** One ticket exists, created with `maestro-ticket create --title ... --body ... --role <R> --priority <0-3> --idempotency-key <session-id>:create`.
  - Small task: role `senior-software-engineer` if complex or risky, `junior-software-engineer` if routine. Body: goal, testable acceptance criteria, pointers, constraints.
  - Big project: role `planner`, title `Plan: <goal>`. Body: the goal, constraints, deadlines, what the Planner may decide on its own.
  - Infrastructure request: role `operations-manager`. Body: what's needed and why.
  - Duplicate: no new ticket; comment on the existing one instead.
- **Evidence:** the new ticket id and version.

Skip this step for answer only.

### 3. Tell the user

- **Goal:** The user knows what happens next.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** You've sent the user the ticket id, the role it went to and one line on what's next. No harness prompt.
- **Evidence:** the ticket id from step 2.

## Stop and block when

- The request needs a decision only the user can make: ask, and write no ticket until they answer.
- The request goes against `.maestro/rules.md` or the trust level: tell the user why, and write no ticket.

## Wrap-up

- Status: no change (nothing claimed). Log the created ticket id.
- Handoff note: none. The ticket body is the handoff.
