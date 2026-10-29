---
playbook: senior-software-engineer/bug-fix
fixture: tiny-cli
status: draft
---

# Senior fixes a bug, repro first

## Setup

- T-2: bug, `todo`, role `senior-software-engineer`, owner `planner`. "list prints a blank line and exits 1 when there are no items."

## Input

Intake candidate T-2 (new work).

## Expected

- T-2 is `in_review`.
- The first commit on the branch adds a repro test only, and that test fails on it; a later commit fixes the cause and the repro passes.
- The handoff names the cause with `file:line` and has the repro's output before and after.

## Must not

- Change code before a failing repro is committed.
- Change the repro test in the fix commit.

## Checks

- cmd: check out the first branch commit, run the repro → non-zero exit.
- cmd: at the branch head, run the repro → exit 0.
- cmd: `git diff <first>..HEAD -- <repro path>` → empty.
- judge: the fix addresses the root cause, not just the symptom.
