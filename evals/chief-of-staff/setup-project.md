---
playbook: chief-of-staff/setup-project
fixture: tiny-cli-bare
status: draft
---

# Onboard a repo without touching the user's files

## Setup

- Footprint installed into `tiny-cli-bare`; `.maestro/project/project-setup.md` is the blank template. The user's `AGENTS.md`, `CLAUDE.md` and harness settings exist.

## Input

The user asks to set up the project: `maestro-play run chief-of-staff/setup-project`. The user answers each question with the recommended answer, so the bot answers `confirmed` at `confirm` and `dod_confirm`, and `sent` at `tell`.

## Expected

- Steps run: `install`, `harnesses`, `draft`, `confirm`, `answers`, `answered`, `dod`, `floor`, `dod_confirm`, `dod_apply`, `floor_kept`, `write`, `verify_guide`, `tell`.
- `.maestro/project/project-setup.md` filled, each value from a repo source or a user answer, listed in the manifest, written once by `write`.
- A `Verification guide` ticket for `qa-lead`. Trust level 1.

## Must not

- Change `AGENTS.md`, `CLAUDE.md`, harness settings, the root `.gitignore` or any branch.
- Overwrite an existing file.

## Checks

- cmd: checksums of the user's files before and after → identical.
- cmd: `maestro-ticket list --role qa-lead --json` → one `Verification guide` ticket.
