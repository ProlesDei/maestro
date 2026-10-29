---
name: maestro-analyse-logs
description: Read Maestro's logs and ticket events to measure adopted changes against their baselines and find one repeated pattern worth changing; use it in the improvement loop.
disable-model-invocation: true
metadata:
  internal: true
---

# Analyse the logs

You turn session logs and ticket events into numbers: whether an adopted change helped, and the one pattern most worth fixing next.

## Inputs

- The cursor and the log files the runner listed.
- The pattern threshold, from the prompt.
- Adopted changes due for measuring, each with its metric, baseline and window.

## Steps

1. **Read** the log lines since the cursor in `$(git rev-parse --git-common-dir)/maestro/logs/`, and the events of the tickets they name (`maestro-ticket events --ticket <id> --json`). A line that won't parse: report its `file:line`; don't guess.
2. **Measure** each due change: its metric over its window, computed the same way as the baseline. Verdict `keep` if it improved beyond the spread, else `revert`, with the numbers.
3. **Find one pattern** across cost per ticket, Tries, escalations and their reasons, time in each status, retro lines, user corrections, and review and QA failure reasons. It must repeat at least the threshold, on different tickets. Pick the one that costs the most or fails the most. Propose one change, its expected effect, and the metric and window to measure it.

## Reply

```yaml
status: done
summary: 1 change kept; pattern: fixture setup retries on 4 tickets
evidence:
  - logs/2026-10-28.jsonl lines 1-212
measured: [{change: "junior step budget 20→25", baseline: 0.31, now: 0.22, verdict: keep}]
pattern: {what: "test fixture setup took three tries", tickets: [T-3, T-7, T-9, T-11], change: "...", measure: "..."}
next: classify
questions: []
```

## Rules

- Read only.
- Never copy a log line that holds a secret; give its `file:line`.
