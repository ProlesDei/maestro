# Assign a ticket

On restart, defer all engineer, review and QA starts and handoffs that trigger them. Recover both QA slots and close verified merges as the [planner skill](../SKILL.md) directs. Apply any recorded stop, check whether to lift it, and route QA verdicts. Resend recorded owner calls that still lack answers to the chief of staff without recording them again. Resume tickets whose calls all have answers as in `steps/unblock.md`. Check claims and PRs below. Resume incomplete planning before dispatch. When dispatch resumes, follow the replacement handoff rule in “Sessions”. Include each ticket's rulings and the latest usable review verdict for its current head in the handoff to its live engineer, per “Recover a review”.

Check reaped tickets on your goal and those in a T3 finish notice or QA send-back. A reaped ticket with a saved PR stays in progress for review or QA. Send a current verdict to the live engineer. If the engineer ended and the verdict calls for fixes, use `maestro-reassign` with the observed assignee, the ticket’s worktree and its branch. It keeps the existing PR and reserves the paths for a fresh engineer in one guarded update. Give that engineer the remaining fixes. If the PR only needs review, follow [Recover a review](../SKILL.md#recover-a-review).

Use `maestro-release-ticket` to release a claim explicitly. Pass the observed assignee, status and any reservation token. Leave an owner-blocked ticket blocked. If the guard fails, reread the ticket. The planner checks T3 for ended engineer, child and review runs before releasing.

Count one run per ticket with a live engineer or review. Wait at the epic’s limit. QA does not count toward that limit.

Pick a ready, unassigned child ticket. Check the retry cap in “When review or QA sends it back” in `.maestro/skills/engineer/SKILL.md`.

At the cap, check the spec, size and open calls. Fix or split the ticket with `steps/unblock.md`. Approve the first retry and ask the chief of staff to approve later ones. Record each approval before assigning (see “Ticket board”).

Pick the provider as “Sessions” directs. Before launch, use `maestro-reserve` for a new checkout or `maestro-reassign` for an existing one. If a path is held, leave the ticket on the board and try another. If launch fails before the child binds, release the reservation once T3 confirms the launch settled.

For a new ticket checkout, start an engineer child in a new T3 worktree. For a resume, use T3’s existing-worktree launch with the reserved absolute path and branch. Link the thread to this planner and wait for it by thread ID if T3 gives no task ID.

Give the engineer the ticket, reservation token, prior work and any existing PR. In the brief, replace the engineer skill’s first claim step with `maestro-engineer-claim` using the ticket and token. Include the current usable review or QA verdict if it needs engineer action. Save the new thread and any task ID with `maestro-bind-child`. Let T3 notify you when it finishes.
