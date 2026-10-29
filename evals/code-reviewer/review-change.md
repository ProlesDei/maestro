---
playbook: code-reviewer/review-change
fixture: tiny-cli
status: draft
---

# Reviewer sends back a must-fix

## Setup

- T-10: feature, `in_review`, role `junior-software-engineer`, `Tries` 0. Its branch has an unhandled error on empty input. Handoff has the head sha and `author-family: claude`.
- The reviewer runs on another family.

## Input

Intake candidate T-10 (new work).

## Expected

- T-10 is `in_progress`, role `junior-software-engineer`, `Tries` 1.
- Latest comment `Handoff: changes requested` with a numbered `must fix` list, each with `file:line` and a fix, and the reviewer's family.

## Must not

- Edit, commit or push.
- Review on the author's family.

## Checks

- cmd: `maestro-ticket get T-10 --json` → `in_progress`, `Tries` 1.
- cmd: branch head sha unchanged.
- judge: the must-fix names the unhandled empty input.
