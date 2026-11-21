# Assign a ticket

Pick a ready, unassigned child ticket from the board. Check that no running ticket touches the same paths. Pick a model suited to the work.

Give the engineer the ticket ID, prior work it needs, and a unique board actor for this run. Start it in the ticket's worktree and use the board claim as the record of who took it.

If work must resume after a run ends, confirm the session ended. In one update guarded by its actor and in-progress status, set the ticket to open and clear the assignee. If the guard fails, reread the ticket. Assign it afresh when ready, with any existing PR. Never release a claim based on lease expiry alone.
