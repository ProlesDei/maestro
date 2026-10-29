---
name: improvement-loop
description: On a schedule, turn repeated patterns in the logs into one tested change at a time, adopt it within your approval level or propose it, and keep or revert it against a baseline.
role: operations-manager
trigger: scheduled
claims: none
types: none
---

# Improvement loop

## Goal

The workflow gets cheaper and fails less over time, one evidenced change at a time, each tested on evals and measured against a baseline.

## When to use

The improvement loop is due, per the schedule the bot runtime keeps.

## Steps

### 1. Wake gate

- **Goal:** Spend no tokens when there's nothing new.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `logs --since <cursor>`
- **Done when:** Exit `0`. No new lines and no adopted change due for measuring: end here, with no harness session.
- **Evidence:** the files and new line count.

### 2. Measure adopted changes

- **Goal:** Each change adopted earlier is kept or reverted on evidence.
- **Role:** harness
- **Skill:** `maestro-analyse-logs`
- **Model:** Mid-tier
- **Skip when:** no adopted change has reached its measuring window.
- **Done when:** Each due change has its metric now against its baseline, and a keep or revert verdict with the reason, logged with `maestro-log-entry`. A revert of tuning is adopted as in step 7; a revert of anything else is proposed as in step 7.
- **Evidence:** baseline and current numbers; the verdict and reason per change.

### 3. Find one pattern

- **Goal:** At most one change worth making, backed by a pattern, never a one-off.
- **Role:** harness
- **Skill:** `maestro-analyse-logs`
- **Model:** Mid-tier
- **Done when:** The reply names the pattern (seen at least as often as the threshold from preflight) across the signals: cost, tries, escalations, time in each status, retro lines, user corrections, review and QA failure reasons. It proposes one change, its expected effect and how to measure it. No pattern: say so; steps 4 and 5 are skipped, and steps 6 to 8 run only for a revert from step 2.
- **Evidence:** log files read; ticket ids; the counts behind the pattern.

### 4. Classify the change

- **Goal:** Know who approves it and whether you may adopt it.
- **Role:** bot
- **Skill:** `none`
- **Model:** Bot
- **Skip when:** step 3 found no pattern.
- **Done when:** The change has its class and approver from "Changing Maestro" in the team rules: tuning (you adopt), skill or prompt, playbook, or persona or authority. A change to your own guardrails is dropped. A change to Maestro itself (skills, playbooks, personas) is propose-only.
- **Evidence:** the class and the reason.

### 5. Test on evals

- **Goal:** The change doesn't break golden tasks for the roles it touches.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `evals <tasks for the affected roles> --with <change>` and the same tasks without it, as the baseline
- **Skip when:** step 3 found no pattern, or the change is tuning that no golden task covers.
- **Done when:** Both runs exit `0`, and no task that passed without the change fails with it.
- **Evidence:** pass and fail per task, with and without the change.

### 6. Set up the tuning worktree

- **Goal:** A branch for the tuning change.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `worktree add tuning-<date> --branch maestro/tuning-<date> --from <main branch>`
- **Skip when:** no tuning to adopt or revert from steps 2 and 4.
- **Done when:** Exit `0`.
- **Evidence:** exit code; branch and path.

### 7. Adopt or propose

- **Goal:** Tuning is adopted with its baseline recorded; everything else is proposed for approval.
- **Role:** harness
- **Skill:** `maestro-propose-change`
- **Model:** Mid-tier
- **Skip when:** nothing to adopt, revert or propose, or step 5 failed.
- **Done when:** One of:
  - Tuning: `.maestro/project/budgets.json` is changed and committed in the tuning worktree, and the change, baseline and measuring window are logged.
  - Anything else: an investigation ticket owned by `chief-of-staff` holds a `Report:` with the pattern, evidence, eval results, the proposed change, its expected effect and how to measure it. It is moved to `in_review`, then blocked with an `Escalation (chief-of-staff): workflow suggestion` note.
- **Evidence:** commit sha or ticket id; exit codes.

### 8. Open the pull request

- **Goal:** A human can review and merge the tuning change.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `open pr --branch maestro/tuning-<date>`
- **Skip when:** step 7 committed no tuning.
- **Done when:** Exit `0` with the pull request URL.
- **Evidence:** exit code; the URL.

Store the new cursor after the last step that ran.

## Stop and block when

- The logs can't be read or a line won't parse: report the file and line; don't guess the numbers.

## Rules

- Never edit playbooks, skills, personas or `.maestro/rules.md`. Propose them; changes to Maestro itself are never pushed upstream from a user's install unless the maintainer opted in.
- Never edit your own guardrails: budgets, trust levels or permissions for your role.
- Never copy log lines that contain secrets. Say where they are instead.

## Wrap-up

- Skill: `maestro-log-entry` (nothing claimed, no status change). Log the cursor, the pattern, any `Skipped:` steps and the ticket id, if any.
- Handoff note: none; the proposal ticket is the handoff.
