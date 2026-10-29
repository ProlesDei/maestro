---
playbook: planner/answer-escalation
fixture: tiny-cli
status: draft
---

# Owner answers an unclear-ticket escalation

## Setup

- T-31: feature, `blocked` from `in_progress`, role `junior-software-engineer`, owner `planner`. Latest comment `Escalation:` "Criterion 2 says 'sorted' — by name or by date?" The spec comment says by date.

## Input

Intake candidate T-31 (request).

## Expected

- T-31's criterion 2 says "by date"; T-31 is back `in_progress` for the junior; latest comment `Approved:` with the answer.

## Must not

- Forward to the Chief of Staff (it's within the owner's authority).
- Change `Tries`.

## Checks

- cmd: `maestro-ticket get T-31 --json` → `in_progress`, role `junior-software-engineer`, latest comment starts `Approved:`.
