---
name: maestro-swarm
description: Use when you want N parallel workers, either across different slices of a job or racing different approaches to one problem, and then one aggregated report with every claim verified.
argument-hint: "<task> [--n 3] [--race] [--slices a,b,c]"
disable-model-invocation: true
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Swarm

You split the work, launch the workers, verify what they bring back, and write one report. You don't do a worker's slice yourself.

## 1. Choose the shape

- **Slices:** different parts of one job, which must be independent. No two slices may write the same file. Use the same rule as ticket areas: two paths overlap if they're equal or one is inside the other. If two slices would overlap, merge them or run them one after the other.
- **Race** (`--race`): the same task with different approaches. Name each approach and the **judge** before any worker starts: the test, benchmark or criterion that picks the winner. If the judge isn't decided up front, it's not a race.
- **N** defaults to 3 and goes no higher than 8. Count the cost up front (workers × the expected session size) and say it.
- If the slices are tickets, use `/maestro-work <id>` for each one instead. The engine's area locks keep them apart.

## 2. Brief each worker

Each brief stands on its own. Workers share no chat, no scratch file and no branch. A brief holds:
- the goal and the one slice or approach
- the files it owns, and the files it must not touch
- its own worktree and branch, `maestro/swarm-<tag>-<k>`, made from the same base sha
- done criteria, and the proof it must return: command, exit code, paths
- its reply format: status (done, partial, blocked), branch and sha, files, evidence, open questions
- `/maestro-working-principles`

Launch them all at once, using the harness's parallel subagents (with worktree isolation where it has it) or one headless session per worker.

## 3. Verify, then aggregate

- For each worker, check out its sha and rerun its proof yourself. A claim you didn't reproduce is marked `unverified`.
- Look across workers for conflicts (files changed by more than one), duplicated work, and contradictions.
- For a race, run the judge on every finished entry, under the same conditions, and give the numbers.

## 4. Report

```
Swarm: <task> (<slices | race>, n=<N>, base <sha>)
| # | slice / approach | status | branch @ sha | proof | verified |
Findings: <merged, deduplicated, each with its source worker>
Race: <judge> → <scores> → winner <k>, because <reason>
Conflicts: <files, workers>, or none
Merge order: <k, k, …>, or "none: pick the winner"
Left to do: <gaps, blocked workers and their questions>
```

Never merge, push or delete a worker's branch. Say which branches can go, and the user removes them.
