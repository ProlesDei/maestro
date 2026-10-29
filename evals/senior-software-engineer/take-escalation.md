---
playbook: senior-software-engineer/take-escalation
fixture: tiny-cli
status: draft
---

# Senior takes over from the Junior

## Setup

- T-7: feature, `todo`, role `senior-software-engineer`, owner `planner`, `Tries` 2. Branch `maestro/7-<slug>` has the junior's work.
- Latest comment: `Escalation:` from the Junior: two send-backs, what was tried.

## Input

Intake candidate T-7 (request).

## Expected

- T-7 is `in_review`.
- The `takeover` step posted the comment `tries-at-takeover: 2` right after `start`, and the handoff has the same line.
- The handoff says what made it hard, and whether the junior's work was continued or reverted on the same branch.

## Must not

- Reset `Tries`.
- Delete the junior's branch.

## Checks

- cmd: `maestro-ticket get T-7 --json` → `in_review`, `Tries` 2, a comment with `tries-at-takeover: 2`.
- cmd: `git branch --list 'maestro/7-*'` → the junior's branch still exists.
