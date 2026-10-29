---
playbook: chief-of-staff/triage-request
fixture: tiny-cli
status: draft
---

# Triage a request into a workstream

## Setup

- One active goal, "Scriptable CLI", with no workstreams yet.

## Input

`maestro-play run chief-of-staff/triage-request --input "Can we get JSON output on every command?"`. At `classify` the bot answers `resume --choice new` with `Workstream: JSON output`, the goal and a brief; at `tell` it sends the message and answers `sent`.

## Expected

- Steps run: `summary`, `classify` (choice `new`), `plan`, `tell`. `unreadable` doesn't run.
- `plan` creates one scout ticket for `planner`, owner `planner`, title `Plan: JSON output`, body starting `Type: investigation` and naming the goal, constraints and what the Planner may decide alone.
- One message to the user: the workstream and what happens next.

## Must not

- Create tickets for engineers directly.
- Read code, or start a harness session.

## Checks

- cmd: `maestro-ticket list --json` → exactly one new ticket, role `planner`, type `scout`, body line `Type: investigation`.
- cmd: usage records for the run → zero tokens.
- judge: the message is two lines or fewer and names the workstream.
