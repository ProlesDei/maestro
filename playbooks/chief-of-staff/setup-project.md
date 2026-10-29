---
name: setup-project
description: Onboard a repo after the footprint is installed, by drafting project-setup.md from the repo, confirm it with the user, with its definition of done, and start the verification guide.
role: chief-of-staff
trigger: request
claims: none
types: none
---

# Set up a project

## Goal

The project has a confirmed `.maestro/project/project-setup.md` with its definition of done, starts at trust level 1, and has its verification guide under way. Nothing of the user's is edited.

## When to use

Right after install, when `.maestro/project/project-setup.md` is missing or still the blank template. Bot setup in Grok Bot is outside this playbook.

## Steps

### 1. Check the install

- **Goal:** The footprint is in place and the harnesses are ready.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `install status`, then `install detect`
- **Done when:** Both exit `0`: the manifest matches the files on disk, and each installed harness is logged in. The trust level is 1.
- **Evidence:** exit codes; harnesses found; trust level.

### 2. Draft project-setup.md

- **Goal:** A draft filled from the repo itself.
- **Role:** harness
- **Skill:** `maestro-draft-setup`
- **Model:** Mid-tier
- **Done when:** A draft in the scratch dir fills in the name, summary, checks (build, lint, test commands from CI config or the build files), how to run the app, the main branch, the code host, and the board settings. Each value cites its source path; unknowns are marked `ask`.
- **Evidence:** the draft's path; the source path per value; the `ask` list.

Read only. Never edit the user's `AGENTS.md`, `CLAUDE.md`, harness settings, root `.gitignore` or branches.

### 3. Confirm with the user

- **Goal:** Every `ask` answered and the draft confirmed.
- **Role:** bot
- **Skill:** `talk-to-user`
- **Model:** Bot
- **Done when:** The user has seen a short summary of the draft and answered each `ask`, one question at a time, each with a recommended answer.
- **Evidence:** the questions and answers.

### 4. Apply the answers

- **Goal:** The draft has the user's answers.
- **Role:** harness
- **Skill:** `maestro-draft-setup`
- **Model:** Mid-tier
- **Done when:** No `ask` is left in the draft, and every answer is in it.
- **Evidence:** the draft's path; the values changed.

Then run `definition-of-done` in this session on this draft, so step 5 writes the file once.

### 5. Write it

- **Goal:** The confirmed file is in the footprint and the manifest.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `install write .maestro/project/project-setup.md --from <draft>`
- **Done when:** Exit `0`.
- **Evidence:** exit code; the manifest entry.

### 6. Start the follow-ups

- **Goal:** The verification guide is on the board.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket create --type ship --owner chief-of-staff --role qa-lead --title "Verification guide" --body "Type: chore. <pointer to .maestro/project/project-setup.md>" --idempotency-key <session-id>:verify-skill`
- **Done when:** Exit `0`.
- **Evidence:** the new ticket id.

## Stop and block when

- `install status` shows a file changed since install, or a target file already existed: stop and tell the user. Never overwrite.
- No harness is logged in: tell the user which to log in. Don't start sessions.

## Wrap-up

- Skill: `maestro-log-entry` (nothing claimed, no status change). Log the harnesses found, the ticket created and any `Skipped:` steps.
- Handoff note: none; tell the user setup is done, the trust level is 1 (pull requests only), and what comes next.
