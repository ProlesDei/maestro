---
playbook: planner/accept-report
fixture: tiny-cli
status: draft
---

# Owner accepts a scout report

## Setup

- T-32: investigation, `in_review`, role `planner`, owner `planner`, with a `Report:` that answers its question with evidence and recommends one ticket.

## Input

Intake candidate T-32 (new work).

## Expected

- One new ticket from the recommendation, pointing to the report.
- T-32 is `done`, set by the Planner.

## Must not

- Hand T-32 to the Code Reviewer, QA or Operations.

## Checks

- cmd: `maestro-ticket get T-32 --json` → `done`, actor the Planner.
- cmd: one new ticket whose body links T-32.
