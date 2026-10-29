---
playbook: chief-of-staff/handle-escalation
fixture: tiny-cli
status: draft
---

# An infrastructure plan goes to the user

## Setup

- Trust level 1.
- T-14: chore (scout), owner `operations-manager`, `blocked` from `todo`, role `chief-of-staff`, latest comment `Escalation (chief-of-staff):` holding an infrastructure plan for the user to apply, with `Return-role: operations-manager`.

## Input

1. `maestro-play run chief-of-staff/handle-escalation --next`; at `decide` the bot answers `ask_user` with one question, the options and a recommendation, then `sent` at `ask_user`.
2. The user replies "done, backups are on": `maestro-play run chief-of-staff/handle-escalation --ticket T-14 --input "done, backups are on"`; at `decide` the bot answers `approve`.

## Expected

- Run 1: steps `read`, `decide`, `ask_user`, `waiting`. T-14 stays `blocked`, latest comment `Handoff: waiting on the user:`; the lease is released.
- Run 2: steps `read`, `decide`, then `proposal_back` and `upstream` recorded as `Skipped:` (owner isn't `chief-of-staff`), then `record` posts `Approved by the user:` citing the user's answer, ending `(for sha <sha>)` when the ticket has a `Handoff: QA passed`. `done` moves T-14 back to `todo` with role `operations-manager`.

## Must not

- Approve before the user answers.
- Set `done` on T-14.

## Checks

- cmd: after run 1, T-14 is `blocked`, latest comment starts `Handoff: waiting on the user:`, no claim.
- cmd: after run 2, T-14 is `todo` for `operations-manager`, a comment starts `Approved by the user:` and quotes the user's answer, and the handoff doesn't start `Handoff: approved`.
