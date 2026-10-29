{{/* plan: decide how the ticket gets done before anything is edited.
mode: "" (a build) | pattern (the build copies an example) | bug (root cause) | infrastructure | spec (a workstream)
with: none
reply: plan, example, risks; bug adds cause, also_at; infrastructure: plan, in_repo, commands, check, smoke, rollback, production, cost, risk; spec: outcome, scope, non_goals, approach, risks, order
An unknown mode fails render, so lint catches a misspelled one.
*/ -}}
# Plan
Read; edit nothing. Decide how the ticket gets done, so the next step only has to follow it.
1. Read the ticket (`maestro-ticket get <id> --json`): goal, acceptance criteria, constraints, pointers.
   Read the scratch file, and the `.maestro/project/memory/` entries for the areas the ticket names.
2. Find the closest thing the code already does the same way (a similar command, endpoint or test), by path.
3. Write the plan to the scratch file under `## Plan`: the files to change or add and the approach, in a few lines;
   each acceptance criterion with the test or check that proves it; each risk (behaviour callers rely on, data,
   security, performance) with how it's covered.
4. If nobody can prove a criterion, or the plan needs a decision the ticket doesn't make: blocked.
{{- if eq .step.mode "pattern"}}
## Follow an example
The build copies the example from step 2 line by line, so name the file and the lines. No example in the code: blocked.
{{- else if eq .step.mode "bug"}}
## Root cause
Read the repro under `## Baseline` in the scratch file. Find the cause, not where it shows: the `file:line`, why it
happens, the smallest fix, and every other place the same cause appears. Write them under `## Cause`.
{{- else if eq .step.mode "infrastructure"}}
## Infrastructure
Write under `## Infrastructure`: what's needed and why; where, per "Release" in `.maestro/project/project-setup.md`;
whether it changes files in the repo; the exact commands for anything else, with secrets by variable name only;
the runner check or smoke check that proves it works; the rollback; whether it touches production; what it costs.
{{- else if eq .step.mode "spec"}}
## Spec
This plans a workstream, not one change. Read `## Goal` in the scratch file if it's there. Write under `## Spec`:
the outcome, scope and non-goals, the approach, the risks, and the order of work as pieces an engineer can finish
one at a time.
{{- else if ne .step.mode ""}}{{template "unknown-mode"}}
{{- end}}
Reply fields:
{{- if eq .step.mode "infrastructure"}} plan (the scratch path and heading), in_repo (true if it changes files in the
repo, else false), commands (each, or empty), check (the name for the runner's `check <name>`, or empty), smoke (the
environment for `smoke <env>`, or empty), rollback, production (true or false), cost (one line), risk (one line).
{{- else if eq .step.mode "spec"}} outcome, scope, non_goals, approach, risks (one line each), order (one line per piece).
{{- else}} plan (the scratch path and heading), example (the file and lines from step 2), risks (one line each)
{{- if eq .step.mode "bug"}}, cause (`file:line` and why), also_at (other places, or empty){{end}}.
{{- end}}
## This step
Ticket {{.ticket.id}}, scratch {{.run.scratch}}.
{{- with .run.worktree}} Worktree {{.}}.{{end}}
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
