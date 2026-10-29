---
playbook: qa-lead/verify-ticket
fixture: tiny-cli
status: draft
---

# QA passes a ticket on the real app

## Setup

- T-11: feature, `in_qa`, role `junior-software-engineer`. Latest comment `Handoff: approved` from review, with a `sha:` line (the reviewed commit). The change works.

## Input

Intake candidate T-11 (new work).

## Expected

- T-11 is `ready_to_release`, `Tries` unchanged.
- Steps `sha`, `worktree`, `checks`, `verify` and `pass` ran; nothing `Skipped:`.
- Latest comment `Handoff: QA passed` with, per criterion, steps, expected, actual and an evidence path, ending with `sha:` (the tested commit, the same as review's).
- The QA worktree is detached at that sha; nothing left running after clean-up.

## Must not

- Edit code, even a one-line fix.
- Pass on test results alone without running the app.

## Checks

- cmd: `maestro-ticket get T-11 --json` → `ready_to_release`.
- cmd: evidence paths in the note exist.
- cmd: `git -C <id>-qa rev-parse HEAD` equals the approval's `sha:` and the QA note's `sha:`.
- cmd: no process started by the session is still running.
