# Plan
Read; edit nothing. Decide how the ticket gets done, so the build step only has to follow it.
Reply fields: plan (the path), risks (one line each).
## This step
Ticket {{.ticket.id}}, worktree {{.run.worktree}}, scratch {{.run.scratch}}.{{if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
