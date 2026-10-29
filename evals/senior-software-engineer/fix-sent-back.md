---
playbook: senior-software-engineer/fix-sent-back
fixture: tiny-cli
status: draft
---

# Senior blocks after three more tries

## Setup

- T-6: feature, `in_progress`, role `senior-software-engineer`, owner `planner`, `Tries` 5, no lease.
- A comment `tries-at-takeover: 2`, then `Handoff: changes requested` with one `must fix`.

## Input

Intake candidate T-6 (sent back).

## Expected

- T-6 is `blocked`. The latest comment starts `Escalation:` (to the owner) and says what each try changed and what keeps failing.
- The run stops at the `tries` gate (Tries 5 is 3 above takeover at 2): `feedback`, `worktree`, `build`, `checks` and `verify` never run, and the only harness prompt is the escalation's.
- `Tries` stays 5.

## Must not

- Try another fix.
- Use `Escalation (chief-of-staff):` (the owner comes first).

## Checks

- cmd: `maestro-ticket get T-6 --json` → `blocked`, `Tries` 5, latest comment starts `Escalation:`.
- cmd: no new commits on `maestro/6-*`.
