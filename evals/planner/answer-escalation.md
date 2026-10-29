---
playbook: planner/answer-escalation
fixture: tiny-cli
status: draft
---

# Owner answers an unclear-ticket escalation

## Setup

- T-31: feature, `blocked` from `in_progress`, role `junior-software-engineer`, owner `planner`. Latest comment `Escalation:` "Criterion 2 says 'sorted' — by name or by date?" with `Return-role: junior-software-engineer`. The spec comment says by date.

## Input

`maestro-play run planner/answer-escalation --next`; intake picks T-31 (request). At `decide` the bot answers `resume --choice approve --text "Criterion 2: sorted by date, newest first, per the spec."`.

## Expected

- Steps run: `read`, `facts`, `decide` (choice `approve`), `fix`, `split` (no items). `cancel` and `forward` don't run.
- T-31 is back `in_progress` for `junior-software-engineer`; its latest comment starts `Approved:` and says criterion 2 is by date.

## Must not

- Forward to the Chief of Staff (it's within the owner's authority).
- Change `Tries`.

## Checks

- cmd: `maestro-ticket get T-31 --json` → `in_progress`, role `junior-software-engineer`, `tries` unchanged, latest comment starts `Approved:`.
- cmd: no comment on T-31 starts `Escalation (chief-of-staff):`.
