---
name: handle-escalation
description: Resolve a blocked ticket that needs a decision above the team's authority, such as a production deploy approval, and bring the user only the decisions that need them.
role: chief-of-staff
trigger: request
claims: blocked
---

# Handle an escalation

## Goal

Unblock the team fast. Decide what you have the authority to decide; bring the user only what needs them, as one short question with a recommendation.

## When to use

- A `blocked` ticket whose latest comment starts `Escalation:`: a blocker, a decision above the author's authority, an approval gate such as a production deploy, or a workflow suggestion from the Operations Manager.
- An answer from the user to an earlier escalation arrives in the prompt.

## Steps

### 1. Read the escalation

- **Goal:** Know the decision needed, the options and what each costs.
- **Skill:** none
- **Tier:** Frontier
- **Done when:** The decision is stated in one sentence, with options and a recommended one.
- **Evidence:** `at-ticket get <id> --json` exit code and version; the escalation comment; related ticket ids.

Read the ticket, its comments and the tickets it points to. Don't read code.

### 2. Decide or ask

- **Goal:** A decision, from you or from the user.
- **Skill:** none
- **Tier:** Frontier
- **Done when:** One of:
  - Within your authority and the trust level in `.agent-team/environment.md`: you decide.
  - Production deploy, scope, cost, risk to users or data, or anything you can't undo, beyond what the trust level lets you approve: ask the user. One question, the options, your recommendation.
- **Evidence:** the decision and the reason, or the question sent to the user.

This is your judgment, not a harness prompt.

### 3. Record the decision

- **Goal:** The team can act on the decision without asking again.
- **Skill:** none
- **Tier:** Mid-tier
- **Done when:** `at-ticket comment <id> --body "Approved: ..."` or `"Declined: ..."` is posted, with what to do next. For a declined deploy, say what would change your answer.
- **Evidence:** comment exit code.

For an accepted workflow suggestion, tell the user what should change upstream (playbooks and skills live in the bot profile, not their repo), then close the ticket the no-code way from the playbooks README.

## Stop and block when

- The user hasn't answered yet. Leave the ticket `blocked` and post `Handoff: waiting on the user: <question>`. The answer comes back in a later prompt.

## Wrap-up

- Status: back to where the work continues, as the escalation says: for example `ready_to_release` for an approved deploy, `todo` with `--role` to reroute, `in_progress` for changes, `cancelled` to drop it.
- Handoff note: the decision and the reason, in two lines.
