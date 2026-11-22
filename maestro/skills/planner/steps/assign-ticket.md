# Assign a ticket

On a T3 finish notice, read the thread's state and result. On restart, do this for each engineering claim using its actor as the thread ID. If a report is missing, check the ticket and PR. If more engineering is needed and T3 confirms the session and its child work ended, reopen the ticket and clear the assignee in one update guarded by its actor and in-progress status. If the guard fails, reread the ticket.

Pick a ready, unassigned child ticket. Check that no running ticket touches the same paths. Check the retry cap in “When review or QA sends it back” in `.maestro/skills/engineer/SKILL.md`.

At the cap, check whether the spec is wrong, the ticket is too big, or a call is missing. Settle missing calls with `steps/unblock.md`. Fix the ticket with `steps/update-ticket.md` or split it with `steps/create-ticket.md`. Record what changed and approve the retry on the ticket before assigning again.

Pick a model suited to the work. Start a fresh T3 child in the ticket's worktree with the ticket ID, any prior work and any existing PR. Let T3 notify you when it finishes.
