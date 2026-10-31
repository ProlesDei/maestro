# Test a PR

Test the PR at its head sha `H` in the running app and post one `QA:` comment that `/maestro-code-review` reads.

## Phase A: Resolve

1. Find the ticket, branch and PR from the input. Ticket `T-12` is branch `maestro/T-12`. From the PR, read its URL, state, head sha (`H`) and whether it comes from a fork.
2. Refuse, with the reason, if the PR isn't open, comes from a fork (never run a fork's code), or `maestro-ticket get <id> --json` shows a status other than `in_review` or a live claim (name the holder). A PR with no ticket: `<id>` is `pr-<n>`, its body is the spec, and you invent no criteria.
3. If the ticket's latest `QA:` comment is already at `H`, give it and stop, unless the ask says to test again.
4. Name this run `qa-<id>-<6 random letters>`; call it `<run>`. No other session shares it. Fetch, add a detached worktree at `H` in `.maestro/worktrees/<run>`, and check its HEAD is `H`.
5. Find how the app runs: `## Run` in `project-setup.md`, else the README and build files. If nothing says, stop and say so, posting nothing.

## Phase B: Test

1. Start the app from the worktree on fresh state: a new data dir or scratch database and a free port, never a shared or production service. If it won't start, that is a fail: keep the command and the error line.
2. Number each acceptance criterion and copy it word for word. Prove each on the running app with the proof table in Phase B of the **maestro-prove-complete** skill ([SKILL.md](../../maestro-prove-complete/SKILL.md)).
3. Keep the command, exit code and a short excerpt, never a whole log. Save screenshots and long logs in `maestro/evidence/<id>/<run>/` in the repo's shared git folder, never in the worktree, and name them by path.
4. Try to break it as that skill's Phase C says: bad input, limits, error paths, a restart, a double submit, one nearby feature, and 3 runs of a flaky failure with its rate.
5. A criterion you couldn't check is `not checked` with the reason, never a pass. One no one can check as written is `not checkable`, with the question that would fix it.

## Phase C: Judge and post

1. `pass` only when every criterion passes with evidence at `H`; anything else is `fail`. A problem outside the criteria doesn't fail it: list it as a possible new ticket.
2. Stop every process you started.
3. Post this comment on the ticket with `maestro-ticket comment`, keyed by `--idempotency-key <run>` so a retry replays instead of posting twice. With no ticket, comment on the PR.

   ```text
   QA: <pass|fail> at <H> (PR <url>)
   - 1 pass: `<command>` exit 0: <excerpt>
   - 2 fail: steps <exact, from a fresh start>; expected <...>; actual <quoted>; evidence <path or error line>
   - 3 not checked: <why> | not checkable: <question>
   Tried: <each edge case, one line>
   Outside the criteria: <possible new tickets, or none>
   ```

4. Remove the worktree. If it isn't clean, leave it and name the path.

In the reply, put the verdict in the first line, then one line per criterion, the steps for each failure, the comment id and any worktree left behind. `Next:` is `/maestro-code-review <n>`.
