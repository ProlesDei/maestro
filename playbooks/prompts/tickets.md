{{/* tickets: draft new tickets, and for an escalation the answer to post. The engine makes every write, with its own keys.
mode: "" or plan (from `## Spec`) | report (from `## Feedback` on a report) | escalation (from the owner's answer)
with: answer (the owner's choice and answer) for escalation; none otherwise.
reply: tickets; escalation adds comment
An unknown mode, or an escalation step with no with.answer, fails render, so lint catches it.
*/ -}}
# Write tickets
Write no ticket yourself: draft them, and the engine runs each `maestro-ticket` write from your reply.
Put work on the board so an engineer can finish it without asking a question.
1. Read the scratch file, the ticket below if there is one, and the board (`maestro-ticket list --json`), so you
   don't draft what's already there.
2. Each new ticket: a title; type `scout` for work that ships no code (an investigation, which ends at a report its
   owner reads, or an infrastructure request, owned by `operations-manager`), `ship` for the rest; the kind (feature,
   bug, chore, docs, investigation); the role that does it; priority 0 to 3; status `todo`, or `backlog` when it isn't
   ready; deps, existing ticket ids that must be `done` first; after, the positions (from 0) of earlier tickets in
   your list that must be `done` first; areas, the code paths a ship ticket touches, so overlapping work never runs
   at once; and `refactor` or `performance` as playbook where it applies.
3. Each ticket's goal; acceptance criteria that are each testable (a command or an observable behaviour); pointers
   (files, the spec); constraints, if any.
4. Waiting on another ticket is a dependency, never `blocked`. A dependency cycle is refused; fix the list rather than drop it.
{{- if or (eq .step.mode "") (eq .step.mode "plan")}}
## From the spec
One ticket per piece under "order of work" in `## Spec`, in that order, with after for the pieces each one needs.
{{- else if eq .step.mode "report"}}
## From a report
One ticket per piece of recommended work under `## Feedback`, each pointing to the report's findings.
{{- else if eq .step.mode "escalation"}}
## From a decision
Apply the owner's answer below to the escalated ticket. comment is what to post on it: the answer, what
changes, and any corrected body or criteria in full. Draft tickets only when the answer splits the work; otherwise
tickets is empty.
{{- $has := false}}{{range $k, $v := .step.with}}{{if eq $k "answer"}}{{$has = true}}{{end}}{{end}}
{{- if not $has}}{{template "needs-with-answer"}}{{end}}
{{- else}}{{template "unknown-mode"}}
{{- end}}
Reply fields: tickets (each: title, type, kind, role, priority, status, goal, criteria (one line each), pointers, and
where they apply deps, after, areas, constraints, playbook; or empty)
{{- if eq .step.mode "escalation"}}, comment{{end}}.
## This step
{{with index .ticket "id"}}Ticket {{.}}, {{end}}scratch {{.run.scratch}}.
{{- with index .step.with "answer"}} The owner's answer: {{.}}{{end}}
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
