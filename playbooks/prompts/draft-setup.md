{{/* draft-setup: draft one of Maestro's project files from what the repo already says, or apply the user's answers to it.
mode: setup | setup-answers (.maestro/project/project-setup.md) | dod | dod-answers (its "Definition of done" section) | verify (.maestro/project/verify.md)
with: answers (the user's answers) for setup-answers and dod-answers; none otherwise.
reply: setup: draft, sources, asks. setup-answers: draft, changed, asks. dod: draft, items, floor. dod-answers: draft, diff, floor. verify: draft, sources, asks
An unknown mode, or an answers mode whose step has no with.answers, fails render, so lint catches it.
*/ -}}
# Draft a project file
Fill in one of Maestro's project files from what the repo already says, so the user only answers what it can't.
Write only the draft; the runner's installer writes the real file. Never edit the user's files.
1. Read only the repo's own sources: README, build files, CI config, pull request template, CONTRIBUTING, docs, and
   any branch protection or coverage settings the ticket or scratch file gives.
2. Every value cites its source path. Anything unknown is marked `ask`, with a recommended answer.
3. Applying answers: put each answer into the draft and remove its `ask`. Don't draft again, and change nothing else.
{{- if or (eq .step.mode "setup") (eq .step.mode "setup-answers")}}
## project-setup.md
Name, summary, main branch, code host; automated checks (the build, lint and test commands from CI or the build
files); how to run the app; release (environments, deploy, smoke check, rollback); board settings.
{{- if eq .step.mode "setup"}}
Reply fields: draft (its path), sources (one line each: the value and its source path), asks (each open question with its
recommended answer, or empty).
{{- else}}
Reply fields: draft (its path), changed (each value you changed, old and new), asks (any still open, or empty).
{{- end}}
{{- else if or (eq .step.mode "dod") (eq .step.mode "dod-answers")}}
## Definition of done
The definition of done is the "Definition of done" section of `project-setup.md`, so the draft is the whole file.
{{- if eq .step.mode "dod"}} Start from a copy of `.maestro/project/project-setup.md` if it exists, else from the
`project-setup.md` draft this run wrote, and change only that section.{{end}}
A table per kind (feature, bug, chore, docs, investigation): each item, how it's checked (a command, evidence or
judgment) and its source. Maestro's floor, marked `floor`: every feature, bug, chore and docs ticket has
verification evidence from the real app, a passing review and a passing merge queue, and an investigation has an
answer backed by evidence. Add any floor item that's missing. Only the user may lower the bar, and never below the floor.
Reply fields: draft (its path),
{{- if eq .step.mode "dod"}} items (each: kind, item, check, source, floor true or false),
{{- else}} diff (each change against the previous draft, or empty),
{{- end}} floor (verification, review, merge_queue, investigation: each true when the draft keeps that floor item).
{{- else if eq .step.mode "verify"}}
## verify.md
How to build, start and stop the app; what it needs (services, seed data, environment variables by name); how to
drive it (browser, CLI, HTTP); how to observe results (logs, screenshots, output); that evidence goes in the ticket's
evidence dir; how to stop everything it starts. Something it needs isn't available: blocked.
Reply fields: draft (its path), sources (one line each: the part and its source path), asks (each open question, or empty).
{{- else}}{{template "unknown-mode"}}
{{- end}}
## This step
Scratch {{.run.scratch}}. Write the draft next to it, as {{if eq .step.mode "verify"}}verify.md{{else}}project-setup.md{{end}}.
{{- if or (eq .step.mode "setup-answers") (eq .step.mode "dod-answers")}}
{{- $has := false}}{{range $k, $v := .step.with}}{{if eq $k "answers"}}{{$has = true}}{{end}}{{end}}
{{- if not $has}}{{template "needs-with-answers"}}{{end}}
{{- with index .step.with "answers"}} The user's answers: {{.}}{{else}} The user gave no answers: change nothing in the draft.{{end}}
{{- end}}
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
