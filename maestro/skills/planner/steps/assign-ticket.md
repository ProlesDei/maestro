# Assign a ticket

On restart, defer all engineer, review and QA starts and handoffs that trigger them. Close verified merges, finish recorded halts and claim releases, check whether to lift the stop, and route QA verdicts under the [planner skill](../SKILL.md). Resume tickets blocked on owner calls with answers already on the ticket, as in `steps/unblock.md`. Queue rulings with no fresh review for live engineers. Check claims and PRs below and resume incomplete planning before dispatch.

On a T3 finish notice or QA send-back, check only the affected tickets. Read each engineer's thread using the claim's actor.

For an ended engineer, release the claim if the ticket needs more engineering. Otherwise, if the claimed ticket has an open PR with no live review, queue a fresh review if the current head has no review verdict or the latest verdict predates a ruling. Use “Get it reviewed” in `.maestro/skills/engineer/SKILL.md`.

To release a claim, clear the assignee and set the status to open, or to blocked if it is already blocked or awaits an owner answer. Guard that one update by its actor and observed status (in progress or blocked). If the guard fails, reread the ticket. The planner waits for T3 to confirm the engineer and child work ended before releasing a claim.

Count each live engineer session and its reviews as one run, even if only its review is still live. Wait at the epic's limit. A ticket waiting on QA holds no slot.

Pick a ready, unassigned child ticket whose paths no running ticket touches. Check the retry cap in “When review or QA sends it back” in `.maestro/skills/engineer/SKILL.md`.

At the cap, check the spec, size and open calls. Fix or split the ticket with `steps/unblock.md`. Approve the first retry and ask the chief of staff to approve later ones. Record each approval before assigning (see “Ticket board”).

Pick a model suited to the work. Start the engineer in the ticket's worktree. Give it the worktree path, ticket ID, prior work and any existing PR. Let T3 notify you when it finishes.
