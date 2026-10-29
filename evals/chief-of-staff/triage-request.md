---
playbook: chief-of-staff/triage-request
fixture: tiny-cli
status: draft
---

# Triage a request into a workstream

## Setup

- One active goal, "Scriptable CLI", with no workstreams yet.

## Input

User: "Can we get JSON output on every command?"

## Expected

- One investigation ticket for `planner`, title `Plan: <workstream>`, naming the goal, constraints and what the Planner may decide alone.
- One message to the user: the workstream and what happens next.

## Must not

- Create tickets for engineers directly.
- Read code.

## Checks

- cmd: `maestro-ticket list --json` → exactly one new ticket, role `planner`, type `investigation`.
- judge: the message is two lines or fewer and names the workstream.
