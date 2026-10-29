{{/* verify: show from the running app whether the ticket does what it says. Fixes nothing.
mode: "" (the engineer, before submitting) | qa (the QA checkout; also any ticket at in_qa) | guide (prove a draft verify.md once)
with: none
reply: criteria, failures, sha, followups
An unknown mode fails render, so lint catches a misspelled one.
*/ -}}
# Verify
Show from the running app whether the ticket does what it says. Reading code is not proof. Fix nothing.
1. Read the acceptance criteria (`maestro-ticket get <id> --json`) and the definition-of-done rows for this kind in
   `.maestro/project/project-setup.md`. If `.maestro/project/verify.md` exists, follow it to start and drive the app.
2. Check each one the way it says. Command: run it in the worktree; the exit code and test counts. Evidence: run, call
   or click the real app, and save excerpts to the evidence dir as <n>-<slug>.<ext>, never whole logs. Judgment: what
   you looked at and what you concluded, with paths.
3. Try empty and wrong input, limits, error paths, and one nearby feature.
4. Anything you couldn't check is `not checked`, with the reason, never a pass. If it can't be checked as written: blocked.
5. Problems outside the criteria don't fail the ticket. List them under followups. Stop what you started.
{{- if or (eq .step.mode "qa") (and (eq .step.mode "") (eq (print (index .ticket "status")) "in_qa"))}}
## QA
You test the checkout of exactly what was reviewed, never the branch. Rows that only the merge queue proves are
`later`; every other row is yours, so a row you couldn't check means blocked, never done. sha: `git rev-parse HEAD`
in the worktree.
{{- else if eq .step.mode "guide"}}
## Prove the guide
Follow the draft guide `verify.md` next to the scratch file, not `.maestro/project/verify.md`, on one behaviour from
`project-setup.md` or the README. Note each place the guide was wrong or missing a step under `## Guide fixes` in the scratch file, as a failure.
{{- else if eq .step.mode ""}}
## Before submitting
Rows that review, QA or the merge queue prove are `later`, never pass or fail.
{{- else}}{{template "unknown-mode"}}
{{- end}}
Reply fields: criteria (one line each: id, pass | fail | later | not checked, evidence path), failures (one line each:
steps, expected, actual, path), sha (the commit you tested), followups (possible new tickets, or empty). Status is failed if
any criterion failed.
## This step
Ticket {{.ticket.id}} ({{.ticket.kind}}), worktree {{.run.worktree}}, evidence dir {{.run.evidence_dir}}, scratch {{.run.scratch}}.
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
