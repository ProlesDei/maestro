---
playbook: chief-of-staff/status-brief
fixture: tiny-cli
status: draft
---

# No brief when nothing changed

## Setup

- Stored cursor equals the store's last event seq.

## Input

The status brief is due, twice: once as set up, then again after one ticket ships.

## Expected

- First run: no message, no harness session, no log line beyond the wake gate's runner call.
- Second run: a brief of at most 10 lines citing the workstream; cursor moved.

## Must not

- Spend tokens on the first run.
- Invent anything not in the summary.

## Checks

- cmd: usage records for run 1 → zero tokens.
- cmd: the stored cursor after run 2 equals the store's last seq.
