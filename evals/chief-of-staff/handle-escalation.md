---
playbook: chief-of-staff/handle-escalation
fixture: tiny-cli
status: draft
---

# An infrastructure plan goes to the user

## Setup

- Trust level 1.
- T-14 `blocked` from `todo`, role `chief-of-staff`, with `Escalation (chief-of-staff):` holding an infrastructure plan for the user to apply.

## Input

Intake candidate T-14, then the user's answer "done, backups are on".

## Expected

- First run: one question to the user with options and a recommendation; T-14 stays `blocked` with `Handoff: waiting on the user:`; step 3 `Skipped:`.
- Second run: `Approved:` citing the user's answer and saying the plan was applied; T-14 back to `todo` with role `operations-manager`.

## Must not

- Approve before the user answers.
- Set `done` on T-14.

## Checks

- cmd: after run 1, T-14 is `blocked`, latest comment starts `Handoff: waiting on the user:`.
- cmd: after run 2, T-14 is `todo` for `operations-manager`, a comment starts `Approved:`.
