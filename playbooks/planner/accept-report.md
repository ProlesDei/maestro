---
name: accept-report
description: As an investigation ticket's owner, read its report, turn the findings into tickets, and set it done, or send it back when the report doesn't answer the question.
role: planner
trigger: new-work
claims: in_review
types: investigation
---

# Accept a report

## Goal

Every scout ends with its owner having read the answer and acted on it. The owner, not the Operations Manager, closes it (decision 0009).

## When to use

An `in_review` investigation ticket for role `planner` that you own, with a `Report:` comment. Ship tickets in `in_review` go to the Code Reviewer.

## Steps

### 1. Read the report

- **Goal:** Know whether the report answers the ticket's question with evidence.
- **Role:** harness
- **Skill:** `maestro-read-feedback`
- **Model:** Mid-tier
- **Done when:** The reply gives the question, the answer, and for each finding whether its evidence holds up (spot-check at least one by reading the file or output it cites).
- **Evidence:** the `Report:` comment; what was spot-checked and the result.

### 2. Decide

- **Goal:** Accept, or send back.
- **Role:** bot
- **Skill:** `none`
- **Model:** Bot
- **Done when:** Accept if the question is answered and the evidence holds. Otherwise send back with what's missing.
- **Evidence:** the verdict and the reason.

### 3. Act on the findings

- **Goal:** The recommended next work is on the board.
- **Role:** harness
- **Skill:** `maestro-write-tickets`
- **Model:** Mid-tier
- **Skip when:** sent back, or the report recommends no new work.
- **Done when:** Each recommended ticket you agree with is created with type, role, priority, `--owner planner`, dependencies, and a pointer to the report.
- **Evidence:** new ticket ids.

## Stop and block when

- The findings need a decision above your authority: block with an `Escalation (chief-of-staff):` note with the question and your recommendation.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: accepted → `done`; sent back → `in_progress` for the author's role. A scout send-back doesn't add a try.
- Handoff note: the verdict, the tickets created, or what the report is missing. Any `Skipped:` steps.
