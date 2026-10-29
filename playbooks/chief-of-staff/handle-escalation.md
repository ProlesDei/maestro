---
name: handle-escalation
description: Resolve a ticket forwarded to the Chief of Staff, such as a Gate approval, a question above the owner's authority or a workflow suggestion, and bring the user only what needs them.
role: chief-of-staff
trigger: request
claims: blocked
types: any
---

# Handle an escalation

## Goal

Unblock the team fast. Decide what you have the authority to decide; bring the user only what needs them, as one short question with a recommendation.

## When to use

- A `blocked` ticket whose latest comment starts `Escalation (chief-of-staff):`: a question the owner forwarded, a Gate above the trust level, a security problem, or a workflow suggestion from the Operations Manager.
- An answer from the user to an earlier escalation arrives in the prompt.

## Steps

### 1. Read the escalation

- **Goal:** Know the decision needed, the options and what each costs.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket get <id> --json`
- **Done when:** Exit `0`; you have the escalation comment and any `Report:`. Read the ticket and the tickets it points to, never code.
- **Evidence:** version; the escalation comment; related ticket ids.

### 2. Decide or ask

- **Goal:** A decision, from you or from the user.
- **Role:** bot
- **Skill:** `talk-to-user`
- **Model:** Bot
- **Done when:** One of:
  - Within your authority and the trust level: you decide.
  - A Gate above the trust level, scope, cost, risk to users or data, anything you can't undo, or a persona or authority change: ask the user. One question, the options, your recommendation.
  - A workflow suggestion: decide per its class in "Changing Maestro" in the team rules. A playbook change needs you; a persona or authority change needs the user.
- **Evidence:** the decision and the reason, or the question sent.

### 3. Record the decision

- **Goal:** The team can act on it without asking again.
- **Role:** runner
- **Skill:** `runner`
- **Model:** No tokens
- **Run:** `ticket comment <id> --body "<Approved: or Declined: ...>" --holder <holder> --token <token> --idempotency-key <session-id>:decision`
- **Skip when:** waiting on the user.
- **Done when:** Exit `0`. An `Approved:` for a Gate cites the user's answer. A `Declined:` says what would change it.
- **Evidence:** comment exit code.

For an accepted change to Maestro itself, tell the user what should change upstream. It's never pushed upstream from their install.

## Stop and block when

- The user hasn't answered yet: leave the ticket `blocked` (`keep`) with `Handoff: waiting on the user: <question>`. The answer comes back in a later prompt.

## Wrap-up

- Skill: `maestro-handoff-note`.
- Status: back to the status it was blocked from (for example `ready_to_release` for an approved Gate), with `--role` set to the escalation's `Return-role:`, or the role that should act now if you re-route it, or `cancelled` to drop it. A workflow suggestion is a scout ticket you own: back to `in_review`, then `done` with `--actor chief-of-staff` (decision 0009). Never set `done` on a ship ticket.
- Handoff note: the decision and the reason, in two lines, and any `Skipped:` steps.
