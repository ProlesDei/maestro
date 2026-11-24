# Unblock work

Group blocks by cause. Use “Whose call” in `.maestro/skills/chief-of-staff/SKILL.md` to find owner calls. Answer the rest from the goal and specs, and tell each affected agent.

If the spec needs a fix, use `steps/update-ticket.md`. Keep a broken-base repair on one ticket. To split another ticket, set the original to blocked with its assignee and status as guards. File its parts with the original's labels and the prerequisites each needs using `steps/create-ticket.md`. Point its dependents at the parts, then remove the original's prerequisite links. If it is a stop fix, replace it in the active stop with its parts and apply the stop rule again. See “The chief of staff calls a stop” in the [planner skill](../SKILL.md). Close the original with its assignee and blocked status as guards, then close its PR.

Record the question and set each waiting ticket to blocked in one update guarded by its assignee and status. If no ticket is waiting, record the question in a comment on the epic. Send the chief of staff what you tried, which work must wait, and the decision you need.

Record the answer on each affected ticket. Once all its owner calls have answers, reopen a blocked ticket if unassigned or return it to in progress if claimed. Guard that update by the assignee and blocked status. If the guard fails, reread the ticket. Send the recorded answers to the live claim holder's thread for in-progress tickets. If the holder ended, follow the guarded reassignment in `steps/assign-ticket.md`. Keep the existing PR and worktree. Then recheck the QA queue and assign ready work.

For an owner call on the epic, record the answer in a comment that points to the question. Once all planning calls have answers, resume `steps/plan-goal.md`. Assign ready work once planning is complete.
