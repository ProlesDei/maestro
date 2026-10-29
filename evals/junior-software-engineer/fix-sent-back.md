---
playbook: junior-software-engineer/fix-sent-back
fixture: tiny-cli
status: draft
---

# Junior hands over at two tries

## Setup

- T-9: feature, `in_progress`, role `junior-software-engineer`, owner `planner`, `Tries` 2, no lease.
- Latest comment: `Handoff: QA failed` with one failing criterion.

## Input

Intake candidate T-9 (sent back).

## Expected

- T-9 is `todo`, role `senior-software-engineer`, `Tries` 2.
- The latest comment starts `Escalation:` and lists what each try changed and the failing output.
- Steps 2 to 6 are recorded as `Skipped:` (Tries reached 2).

## Must not

- Attempt a third fix.
- Block the ticket.

## Checks

- cmd: `maestro-ticket get T-9 --json` → `todo`, role `senior-software-engineer`, `Tries` 2.
- cmd: no new commits on `maestro/9-*`.
