{{/* build: make the change with tests and commit it clean. with: mode (feature, bug, refactor or perf). */ -}}
# Build
Make the change the plan or the Kickback asks for, prove it with tests, and leave the worktree clean.
1. `git rev-parse --abbrev-ref HEAD` in the worktree must print the branch below. If not: failed.
2. Read the ticket and `## Plan` in the scratch file, if it's there. Match the code the plan names.
3.
{{- if eq .step.with.mode "bug"}} Commit a test that fails because of the bug, alone. Fix at the cause; undo the fix once to see it fail.
{{- else if eq .step.with.mode "refactor"}} Commit passing tests that pin today's behaviour. Change in small commits, green after each.
{{- else if eq .step.with.mode "perf"}} Measure with the plan's command, change only what the numbers show, measure again. Short of target: failed.
{{- else}} Write each criterion's test first; see it fail for the right reason, not a typo. Then make it pass.{{end}}
4. Run each command under `## Checks` in `.maestro/project/project-setup.md` until it passes. Never skip, weaken or
   edit a test to make it pass. A pinned test that must change, or a dependency the ticket doesn't name: blocked.
5. Touch only what the ticket needs. `git add` your files by path and commit, saying what changed and why;
   `git status --porcelain` prints nothing.
{{- if hasPrefix .ticket.latest "Kickback:"}}
Kickback: review sent this back. In the latest `Kickback:` comment, fix and commit each `auto-fix` finding and skip
`no-op`. If any `ask-user` finding is there, then reply blocked with its question. Never guess an answer.
{{- end}}
Reply: commit (one sha), files, tests (command, exit code, counts; perf adds before and after), items (each
`auto-fix` finding and what you changed, or empty).
## This step
Ticket {{.ticket.id}}, mode {{.step.with.mode}}, branch {{.run.branch}}, worktree {{.run.worktree}}, scratch {{.run.scratch}}.
{{- with index .steps "checks"}}{{if eq (print (index . "status")) "failed"}} Checks failed; start from {{index . "output_path"}}.{{end}}{{end}}
{{- with index .steps "verify"}}{{with index . "reply"}}{{with index . "failures"}} Verify failed: {{json .}}.{{end}}{{end}}{{end}}
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
