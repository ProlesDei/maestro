---
name: definition-of-done
description: Draft the project's definition of done from the repo's CI, pull request template, contributing guide, branch protection and coverage settings, and have the user confirm it.
role: chief-of-staff
trigger: request
claims: none
types: none
---

# Definition of done

## Goal

The project's definition of done is confirmed by the user, varies by ticket type, says how each item is checked, and is never below Maestro's floor.

## When to use

During `setup-project`, or when the user asks to change the definition of done. Bots may propose raising the bar; only the user can lower it.

## Steps

### 1. Draft it from the repo

- **Goal:** A draft that matches what the repo already enforces.
- **Role:** harness
- **Skill:** `maestro-draft-setup`
- **Model:** Mid-tier
- **Done when:** A draft of the Definition of done section of `.maestro/project/project-setup.md` is in the scratch dir. Each item has its ticket types (feature, bug, chore, docs, investigation), how it's checked (a command, evidence or judgment) and its source: CI config, pull request template, CONTRIBUTING, branch protection (from the code host), coverage thresholds.
- **Evidence:** the draft's path; the source per item.

### 2. Check the floor

- **Goal:** The draft is at or above Maestro's floor.
- **Role:** bot
- **Skill:** `none`
- **Model:** Bot
- **Done when:** Every ship type has verification evidence from the real app, a passing review and a passing merge queue, and investigations have an answer backed by evidence. Missing ones are added and marked `floor`.
- **Evidence:** the items added for the floor.

### 3. Confirm with the user

- **Goal:** The user confirms the definition of done.
- **Role:** bot
- **Skill:** `talk-to-user`
- **Model:** Bot
- **Done when:** The user saw the table and confirmed it, or changed items. A change that lowers the bar is accepted only from the user, and never below the floor.
- **Evidence:** the user's answer; each change.

### 4. Apply the answers

- **Goal:** The confirmed section is in a scratch copy of `.maestro/project/project-setup.md` (during setup, the setup draft).
- **Role:** harness
- **Skill:** `maestro-draft-setup`
- **Model:** Mid-tier
- **Done when:** The scratch copy has the confirmed table and nothing else in the file changed.
- **Evidence:** the scratch copy's path; the diff against the current file.

### 5. Write it

- **Goal:** The confirmed section is in the project.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `install write .maestro/project/project-setup.md --from <scratch copy> --branch maestro/dod-<date>`, then `open pr --branch maestro/dod-<date>`
- **Skip when:** during setup: `setup-project` writes the draft with this section in it.
- **Done when:** Exit `0`.
- **Evidence:** exit codes; the pull request URL.

## Stop and block when

- The user doesn't confirm: keep the draft in the scratch dir, and leave the floor in force. Ask again at the next recap.

## Wrap-up

- Skill: `maestro-log-entry` (nothing claimed, no status change). Log the item count per ticket type and any `Skipped:` steps.
- Handoff note: none.
