---
name: maestro-swarm
description: "Run N parallel workers on slices of one job or on rival approaches to one problem, rerun every proof they return, and write one report. Use for /maestro-swarm, 'swarm this', 'fan this out', 'race these approaches', or a batch of Maestro tickets to run at once."
argument-hint: "<task> [--n 3] [--race] [--slices a,b,c]"
disable-model-invocation: true
---

# Swarm

You split the work, launch the workers, check what they return, and write one report. You never do a worker's share, and you never merge, push or delete a worker's branch. Follow the **maestro-working-principles** skill.

## Phase A: Frame

1. State what done looks like and what the swarm hands back.
2. Pick the shape:

   | Shape | Rule |
   |---|---|
   | Slices: separate parts of one job | No two slices write the same file. Two paths clash if they match or one sits inside the other. Merge clashing slices or run them one after the other. |
   | Race (`--race`): one task, rival approaches | Before any worker starts, name each approach and the benchmark, output or rule that picks the winner. |

3. Set N: 3 by default, 8 at most. Say the cost before you start: workers times the expected session size.
4. If the slices are Maestro tickets, give each worker one ticket id whose role is `engineer` or unset; a `Design:` scout is the designer's. It claims the ticket on the ticket board, as the **maestro-working-principles** skill says.

## Phase B: Launch

1. Write each brief to stand alone. Workers share no chat, scratch file or branch. Each brief names:
   - the goal and its one slice or approach
   - the files it owns and the files it must not touch
   - its own worktree and branch: for a ticket, a worktree of its own on branch `maestro/<ticket-id>`, so QA and review find its PR; else `maestro/swarm-<tag>-<k>`, cut from one base sha, with `<tag>` a short slug and 4 random letters, so no other swarm or earlier run has the branch
   - done criteria and the proof to return: command, exit code, paths
   - with no ticket, a `Not handled:` list of the cases it should leave, or `Not handled: none`; pass the same list in the prompt that starts its review and QA, since they never take one from the PR
   - the reply shape: `done`, `partial` or `blocked`; branch and sha; files; proof; open questions
2. Launch them all in one message: use parallel subagents, each at the effort **Model and effort** in **maestro-working-principles** sets. Use the harness's worktree isolation only for slices that aren't tickets, since it names its own branch. If one drops out, go on with N-1 and say so.
3. Find each worker's worktree path from the launch or Git's worktree list.

## Phase C: Check

1. When a worker ends, even on timeout or an early stop, use [server.sh](../maestro-working-principles/scripts/server.sh) from its worktree to stop all servers there. A shared harness can outlive the worker. Do this while the worktree still belongs to that worker alone. For a ticket, skip the stop if the worker no longer holds its claim. Then release any claim it still holds as **Claims** says. If a worker returns no sha or no proof, relaunch it once unless its ticket is `blocked`. After a second miss, record a gap. A gap is not a pass.
2. Rerun each worker's proof in your own detached worktree in a fresh temp folder outside the repo. Record the path and check its head matches the worker's SHA. Follow the **maestro-working-principles** shell rule. Remove the worktree and folder when done. Mark a claim you can't reproduce `unverified`.
3. Look across workers for files more than one changed, duplicate work, and claims that contradict each other.
4. For a race, score every finished entry by the chosen measure under the same conditions and record the numbers.

## Phase D: Report

```
Swarm: <task> (<slices | race | tickets>, n=<N>, base <sha>)
| # | slice / approach / ticket | status | branch @ sha | proof | verified |
Findings: <merged, deduplicated, each with its source worker>
Race: <measure>: <scores>; winner <k>, because <reason>
Conflicts: <files, workers>, or none
Merge order: <k, k, ...>, or "none: pick the winner"
Left to do: <gaps, blocked workers and their questions>
Can delete: <branches the user may remove, never a ticket's>
```

**Reply:** the report above, filled in, with no raw worker output.
