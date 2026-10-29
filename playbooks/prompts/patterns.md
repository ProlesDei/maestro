{{/* patterns: judge adopted changes from their numbers, and find the one pattern most worth fixing next.
mode: "" (both parts in order) | measure | find
with: none. Optional with.logs: the output path of the runner's `logs --since`; else a `logs` step's output_path. Optional with.usage: the output path of `maestro-usage report`.
reply: measure: verdicts, tuning_reverts, other_reverts. find: pattern, count, evidence, change, expected_effect, measure, tasks
An unknown mode fails render, so lint catches a misspelled one.
*/ -}}
# Find a pattern
Read; edit nothing. The runner counted; you judge.
Read the log files and lines the logs output lists, under `logs/` in the git common dir's `maestro/` folder, and the
events of the tickets they name (`maestro-ticket events --ticket <id> --json`). A line that won't parse: give its
`file:line`; don't guess. Never copy a line that holds a secret.
{{- if not (or (eq .step.mode "") (eq .step.mode "measure") (eq .step.mode "find"))}}{{template "unknown-mode"}}{{end}}
{{- if or (eq .step.mode "") (eq .step.mode "measure")}}
## Measure
Each `"kind":"adopted"` line whose window has passed: compute its metric over the window the same way as its
baseline. Verdict `keep` if it improved by more than the spread, else `revert`, with the numbers. Write them under
`## Measured` in the scratch file.
{{- end}}
{{- if or (eq .step.mode "") (eq .step.mode "find")}}
## Find
Find one pattern across cost per ticket, Tries, escalations and their reasons, time in each status, retro lines,
user corrections, and review and QA failure reasons. It must repeat on different tickets at least as often as the
pattern threshold in `.maestro/project/budgets.json`. Pick the one that costs or fails the most, and propose one
change for it, with its expected effect and the metric and window to measure it. Never propose a change to your own
role's budgets, trust levels or permissions. Write it under `## Pattern` in the scratch file. No pattern repeats
enough: pattern is empty.
{{- end}}
Reply fields:
{{- if or (eq .step.mode "") (eq .step.mode "measure")}} verdicts (each: change, commit, metric, baseline, now,
spread, keep | revert), tuning_reverts (the commit of each `revert` that changed `.maestro/project/budgets.json`, or
empty), other_reverts (each other `revert`: the change and its commit, or empty)
{{- if eq .step.mode ""}};{{else}}.{{end}}
{{- end}}
{{- if or (eq .step.mode "") (eq .step.mode "find")}} pattern (what repeats, in one line, or empty), count,
evidence (the ticket ids and log `file:line`s), change, expected_effect, measure (the metric and window), tasks
(the golden tasks, as `<role>/<job>`, whose playbooks the change touches, or empty).
{{- end}}
## This step
{{- $logs := index .step.with "logs"}}{{if not $logs}}{{with index .steps "logs"}}{{$logs = index . "output_path"}}{{end}}{{end}}
{{if $logs}}Logs {{$logs}}{{else}}No logs output: read the log files since the last cursor yourself{{end}}{{with index .step.with "usage"}}, usage {{.}}{{end}}, scratch {{.run.scratch}}.
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
