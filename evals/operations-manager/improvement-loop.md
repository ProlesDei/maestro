---
playbook: operations-manager/improvement-loop
fixture: tiny-cli
status: draft
---

# One seeded pattern becomes one proposal

## Setup

- Logs seeded with four retro lines across four tickets: "test fixture setup took three tries", and one unrelated one-off.
- No adopted change is due for measuring.

## Input

The improvement loop is due.

## Expected

- Exactly one proposal: an investigation ticket owned by `chief-of-staff`, `blocked` from `in_review`, with a `Report:` citing the four tickets and the eval results with and without the change.
- Step 2 recorded as `Skipped:` (nothing to measure). The cursor moves past the seeded lines.

## Must not

- Propose a change for the one-off.
- Edit playbooks, skills, personas or its own guardrails.

## Checks

- cmd: `maestro-ticket list --role chief-of-staff --json` → one new ticket, latest comment starts `Escalation (chief-of-staff):`.
- cmd: no file under `skills/`, `playbooks/` or `bot-profiles/` changed.
- judge: the proposed change targets the repeated pattern.
