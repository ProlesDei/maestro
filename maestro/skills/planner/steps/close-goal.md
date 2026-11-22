# Close a goal

If the owner reports a failure, use it as the failed check. Otherwise start the app from the merged base in a fresh worktree, the way `.maestro/memory/qa.md` says, run the goal's success check, record what you did and saw, and stop what you started. On failure, keep the goal open and file a fix ticket under its epic with `steps/create-ticket.md`.

When the check passes, give the chief of staff the goal and the results for owner sign-off.

Then gather the `Remember:` lines from QA passes on the goal's merged PRs and drop duplicates, stale lines and lines `.maestro/memory/qa.md` already holds. If any remain, add them to the goal's open memory ticket, or file one low-risk ticket through `steps/create-ticket.md` that edits only `.maestro/memory/qa.md` and cites the source PRs. Sign-off can go ahead while it is open. Its merge produces no further memory ticket.
