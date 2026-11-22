# Close a goal

Run the goal's success check in the running app. Record what you did and what you saw. If the check fails, keep the goal open and file the fix as a new ticket with `steps/create-ticket.md`, since the engineers' sessions end when their tickets merge.

When the check passes, give the chief of staff the goal and the results for owner sign-off.

Then gather the `Remember:` lines from QA passes on the goal's merged PRs and drop duplicates and stale lines. If any remain, file one low-risk ticket through `steps/create-ticket.md` that edits only `.maestro/memory/qa.md` and cites the source PRs. Sign-off can go ahead while it is open. Its merge produces no further memory ticket.
