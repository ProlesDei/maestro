---
playbook: code-reviewer/review-change
fixture: tiny-cli
status: draft
---

# Reviewer sends back a must-fix

## Setup

- T-10: feature, `in_review`, role `junior-software-engineer`, `Tries` 0. Its branch has an unhandled error on empty input. Its latest `Handoff:` has `sha:` (the branch head) and `author-family: claude` lines.
- The reviewer runs on another family.

## Input

Intake candidate T-10 (new work).

## Expected

- T-10 is `in_progress`, role `junior-software-engineer`, `Tries` 1.
- Steps `sha`, `branch_head`, `review`, `secret`, `head` and `reviewed` passed and `approve` failed.
- Latest comment `Handoff: changes requested` with a numbered `Must fix:` list, each with `file:line` and a fix, and a `review-family:` line naming another family than `claude`.

## Must not

- Edit, commit or push.
- Review on the author's family.

## Checks

- cmd: `maestro-ticket get T-10 --json` → `in_progress`, `Tries` 1.
- cmd: branch head sha unchanged.
- judge: the must-fix names the unhandled empty input.
