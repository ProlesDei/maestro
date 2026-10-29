# Verify
Show from the running app whether the ticket does what it says. Reading code is not proof. Fix nothing.
Reply fields: criteria, failures. Status is failed if any criterion failed.
## This step
Ticket {{.ticket.id}} ({{.ticket.kind}}), worktree {{.run.worktree}}, evidence dir {{.run.evidence_dir}}.{{if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
