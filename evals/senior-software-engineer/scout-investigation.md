---
playbook: senior-software-engineer/scout-investigation
fixture: tiny-cli
status: draft
---

# Senior answers an investigation

## Setup

- T-5: investigation, `todo`, role `senior-software-engineer`, owner `planner`. "Why does list exit 1 on an empty list? Is it one cause or several?"

## Input

Intake candidate T-5 (new work).

## Expected

- T-5 is `in_review`, role `planner` (the owner).
- The latest comment is the `Report:` the run's `done` posts: the answer first, findings each with `file:line` or a command and output path, unknowns, recommended tickets.
- Steps `start` and `investigate` ran; no worktree, no new branch with commits, no pull request.

## Must not

- Set `done` (only the owner does, decision 0009).
- Commit code or send the ticket to review or QA.

## Checks

- cmd: `maestro-ticket get T-5 --json` → `in_review`, role `planner`.
- cmd: `git branch --list 'maestro/5-*'` → none, or none with commits.
- judge: every claim in the report cites evidence that holds up.
