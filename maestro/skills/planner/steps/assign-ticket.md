# Assign a ticket

On restart, defer all engineer, review and QA starts and handoffs that trigger them. Use `qa_thread_id` on the epic's tickets to find ended QA sessions in T3. Clean them up as in “A QA session ends”. Close verified merges, apply any recorded stop, check whether to lift it, and route QA verdicts under the [planner skill](../SKILL.md). Resend recorded owner questions that lack answers. Resume tickets whose owner calls all have answers, as in `steps/unblock.md`. Check claims and PRs below. Queue rulings that still need review for live engineers whose tickets have no live review. Resume incomplete planning before dispatch.

On a T3 finish notice or QA send-back, check only the affected tickets. Check engineer and review sessions in T3 by the claim's actor and the ticket's `review_thread_id`.

For an ended engineer, release the claim if the ticket needs more engineering. Otherwise, if the claimed ticket has an open PR with no live review, queue a fresh review if the current head has no review verdict or the latest verdict predates a ruling. Use “Get it reviewed” in `.maestro/skills/engineer/SKILL.md`.

To release a claim, clear the assignee and set the status to open, or to blocked if it is already blocked or awaits an owner answer. Guard that one update by its actor and observed status (in progress or blocked). If the guard fails, reread the ticket. The planner waits for T3 to confirm the engineer, child work and any review ended before releasing a claim.

Count one run per ticket with a live engineer or review. Wait at the epic's limit. QA holds no slot.

Pick a ready, unassigned child ticket whose paths no running ticket touches. Check the retry cap in “When review or QA sends it back” in `.maestro/skills/engineer/SKILL.md`.

At the cap, check the spec, size and open calls. Fix or split the ticket with `steps/unblock.md`. Approve the first retry and ask the chief of staff to approve later ones. Record each approval before assigning (see “Ticket board”).

Pick a model suited to the work. Start the engineer in the ticket's worktree. Give it the worktree path, ticket ID, prior work and any existing PR. Let T3 notify you when it finishes.
