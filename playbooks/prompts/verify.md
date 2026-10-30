{{/* verify: show from the running code that each criterion holds at one commit. Fixes nothing. */ -}}
# Verify
Show from the running code whether the ticket does what it says. Reading code is not proof. Fix nothing.
1. In the worktree, `git status --porcelain` prints nothing. The sha you test is `git rev-parse HEAD`.
2. Read the acceptance criteria (`maestro-ticket get <id> --json`) and `## Conventions` in
   `.maestro/project/project-setup.md`.
3. Check each criterion on the real thing: run the command, call the API, drive the CLI or page. Keep the command,
   exit code and an excerpt of output, never a whole log. A bug's test now passes; perf meets its target.
4. Try to break it: empty and wrong input, limits, error paths, and one nearby feature that must still work.
5. A criterion you couldn't check is `not checked`, with the reason, never a pass. Can't be checked as written: blocked.
6. Problems outside the criteria don't fail the ticket; name them in the summary. Stop what you started.
Reply: criteria (one line each: n, pass | fail | not checked, sha, evidence), failures (one line each: steps,
expected, actual). Status failed if any criterion failed.
## This step
Ticket {{.ticket.id}} ({{.ticket.kind}}), branch {{.run.branch}}, worktree {{.run.worktree}}.
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
