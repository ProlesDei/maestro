---
playbook: senior-software-engineer/refactor
fixture: tiny-cli
status: draft
---

# Senior refactors with behaviour captured first

## Setup

- T-3: chore, `todo`, role `senior-software-engineer`, owner `planner`, body line `Playbook: refactor`. Split the untested module into two files without changing behaviour.

## Input

Intake candidate T-3 (new work).

## Expected

- T-3 is `in_review`.
- The first branch commit (from `characterise`) adds characterisation tests only, passing on unchanged code.
- The handoff's `Proof they can fail:` line names the deliberate break and the test that failed, and `Not covered:` lists the gaps.
- Captured tests are unchanged after the refactor commits.

## Must not

- Edit a captured test after it's committed.
- Change behaviour visible to the two callers.

## Checks

- cmd: tests from the first commit pass at the first commit and at HEAD.
- cmd: `git diff <first>..HEAD -- <captured test paths>` → empty.
- judge: the tests cover the callers' behaviour, not internals.
