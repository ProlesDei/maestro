---
name: release
description: Merge a QA-passed ticket to the main branch and deploy it, with Chief of Staff approval for production, then mark it done.
role: operations-manager
trigger: new-work
claims: ready_to_release
---

# Release

## Goal

Finished work reaches users safely: what QA passed is exactly what ships, production deploys are approved, and a failed release is rolled back.

## When to use

A `ready_to_release` ticket.

## Steps

### 1. Check it's ready

- **Goal:** Know it's safe to ship and whether approval is in place.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** The reply confirms:
  - The latest QA note is `Handoff: QA passed`, and the branch head matches the sha QA tested.
  - Whether this deploys to production, per `.agent-team/project-setup.md`.
  - For production: an `Approved:` comment from the Chief of Staff after the QA pass.
- **Evidence:** `at-ticket get <id> --json` version; the branch head sha; the comments relied on.

New commits after QA: send it back to `in_progress`. Production and no approval: stop and block.

### 2. Merge (Gate)

- **Goal:** The branch is on the main branch.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** The merge command from `.agent-team/project-setup.md` exits `0` (for example `git checkout <main branch>` then `git merge --no-ff at/<id>-<slug>`, then push if the project has a remote).
- **Evidence:** exit codes; the merge commit sha.

Conflict: `git merge --abort`, then send it back to `in_progress` with the conflicting files.

### 3. Check the main branch

- **Goal:** The main branch still passes after the merge.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** Build and test from `.agent-team/project-setup.md` exit `0` on the main branch.
- **Evidence:** each command, exit code and test counts.

### 4. Deploy (Gate)

- **Goal:** The release is live and healthy.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** The deploy command and then the smoke check from `.agent-team/project-setup.md` exit `0`.
- **Evidence:** each command and exit code; the deployed version or sha.

Skip if the project has no deploy step.

## Stop and block when

- Production deploy without approval: comment `Escalation: approval needed for production deploy of T-<id>` with what ships, the risk and the rollback, and block.
- Step 3 or 4 fails: run the rollback in `.agent-team/project-setup.md`, then block with an `Escalation:` note and the output. No rollback defined: block at once.

## Wrap-up

- Status: `done`.
- Handoff note: merge sha, check results, deploy and smoke check evidence, the approval relied on.
