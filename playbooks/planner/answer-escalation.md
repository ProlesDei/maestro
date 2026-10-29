---
name: answer-escalation
description: As a ticket's owner, answer an escalation from the team and unblock the ticket, or forward it to the Chief of Staff when it's above your authority.
role: planner
trigger: request
claims: blocked
types: any
---

# Answer an escalation

## Goal

Blocked work moves again as soon as the owner can decide, and only questions above the owner's authority reach the Chief of Staff.

## When to use

A `blocked` ticket you own whose latest comment starts `Escalation:` (not `Escalation (chief-of-staff):`): an unclear ticket, a bug that won't reproduce, a missing service, a senior out of tries, a conflict between asks.

## Steps

### 1. Read the escalation

- **Goal:** Know the question, the options and what each costs.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket get <id> --json`
- **Done when:** Exit `0`; you have the escalation comment, the ticket's acceptance criteria and its history.
- **Evidence:** version; the comment.

### 2. Gather the facts

- **Goal:** The facts the answer depends on.
- **Role:** harness
- **Skill:** `maestro-investigate`
- **Model:** Mid-tier
- **Skip when:** the escalation already has the facts needed to answer.
- **Done when:** Each fact the question turns on is checked in the spec, related tickets or the code (read only), with its source.
- **Evidence:** paths and ticket ids read.

### 3. Decide or forward

- **Goal:** An answer, or a forward to the Chief of Staff.
- **Role:** bot
- **Skill:** `none`
- **Model:** Bot
- **Done when:** One of:
  - Within your authority (clarify criteria, split or re-route the ticket, change order, accept a known limit): decide.
  - Scope, cost, risk to users or data, a product decision, or a Gate: forward.
- **Evidence:** the decision and the reason, or what is above your authority.

### 4. Apply the answer

- **Goal:** The ticket can move without anyone asking again.
- **Role:** harness
- **Skill:** `maestro-write-tickets`
- **Model:** Mid-tier
- **Skip when:** step 3 forwarded.
- **Done when:** The ticket body or criteria are corrected, and any new or split tickets are created with dependencies.
- **Evidence:** ticket ids and versions changed.

## Stop and block when

- Step 3 forwarded: the ticket stays `blocked` (`keep`: no move, the prefix routes it). The note starts `Escalation (chief-of-staff):` (`maestro-escalate`) with the question, options, your recommendation and the original escalation.

## Wrap-up

- Skill: `maestro-handoff-note`, or `maestro-escalate` when forwarding.
- Status: back to the status it was blocked from, with `--role` set to the escalation's `Return-role:` (or the role that should act now if you re-route it), `cancelled` to drop it, or `keep` when forwarded.
- Handoff note: `Approved:` or `Declined:` with the decision and what to do next, or the forward. Any `Skipped:` steps.
