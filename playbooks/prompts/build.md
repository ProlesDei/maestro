{{/* build: make the planned change, or fix a list of items, with tests, and commit it clean.
mode: none. Reads `## Plan`, `## Cause`, `## Baseline` and `## Feedback` from the scratch file when they're there.
with: none. Optional steps it reads when the playbook has them: checks (output_path), verify (reply.failures).
reply: commit, files, tests, items
*/ -}}
# Build
Make the change the plan or the fix list asks for, prove it with tests, and leave the worktree clean.
1. `git rev-parse --abbrev-ref HEAD` in the worktree must print the branch below. If not: failed.
2. Read the scratch file. Change what `## Plan` says, the way its example does, or fix each item under `## Feedback`
   that says fix. A bug's fix goes at the cause under `## Cause`.
3. Give each acceptance criterion a test that fails without the change. Run the test command from
   "Automated checks" in `.maestro/project/project-setup.md` until it passes. If `## Baseline` is there:
   a repro must now pass; characterisation tests stay exactly as committed, and you commit in small steps
   with them passing after each; a trace means you change only the hot spots it names.
4. `git add` only your files, then commit, saying what changed and why. `git status --porcelain` prints nothing.
5. Touch only what the ticket needs: no drive-by refactors or unrelated formatting. Never edit a baseline to make it
   pass, and never skip or weaken a test. If it needs a dependency the ticket doesn't ask for: blocked.
Reply fields: commit, files, tests (command, exit code, counts), items (for a fix list: each item's number and the
files changed, or why not; otherwise empty).
## This step
Ticket {{.ticket.id}}, branch {{.run.branch}}, worktree {{.run.worktree}}, scratch {{.run.scratch}}.
{{- with index .steps "checks"}}{{if eq (print (index . "outcome")) "fail"}} Checks failed; start from {{index . "output_path"}}.{{end}}{{end}}
{{- with index .steps "verify"}}{{with index . "reply"}}{{with index . "failures"}} Verify failed: {{json .}}.{{end}}{{end}}{{end}}
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
