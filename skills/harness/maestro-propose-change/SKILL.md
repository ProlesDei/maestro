---
name: maestro-propose-change
description: Adopt a tuning change on its branch with its baseline, or file any other change as a proposal for the Chief of Staff; use it at the end of the improvement loop.
disable-model-invocation: true
metadata:
  internal: true
---

# Propose a change

You put an improvement where the right person can approve it: tuning as a commit for review, anything else as a proposal ticket.

## Inputs

- Holder and session id.
- The change, its class, its metric, baseline and window, and the eval results.
- For tuning: the `maestro/tuning-<date>` worktree.

## Steps

- **Tuning** (budgets, tiers, limits):
  1. Change the value in `.maestro/project/budgets.json` in the tuning worktree.
  2. Commit, with the old and new value, baseline, metric and window in the message.
  3. Append one line to `$(git rev-parse --git-common-dir)/maestro/logs/<YYYY-MM-DD>.jsonl`: `{"kind":"adopted","change":...,"metric":...,"baseline":...,"window":...,"commit":...}`, so a later loop can measure it.
- **Anything else:**
  Number each proposal in this session (`<n>` = 1, 2, …) so every write has its own idempotency key.
  1. `maestro-ticket create --title "Proposal: <change>" --type scout --owner chief-of-staff --role chief-of-staff --body-file - --idempotency-key <session-id>:proposal-<n> --json`, with `Type: investigation` in the body.
  2. `maestro-ticket claim <id> --holder <holder> --keepalive $PPID --idempotency-key <session-id>:proposal-<n>-claim --json`, and note `claim.token`. Then `maestro-ticket transition <id> --to in_progress --from todo --expect-version <N> --holder <holder> --token <token> --idempotency-key <session-id>:proposal-<n>-start --json`, and `maestro-ticket transition <id> --to in_review --from in_progress --expect-version <N+1> --holder <holder> --token <token> --idempotency-key <session-id>:proposal-<n>-review --json`.
  3. `maestro-ticket comment <id> --body-file - --holder <holder> --token <token> --idempotency-key <session-id>:proposal-<n>-report --json`, a `Report:` with the pattern and its counts, the ticket ids, the eval results with and without the change, the proposed change, its expected effect, how to measure it, and its class and approver.
  4. `maestro-ticket comment <id> --body "Escalation (chief-of-staff): workflow suggestion: <change>, see the Report: above" --holder <holder> --token <token> --idempotency-key <session-id>:proposal-<n>-escalate --json`, so the latest comment routes it, then `maestro-ticket transition <id> --to blocked --from in_review --expect-version <N+2> --holder <holder> --token <token> --actor-role <your role> --note "Escalation (chief-of-staff): workflow suggestion" --release --idempotency-key <session-id>:proposal-<n>-block --json`.

## Reply

```yaml
status: done
summary: proposal T-50 for the Chief of Staff
evidence:
  - maestro-ticket create → exit 0, T-50
  - maestro-ticket transition T-50 --to blocked → exit 0
next: log and end
questions: []
```

## Rules

- Never change your own role's budgets, trust levels or permissions.
- Never edit playbooks, skills, personas or `.maestro/rules.md`. Those are proposals only.
