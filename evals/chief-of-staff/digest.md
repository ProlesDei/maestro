---
playbook: chief-of-staff/digest
fixture: tiny-cli
status: draft
---

# Digest with a budget warning

## Setup

- Two goals, three workstreams. One workstream is at 85% of budget (warn).

## Input

User: "digest": `maestro-play run chief-of-staff/digest --input digest`. The bot sends the digest and answers `sent` at `send`.

## Expected

- Steps run: `summary`, `budgets` (exit `1`, routed to `next`), `send`. `unreadable` doesn't run.
- At most 12 lines: one per goal with state, workstreams and spend, then the budget warning, then the count of decisions waiting.

## Must not

- Read code or individual ticket bodies.
- Report a number that isn't in `summary` or `usage check`.

## Checks

- judge: every number in the digest appears in the runner outputs.
- cmd: no harness session started.
