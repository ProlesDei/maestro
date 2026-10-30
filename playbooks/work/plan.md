{{/* plan: decide how the ticket gets done, before any edit. with: mode (feature, bug, refactor or perf). */ -}}
# Plan
Read; edit nothing. Decide how the ticket gets done, so the build only has to follow it.
1. Read the ticket (`maestro-ticket get <id> --json`): goal, acceptance criteria, constraints, pointers, and any
   `Answer:` comment, which settles an earlier block.
2. Find the closest code that already does the same kind of thing (a command, endpoint or test), by path.
3. Name each file to change or add, and the approach, in a few lines.
4. Give each acceptance criterion the test or check that proves it.
5. List each risk (behaviour callers rely on, data, security, speed) and how the plan covers it.
6. Write steps 2 to 5 under `## Plan` in the scratch file. A criterion nobody can prove, or a decision the ticket
   doesn't make: blocked.
{{- if eq .step.with.mode "bug"}}
Bug: find the cause, not where it shows: the `file:line`, why it happens, and each other place with the same cause.
Plan the smallest test that fails because of the bug.
{{- else if eq .step.with.mode "refactor"}}
Refactor: behaviour must not change. Plan tests at the public edges that pin what the code does today, and an order
of small steps that keeps them green.
{{- else if eq .step.with.mode "perf"}}
Perf: name the target number, the command that measures it, the run count and the machine. No target: blocked.
{{- end}}
Reply: plan (the scratch path and heading), risks (one line each).
## This step
Ticket {{.ticket.id}}, mode {{.step.with.mode}}, worktree {{.run.worktree}}, scratch {{.run.scratch}}.
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
