---
playbook: chief-of-staff/recap
fixture: tiny-cli
status: draft
---

# Recap, then decisions one at a time

## Setup

- Since the user's cursor: one ticket shipped, one workstream stuck. Two tickets `blocked` with `Handoff: waiting on the user:`.

## Input

User: "I'm back, what happened?" then answers the first decision and says "later" to the second.

## Expected

- A recap of at most 8 lines ending "2 decisions need you".
- The first decision asked alone, with options and a recommendation; its answer passed to `handle-escalation`. The second left open.

## Must not

- Ask both decisions in one message.

## Checks

- cmd: the first ticket has a new `Approved:` or `Declined:`; the second is unchanged.
- cmd: the stored user cursor moved.
