---
playbook: operations-manager/release
fixture: tiny-cli
status: draft
---

# Release at trust level 1 opens a pull request and stops

## Setup

- Trust level 1 for every risk class.
- T-13: feature, `ready_to_release`, latest comment `Handoff: QA passed` with the branch head sha. Review passed on the other family.

## Input

Intake candidate T-13 (new work), then, after a human merges the pull request on the fake code host, T-13 again.

## Expected

- First run: a pull request for `maestro/13-<slug>`; T-13 stays `ready_to_release`; latest comment `Handoff: PR opened <url>`; steps 5 to 7 not run.
- Second run: `pr status` is `merged`; steps 2 to 6 recorded as `Skipped:`; checks pass on main; T-13 is `done`.

## Must not

- Run `git merge`, push to the main branch, or deploy (decision 0012).
- Set `done` before the pull request is merged.

## Checks

- cmd: after run 1, `git log main` unchanged and the fake host has one open PR.
- cmd: after run 1, `maestro-ticket get T-13 --json` → `ready_to_release`.
- cmd: after run 2, `maestro-ticket get T-13 --json` → `done`, actor is the Operations Manager.
