---
name: scout-investigation
description: Answer an investigation ticket with an evidenced report, changing no code, and hand the report to the ticket's owner.
role: senior-software-engineer
trigger: new-work
claims: todo
types: investigation
---

# Scout investigation

## Goal

The owner gets a short answer to the ticket's question, with every claim backed by evidence they can check, and no code changed.

## When to use

A `todo` investigation ticket for role `senior-software-engineer`. It ends at a report: no review, QA or release.

## Steps

### 1. Start

- **Goal:** The ticket is in progress under your lease.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <holder> --token <token> --idempotency-key <session-id>:start`
- **Done when:** Exit `0`.
- **Evidence:** exit code; new version.

### 2. Frame the question

- **Goal:** The question, what would answer it, and where to look.
- **Role:** harness
- **Skill:** `maestro-investigate`
- **Model:** Frontier
- **Done when:** The scratchpad has the question in one sentence, what evidence would settle it, and the places to look.
- **Evidence:** the ticket version read; the scratchpad path.

### 3. Investigate

- **Goal:** Evidence for or against each possible answer.
- **Role:** harness
- **Skill:** `maestro-investigate`
- **Model:** Frontier
- **Done when:** Each finding has its evidence: `file:line`, a command and its output path, or a source. Experiments run in a temp dir or a detached worktree, never on a branch that ships.
- **Evidence:** findings with their evidence; commands and exit codes.

### 4. Write the report

- **Goal:** A report the owner can read in two minutes.
- **Role:** harness
- **Skill:** `maestro-write-report`
- **Model:** Mid-tier
- **Done when:** A `Report:` comment is on the ticket: the answer first, then the findings with evidence, what's still unknown, and recommended next tickets.
- **Evidence:** comment exit code.

## Stop and block when

- The question can't be answered without a decision or access outside the team: block with an `Escalation:` note to the owner saying what's missing.
- The step budget runs out: post what you have as a partial `Report:`, then block with an `Escalation:` note to the owner.

## Wrap-up

- Skill: `maestro-handoff-note` (`maestro-escalate` when blocking).
- Status: `in_review` with `--role <owner role>`. The owner closes it after reading the report (decision 0009).
- Handoff note: where the report is, the one-line answer, any `Skipped:` steps.
