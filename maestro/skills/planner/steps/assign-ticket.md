# Assign a ticket

On restart, defer all engineer, review and QA starts and handoffs that trigger them. Recover the slot and close verified merges as the [planner skill](../SKILL.md) directs. Apply any recorded stop, check whether to lift it, and route QA verdicts. Resend unanswered owner questions to the chief of staff. Resume tickets whose owner calls all have answers, as in `steps/unblock.md`. Check claims and PRs below. Resume incomplete planning before dispatch. When dispatch resumes, resend each live engineer the latest ruling on its ticket.

On a T3 finish notice or QA send-back, check only the affected tickets. Check engineer and review sessions in T3 by the claim's actor and the ticket's `review_thread_id`.

For an ended engineer, release the claim if the ticket needs more engineering. Otherwise, if the claimed ticket has an open PR with no live review, queue a fresh review if the current head has no review verdict or the latest verdict predates a ruling. Use “Get it reviewed” in `.maestro/skills/engineer/SKILL.md`.

To release a claim, clear the assignee and set the status to open, or to blocked if it is already blocked or awaits an owner answer. Guard that one update by its actor and observed status (in progress or blocked). If the guard fails, reread the ticket. The planner waits for T3 to confirm the engineer, child work and any review ended before releasing a claim.

Count one run per ticket with a live engineer or review. Wait at the epic's limit. QA does not count toward that limit.

Pick a ready, unassigned child ticket whose paths no running ticket touches. Check the retry cap in “When review or QA sends it back” in `.maestro/skills/engineer/SKILL.md`.

At the cap, check the spec, size and open calls. Fix or split the ticket with `steps/unblock.md`. Approve the first retry and ask the chief of staff to approve later ones. Record each approval before assigning (see “Ticket board”).

Pick a model suited to the work. If the ticket's worktree exists, start the engineer child in your checkout and give it that path. Otherwise start it in a new T3 worktree. Give it the ticket ID, prior work and any existing PR. Save its task ID as “Sessions” directs. Let T3 notify you when it finishes.
