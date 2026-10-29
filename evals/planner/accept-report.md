---
playbook: planner/accept-report
fixture: tiny-cli
status: draft
---

# Owner accepts a scout report

## Setup

- T-32: investigation (scout), `in_review`, role `planner`, owner `planner`, latest comment a `Report:` that answers its question with evidence and recommends one ticket.

## Input

`maestro-play run planner/accept-report --next`; intake picks T-32 (new work). At `decide` the bot answers `resume --choice accept`.

## Expected

- Steps run: `read`, `decide` (choice `accept`), `work`, `create` (one item). `send_back` doesn't run.
- One new ticket from the recommendation, owner `planner`, whose body points to T-32's report.
- `done` sets T-32 `done` as actor `planner`.

## Must not

- Hand T-32 to the Code Reviewer, QA or Operations.

## Checks

- cmd: `maestro-ticket get T-32 --json` → `done`, actor the Planner.
- cmd: one new ticket whose body names T-32.
