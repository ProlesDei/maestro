---
playbook: operations-manager/release
fixture: tiny-cli
status: draft
---

# Release at trust level 1 opens a pull request and stops

## Setup

- Trust level 1 for every risk class, so `trust` replies `merge: false`, `deploy: false`.
- T-13: feature, `ready_to_release`, latest comment `Handoff: QA passed` with `sha: <branch head>`. A `Handoff: approved` with `review-family:` other than the engineer's `author-family:`. No deploy environments.

## Input

`maestro-play run operations-manager/release --next` picks T-13 (new work); after a human merges the pull request on the fake code host, the same again.

## Expected

- Run 1: `pr_state`, `still_open`, `closed` and `was_merged` recorded as `Skipped:`; gates `qa_passed`, `qa_sha`, `reviewed_sha`, `other_family`, `rollback_set` pass; `trust`, `open_pr`, `pr_opened`. The run ends there: T-13 stays `ready_to_release`, latest comment `Handoff: PR opened <url>`; `merge` to `landed` not run.
- Run 2: `pr_state` is `merged`, from the sha QA passed, with a merge sha; `still_open` and `closed` fail, `was_merged` passes and jumps to `landed`; checks pass on main at the merge sha; `done` sets T-13 `done`.
- Neither run starts a harness session.

## Must not

- Run `git merge`, push to the main branch, or deploy (decision 0012).
- Set `done` before the pull request is merged.

## Checks

- cmd: after run 1, `git log main` unchanged and the fake host has one open PR.
- cmd: after run 1, `maestro-ticket get T-13 --json` → `ready_to_release`, no claim.
- cmd: after run 2, `maestro-ticket get T-13 --json` → `done`, actor is the Operations Manager.
- cmd: usage records for both runs → zero tokens.
