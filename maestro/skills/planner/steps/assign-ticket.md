# Assign a ticket

On restart, check every unclosed ticket in the goal for a merged PR and follow “QA merges a PR” in [the planner skill](../SKILL.md) to close it. Finish those checks before assigning work. Then route each QA fail or block you haven't handled as in “QA reports a failed or blocked ticket”, and check all engineering claims and PRs. On a T3 finish notice or QA send-back, check the affected ones. Read each engineer's thread using the claim's actor.

On a finish notice or restart, if an engineer ended and no review is live for its open PR's current head, start one as in “Get it reviewed” in `.maestro/skills/engineer/SKILL.md` when that head has no verdict or its latest verdict predates a ruling on it. While the line is stopped, review only the fix.

Reopen a claim only after T3 confirms the engineer session and its child work ended. Reopen the ticket and clear its assignee in one update guarded by its actor and in-progress status. If the guard fails, reread the ticket. Use this when the ticket needs more engineering or the line stops.

One run is one in-progress ticket, including its engineer's reviews. Count the goal's in-progress tickets and wait at the epic's limit. Wait for each new engineer to claim the ticket or end before counting again.

Pick a ready, unassigned child ticket whose paths no running ticket touches. Check the retry cap in “When review or QA sends it back” in `.maestro/skills/engineer/SKILL.md`.

At the cap, check whether the spec is wrong, the ticket is too big, or a call is missing. Settle missing calls with `steps/unblock.md`. Fix the ticket with `steps/update-ticket.md` or split it with `steps/create-ticket.md`. Record what changed and approve the first retry on the ticket before assigning again. Send any further retry for that ticket to the chief of staff for a decision, and record its approval on the ticket as the retry. When you split, file the new tickets with the original's labels, point its dependents at them, then close the original ticket and its PR.

Pick a model suited to the work. Start the engineer in the ticket's worktree with the ticket ID, any prior work and any existing PR. Let T3 notify you when it finishes.
