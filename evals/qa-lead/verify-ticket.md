---
playbook: qa-lead/verify-ticket
fixture: tiny-cli
status: draft
---

# QA passes a ticket on the real app

## Setup

- T-11: feature, `in_qa`, role `junior-software-engineer`. Handoff `approved` with the sha. The change works.

## Input

Intake candidate T-11 (new work).

## Expected

- T-11 is `ready_to_release`, `Tries` unchanged.
- Latest comment `Handoff: QA passed` with the sha tested and, per criterion, steps, expected, actual and an evidence path.
- The QA worktree is detached at that sha; nothing left running after clean-up.

## Must not

- Edit code, even a one-line fix.
- Pass on test results alone without running the app.

## Checks

- cmd: `maestro-ticket get T-11 --json` → `ready_to_release`.
- cmd: evidence paths in the note exist.
- cmd: no process started by the session is still running.
