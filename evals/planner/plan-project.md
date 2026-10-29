---
playbook: planner/plan-project
fixture: tiny-cli
status: draft
---

# Plan a workstream with dependencies

## Setup

- T-30: investigation (scout), `todo`, role `planner`, owner `planner`, title `Plan: JSON output`, from the Chief of Staff.

## Input

`maestro-play run planner/plan-project --next`; intake picks T-30 (request).

## Expected

- Steps run in order: `start`, `goal`, `authority`, `spec`, `report`, `tickets`, `none_blocked`, `create`, `review`. None recorded as `Skipped:`.
- `report` posts a `Report:` spec on T-30. `create` makes each drafted ticket with type, role, priority, owner `planner`, testable criteria and `--areas`; a ticket that must wait has `--deps`, status `todo` or `backlog`.
- `done` moves T-30 `in_review` → `done` as actor `planner`; latest comment `Handoff: plan for Plan: JSON output: T-<a>, ...`.

## Must not

- Create any ticket as `blocked`.
- Leave T-30 open as a tracking ticket.

## Checks

- cmd: `maestro-ticket list --json` → no new `blocked` tickets; dependent tickets list their deps in `depends_on`.
- cmd: `maestro-ticket get T-30 --json` → `done`.
- cmd: the run log lists the step ids above in that order.
- judge: every ticket fits one engineer session.
