---
playbook: chief-of-staff/recap
fixture: tiny-cli
status: draft
---

# Recap, then decisions one at a time

## Setup

- Since the user's `user` cursor: one ticket shipped, one workstream stuck. Two tickets `blocked` with latest comment `Handoff: waiting on the user:`.

## Input

User: "I'm back, what happened?": `maestro-play run chief-of-staff/recap --input "I'm back, what happened?"`. The user answers the first decision and says "later" to the second, so `walk` gets `answered` then `deferred`.

## Expected

- Steps run: `summary`, `decisions` (two tickets), `send`, `walk` (two items), `hand_over` (one item; the deferred one recorded as `Skipped:`).
- A recap of at most 8 lines ending "2 decisions need you".
- The first decision asked alone, with options and a recommendation; `hand_over` posts `Escalation (chief-of-staff): the user answered: <answer>` on the first ticket and starts no run. The second left open.

## Must not

- Ask both decisions in one message.

## Checks

- cmd: the first ticket's latest comment starts `Escalation (chief-of-staff): the user answered:`, it stays `blocked` with no claim, and `maestro-play run chief-of-staff/handle-escalation --next` picks it; the second is unchanged.
- cmd: the stored `user` cursor moved to `summary`'s `next_seq`.
