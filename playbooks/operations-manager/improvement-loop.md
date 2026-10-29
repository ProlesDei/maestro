---
name: improvement-loop
description: Review the logs on a schedule for cost, failures and repeated retros, and suggest workflow improvements to the Chief of Staff as a ticket.
role: operations-manager
trigger: scheduled
claims: none
---

# Improvement loop

## Goal

The workflow gets cheaper and fails less over time, based on evidence from the logs, with the Chief of Staff deciding what changes.

## When to use

The improvement loop is due, per the schedule the bot runtime keeps.

## Steps

### 1. Wake gate

- **Goal:** Spend no tokens when there's nothing new.
- **Skill:** none
- **Tier:** No tokens
- **Done when:** You've counted log lines newer than your stored cursor (the last log date and line read) in `$(git rev-parse --git-common-dir)/maestro/logs/`. None: end here, with no harness session.
- **Evidence:** the files and new line count.

### 2. Find what to improve

- **Goal:** At most three findings, each backed by numbers.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** The harness has read the new log lines (pointers to the files, not pasted) and the tickets they name, and reported:
  - Cost: by role and playbook; the outliers against the usual cost for that playbook.
  - Failures: blocked and failed outcomes, retries, escalations, and where in the playbook they happened.
  - Retros: themes that appear two or more times.
  
  Each finding has the numbers, the ticket ids and the log dates.
- **Evidence:** log files read; ticket ids; the counts behind each finding.

### 3. Suggest

- **Goal:** The Chief of Staff has the suggestions to decide on.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** One ticket is created with `maestro-ticket create --role chief-of-staff --status blocked --title "Workflow suggestion: <short>" --body-file <file> --idempotency-key <session-id>:suggest`, then `maestro-ticket comment <id> --body "Escalation: workflow suggestion, see the body"`. For each finding the body has: the problem, the evidence, the proposed change (which playbook, skill or `environment.md` setting), the expected effect, and how to measure it.
- **Evidence:** the new ticket id; both exit codes.

Skip if there are no findings worth acting on. Store the new cursor either way.

## Stop and block when

- The logs can't be read or a line won't parse: report the file and line; don't guess the numbers.

## Rules

- Never edit playbooks, skills, `.maestro/rules.md` or `.maestro/environment.md` yourself. Suggest; the Chief of Staff decides.
- Never copy log lines that contain secrets. Say where they are instead.

## Wrap-up

- Status: no change (nothing claimed). Log the suggestion ticket id, if any.
- Handoff note: none; the suggestion ticket is the handoff.
