---
name: maestro-swarm
description: "Run N parallel workers on slices of one job or on rival approaches to one problem, rerun every proof they return, and write one report. Use for /maestro-swarm, 'swarm this', 'fan this out', 'race these approaches', or a batch of Maestro tickets to run at once."
argument-hint: "<task> [--n 3] [--race] [--slices a,b,c]"
disable-model-invocation: true
---

# Swarm

You split the work, launch the workers, check what they return, and write one report. You never do a worker's share, and you never merge, push or delete a worker's branch. Follow the **maestro-working-principles** skill.

## Start

Open a todo list with one item per phase below.

## Phase A: Frame

1. State what done looks like and what the swarm hands back.
2. Pick the shape:

   | Shape | Rule |
   |---|---|
   | Slices: separate parts of one job | No two slices write the same file. Two paths clash if they match or one sits inside the other. Merge clashing slices or run them one after the other. |
   | Race (`--race`): one task, rival approaches | Name each approach and the judge (the test, benchmark or rule that picks the winner) before any worker starts. No judge, no race. |

3. Set N: 3 by default, 8 at most. Say the cost before you start: workers times the expected session size.
4. If the slices are Maestro tickets, brief no workers. Run `maestro-play run work --ticket <id>` for each ticket, report the JSON each run prints, and skip to Phase D.

## Phase B: Launch

1. Write each brief to stand alone. Workers share no chat, scratch file or branch. Each brief names:
   - the goal and its one slice or approach
   - the files it owns and the files it must not touch
   - its own worktree and branch, `maestro/swarm-<tag>-<k>`, cut from one base sha
   - done criteria and the proof to return: command, exit code, paths
   - the reply shape: `done`, `partial` or `blocked`; branch and sha; files; proof; open questions
2. Launch them all in one message: parallel subagents with worktree isolation where the harness has it, else one headless session per worker. If one drops out, go on with N-1 and say so.

## Phase C: Check

1. Check out each worker's sha and rerun its proof yourself. Mark a claim you can't reproduce `unverified`.
2. If a worker returns no sha or no proof, relaunch it once. After a second miss, record a gap. A gap is not a pass.
3. Look across workers for files more than one changed, duplicate work, and claims that contradict each other.
4. For a race, run the judge on every finished entry under the same conditions and record the numbers.

## Phase D: Report

```
Swarm: <task> (<slices | race | tickets>, n=<N>, base <sha>)
| # | slice / approach / ticket | status | branch @ sha | proof | verified |
Findings: <merged, deduplicated, each with its source worker>
Race: <judge>: <scores>; winner <k>, because <reason>
Conflicts: <files, workers>, or none
Merge order: <k, k, ...>, or "none: pick the winner"
Left to do: <gaps, blocked workers and their questions>
Can delete: <branches the user may remove>
```

**Reply:** the report above, filled in, with no raw worker output.
