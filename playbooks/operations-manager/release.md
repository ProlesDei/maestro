---
name: release
description: Ship a QA-passed ticket within the trust level, as a pull request at level 1 or through the merge queue above it, and set it done once it has landed.
role: operations-manager
trigger: new-work
claims: ready_to_release
types: feature, bug, chore, docs
---

# Release

## Goal

What QA passed is exactly what ships, never beyond the trust level. At trust level 1 that means a pull request and nothing more: no merge, no push to the main branch (decision 0012).

## When to use

A `ready_to_release` ship ticket: a new one, or one with an open pull request from an earlier run.

## Steps

### 1. Check the pull request

- **Goal:** Know if an earlier run already opened a pull request, and what happened to it.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `pr status <id>`
- **Skip when:** the ticket has no `Handoff: PR opened` note.
- **Done when:** Exit `0` with a state.
  - `open`: nothing to do. End here with no harness session and no status change.
  - `merged`: skip to step 7 with the merge sha.
  - `closed` without merging: block with an `Escalation:` note to the owner.
- **Evidence:** the state and, if merged, the merge sha.

### 2. Check it's ready

- **Goal:** Every gate for this ship has its evidence.
- **Role:** harness
- **Skill:** `maestro-check-release`
- **Model:** Mid-tier
- **Done when:** The reply confirms, with the comment or file for each:
  - The latest QA note is `Handoff: QA passed`, and the branch head is the sha QA tested.
  - Each definition-of-done item for the ticket type in `.maestro/project/project-setup.md` is met.
  - The review passed on the opposite model family.
  - A rollback plan exists for anything that deploys.
- **Evidence:** ticket version; branch head sha; the comments and paths relied on.

New commits after QA: wrap up to `in_progress` for the author. A gate is missing: block with an `Escalation:` note to the owner.

### 3. Read the trust level

- **Goal:** Know how far this ship may go.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `trust <id>`
- **Done when:** Exit `0` with the level for the ticket's risk class.
- **Evidence:** the level and risk class.

### 4. Open the pull request

- **Goal:** The ticket branch is up for a human to merge.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `open pr <id>`
- **Skip when:** the trust level is 2 or more for this risk class.
- **Done when:** Exit `0` with the pull request URL. Then wrap up: status stays `ready_to_release`, note `Handoff: PR opened <url>`. Steps 5 to 7 wait for the next run.
- **Evidence:** exit code; the URL; the head sha pushed.

### 5. Merge (Gate)

- **Goal:** The branch lands on the latest main branch through the merge queue.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `merge <id>`
- **Skip when:** trust level 1 (step 4 opened a pull request instead).
- **Done when:** Exit `0` with the merge sha. The queue re-ran the checks on the latest main.
- **Evidence:** exit code; merge sha; the queue's check results.

Conflict or failing checks in the queue: wrap up to `in_progress` for the author with the output. That isn't a try.

### 6. Deploy (Gate)

- **Goal:** The release is live and healthy.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `deploy <env>`, then `smoke <env>`
- **Skip when:** the project has no deploy step, or the trust level is below 4.
- **Done when:** Both exit `0` for each environment in "Release" in `.maestro/project/project-setup.md`. Migrations, auth and payments also need an `Approved:` citing the user, unless the user has allowed them.
- **Evidence:** each command and exit code; the deployed version or sha.

### 7. Confirm it landed

- **Goal:** The main branch has the change and still passes.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `check all --at <merge sha>`
- **Done when:** Every check exits `0` on the main branch at or after the merge sha.
- **Evidence:** each check, exit code and test counts; the sha checked.

## Stop and block when

- A gate is missing, or a pull request was closed without merging: block with an `Escalation:` note to the owner.
- A Gate step is above the trust level and the project needs it now: block with an `Escalation (chief-of-staff):` note with what ships, the risk and the rollback.
- Step 6 or 7 fails: ask the runner for `rollback <env>` if step 6 deployed, then block with an `Escalation (chief-of-staff):` note and the output. No rollback defined: block at once.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: `done` only after step 7 passed (you are the only role that sets it). After step 4: `keep` (it stays `ready_to_release`).
- Handoff note: the pull request URL or merge sha, check results, deploy and smoke evidence, the approvals relied on, any `Skipped:` steps with their reason.
