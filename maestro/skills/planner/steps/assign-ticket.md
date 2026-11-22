# Assign a ticket

On restart, work through the goal's unclosed tickets in this order before you assign anything:

1. Close each ticket whose PR merged, as in “QA merges a PR” in [the planner skill](../SKILL.md).
2. Finish any stop recorded on the epic: halt and reopen the runs it hasn't halted yet.
3. Route each QA fail or block with no later clearance for its PR's current head, as in “QA reports a failed or blocked ticket”, unless the board shows you already routed it.
4. Send each recorded ruling that no fresh review has followed to its live engineer.
5. Check the remaining engineering claims and PRs as below.

On a T3 finish notice or QA send-back, check only the affected tickets. Read each engineer's thread using the claim's actor.

On a finish notice or restart, if an engineer ended and no review is live for its open PR's current head, start one as in “Get it reviewed” in `.maestro/skills/engineer/SKILL.md` when that head has no verdict or its latest verdict predates a ruling on it. While the line is stopped, review only the stop's fixes.

Reopen a claim only after T3 confirms the engineer session and its child work ended. Reopen the ticket and clear its assignee in one update guarded by its actor and in-progress status. If the guard fails, reread the ticket. Use this when the ticket needs more engineering or the line stops.

One run is one in-progress ticket, including its engineer's reviews. Count the goal's in-progress tickets plus live engineer sessions that haven't claimed yet, each ticket once, and wait at the epic's limit.

Pick a ready, unassigned child ticket whose paths no running ticket touches. Check the retry cap in “When review or QA sends it back” in `.maestro/skills/engineer/SKILL.md`.

At the cap, check whether the spec is wrong, the ticket is too big, or a call is missing. Settle missing calls with `steps/unblock.md`. Fix the ticket with `steps/update-ticket.md` or split it with `steps/create-ticket.md`. Record what changed and approve the first retry on the ticket before assigning again. Send any further retry for that ticket to the chief of staff for a decision, and record its approval on the ticket as the retry. When you split, file the new tickets with the original's labels, point its dependents at them, then close the original ticket and its PR.

Pick a model suited to the work. Start the engineer in the ticket's worktree with the ticket ID, any prior work and any existing PR. Let T3 notify you when it finishes.
