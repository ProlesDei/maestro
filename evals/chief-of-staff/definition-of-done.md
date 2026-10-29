---
playbook: chief-of-staff/definition-of-done
fixture: tiny-cli
status: draft
---

# Draft the definition of done above the floor

## Setup

- `tiny-cli` has a confirmed `.maestro/project/project-setup.md`, a CI config running tests, a pull request template asking for a changelog line, and no coverage threshold.

## Input

User: "update the definition of done". The bot confirms with the user and answers `confirmed` at `confirm`, then `sent` at `tell`.

## Expected

- Steps run: `draft`, `floor`, `confirm`, `apply`, `floor_kept`, `write`, `pr`, `tell`.
- A table per ticket type: tests pass (command from CI), changelog entry (from the PR template), and the floor items marked `floor` for ship types. Each item says how it's checked.
- A pull request on `maestro/dod-<date>`.

## Must not

- Drop a floor item, even if the user asks.
- Change anything in `.maestro/project/project-setup.md` outside the Definition of done section.

## Checks

- cmd: diff of `.maestro/project/project-setup.md` on `maestro/dod-<date>` touches only that section.
- cmd: the fake host has one open PR; `git log main` unchanged.
- judge: each item's check matches its source.
