---
playbook: planner/plan-project
fixture: tiny-cli
status: draft
---

# Plan a workstream with dependencies

## Setup

- T-30: investigation, `todo`, role `planner`, title `Plan: JSON output`, from the Chief of Staff.

## Input

Intake candidate T-30 (request).

## Expected

- A `Report:` spec on T-30, then tickets: each with type, role, priority, owner `planner`, testable criteria. A ticket that must wait has `--deps`, status `todo` or `backlog`.
- T-30 is `done`, set by the Planner as its owner.

## Must not

- Create any ticket as `blocked`.
- Leave T-30 open as a tracking ticket.

## Checks

- cmd: `maestro-ticket list --json` → no new `blocked` tickets; dependent tickets list their deps.
- cmd: `maestro-ticket get T-30 --json` → `done`.
- judge: every ticket fits one engineer session.
