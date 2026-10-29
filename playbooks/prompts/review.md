{{/* review: judge a change against its ticket, precisely enough to fix without asking. Fixes nothing.
mode: none
with: none
reply: sha, findings, must_fix, secret. The playbook's gates compare sha with the handoff's sha:, and pass when must_fix is empty and secret is false.
*/ -}}
# Review
Read; edit nothing. Judge the change against its ticket and list what's wrong precisely enough to fix without asking.
1. Read the ticket, its acceptance criteria and its latest `Handoff:` note: its `branch:`, `sha:` and `author-family:`
   lines and the check results.
2. `git rev-parse <branch>` must equal the handoff's `sha:`. If the branch is missing or the sha differs: failed, with both.
3. Read `git diff <main branch>...<sha>`, the whole of each changed file at that sha (`git show <sha>:<path>`; never
   check it out), and the callers of changed functions.
4. Check correctness: each criterion is implemented and a test proves it; edge cases, errors, limits, ordering.
   Security: input validation, authorization, injection, secrets in code or tests, unsafe defaults.
   Maintainability: it matches the code around it, with no duplication, dead code or change beyond the ticket.
   Tests: they'd fail if the change were wrong; nothing skipped or weakened.
5. Each finding is `must fix` (wrong, unsafe, a criterion unmet, changed behaviour untested) or `suggestion`, with
   `file:line`, what's wrong, why, and a suggested fix.
6. A secret in the diff, or a security hole that's live: reply blocked with secret true, its `file:line` and how bad it
   is, never the value.
Reply fields: sha (the head you reviewed), findings (one line each: severity, `file:line`, what, why, fix), must_fix
(the must-fix findings again, or empty), secret (true or false).
## This step
Ticket {{.ticket.id}}, {{with .run.branch}}branch {{.}}{{else}}the branch in the handoff's `branch:` line{{end}}, main branch {{.project.main_branch}}.
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
