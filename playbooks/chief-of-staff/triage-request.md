---
name: triage-request
description: Turn a user request into a direct answer or a workstream for the Planner, and tell the user what happens next.
role: chief-of-staff
trigger: request
claims: none
types: none
---

# Triage a request

## Goal

Every user request ends up as a direct answer or as clear work for one workstream's Planner, without the user having to explain it twice. You manage goals and workstreams, never individual tickets.

## When to use

A request from the user arrives in the prompt. Not for answers to an open escalation (use `handle-escalation`), or for a digest or recap (use `digest` or `recap`).

## Steps

### 1. Read the summaries

- **Goal:** Know what's already under way.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `summary`
- **Done when:** Exit `0` with the active goals, workstreams and spend.
- **Evidence:** exit code; the workstream ids read.

### 2. Understand the request

- **Goal:** Know what the user wants, which goal it serves, and whether a workstream already covers it.
- **Role:** bot
- **Skill:** `none`
- **Model:** Bot
- **Done when:** The request is classed as one of: answer only; new work for an existing workstream (including `small-tasks` for a small ask that fits no goal); a new workstream. The goal it serves is named, or it is flagged as outside the active bets.
- **Evidence:** the class; the goal and workstream ids.

Unclear in a way that changes the work, or outside the active bets: ask the user one short question with a recommended answer (`talk-to-user`) and stop until they answer.

### 3. Hand it to a Planner

- **Goal:** The work is with the Planner who owns the workstream.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket create --type scout --owner planner --role planner --title "Plan: <workstream>" --body "Type: investigation. <goal, constraints, deadlines, what the Planner may decide alone>" --idempotency-key <session-id>:plan`
- **Skip when:** the request is answer only.
- **Done when:** Exit `0` with the new ticket id. For an existing workstream, the body names it; a small ask that fits no goal names `small-tasks`. This `Plan:` ticket is the only ticket you write: it stands in for the workstream (decision 0504).
- **Evidence:** the new ticket id and version.

### 4. Tell the user

- **Goal:** The user knows what happens next.
- **Role:** bot
- **Skill:** `talk-to-user`
- **Model:** Bot
- **Done when:** You've sent the answer, or the workstream and one line on what's next.
- **Evidence:** the message sent; the ticket id from step 3, if any.

## Stop and block when

- The request needs a decision only the user can make: ask, and create nothing until they answer.
- The request goes against `.maestro/rules.md` or the trust level: tell the user why, and create nothing.

## Wrap-up

- Skill: `maestro-log-entry` (nothing claimed, no status change). Log the class, the ticket id if any, and any `Skipped:` steps.
- Handoff note: none. The planning ticket's body is the handoff.
