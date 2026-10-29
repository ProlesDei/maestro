# Build
Make the change the plan asks for, prove it with tests, and leave the worktree clean.
Reply fields: commit, files, tests.
## This step
Ticket {{.ticket.id}}, branch {{.run.branch}}, worktree {{.run.worktree}}, plan in {{.run.scratch}}.
{{- with .steps.checks.output_path}} Checks failed; start from {{.}}.{{end}}
{{- with .steps.verify.reply.failures}} Verify failed: {{json .}}.{{end}}
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
