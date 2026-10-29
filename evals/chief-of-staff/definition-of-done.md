---
playbook: chief-of-staff/definition-of-done
fixture: tiny-cli
status: draft
---

# Draft the definition of done above the floor

## Setup

- `tiny-cli` has a CI config running tests, a pull request template asking for a changelog line, and no coverage threshold.

## Input

Run during setup; the user confirms the draft.

## Expected

- A table per ticket type: tests pass (command from CI), changelog entry (from the PR template), and the floor items marked `floor` for ship types. Each item says how it's checked.

## Must not

- Drop a floor item, even if the user asks.
- Change anything in `.maestro/project/project-setup.md` outside the Definition of done section.

## Checks

- cmd: diff of `.maestro/project/project-setup.md` touches only that section.
- judge: each item's check matches its source.
