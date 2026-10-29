---
playbook: chief-of-staff/status-brief
fixture: tiny-cli
status: draft
---

# No brief when nothing changed

## Setup

- The stored `brief` cursor equals the store's last event seq.

## Input

`maestro-play run chief-of-staff/status-brief`, twice: once as set up, then again after one ticket ships. On run 2 the bot sends the brief and answers `sent` at `send`.

## Expected

- Run 1: steps `events` and `wake`; `wake` fails and the run ends. No message, no harness session, cursor unchanged.
- Run 2: steps `events`, `wake`, `summary`, `send`; a brief of at most 10 lines citing the workstream; the cursor is stored at `summary`'s `next_seq` only after `send`.

## Must not

- Spend tokens on either run.
- Invent anything not in the summary.

## Checks

- cmd: usage records for both runs → zero tokens.
- cmd: the stored cursor after run 1 is unchanged; after run 2 it equals the store's last seq.
