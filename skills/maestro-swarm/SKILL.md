---
name: maestro-swarm
description: "Use for /maestro-swarm, 'swarm this', 'fan this out', or 'race these approaches'. Runs N parallel workers on separate slices of one job, or on rival approaches to one problem, reruns every proof they bring back, and returns one report."
argument-hint: "<task> [--n 3] [--race] [--slices a,b,c]"
disable-model-invocation: true
---

# Swarm

You split the work, launch the workers, check what they return, and write one report. You never do a worker's share yourself. You never merge, push or delete a worker's branch.

## 1. Frame

1. State what done looks like and what the swarm must hand back.
2. Pick the shape. **Slices** are separate parts of one job. No two slices may write the same file: two paths clash if they match or one sits inside the other. Merge clashing slices or run them one after the other. **Race** (`--race`) gives each worker the same task with a different approach. Name each approach and the judge (the test, benchmark or rule that picks the winner) before any worker starts. No judge, no race.
3. Set N: 3 by default, 8 at most. Say the cost before you start: workers times the expected session size.
4. If the slices are Maestro tickets, run `/maestro-work <id>` on each instead.

## 2. Brief and launch

Each brief stands alone. Workers share no chat, scratch file or branch. Give each one:

- the goal and its one slice or approach
- the files it owns and the files it must not touch
- its own worktree and branch, `maestro/swarm-<tag>-<k>`, cut from one base sha
- done criteria and the proof to return: command, exit code, paths
- the reply shape: `done`, `partial` or `blocked`; branch and sha; files; proof; open questions

Launch them all in one message: parallel subagents with worktree isolation where the harness has it, else one headless session per worker. If a worker drops out, go on with N-1 and say so.

## 3. Check

1. Check out each worker's sha and rerun its proof yourself. Mark any claim you could not reproduce `unverified`.
2. Look across workers for files more than one changed, duplicate work, and claims that contradict each other.
3. For a race, run the judge on every finished entry under the same conditions and record the numbers.

## 4. Report

```
Swarm: <task> (<slices | race>, n=<N>, base <sha>)
| # | slice / approach | status | branch @ sha | proof | verified |
Findings: <merged, deduplicated, each with its source worker>
Race: <judge> → <scores> → winner <k>, because <reason>
Conflicts: <files, workers>, or none
Merge order: <k, k, …>, or "none: pick the winner"
Left to do: <gaps, blocked workers and their questions>
Can delete: <branches the user may remove>
```

Reply: the report above, filled in, with no raw worker output pasted.
