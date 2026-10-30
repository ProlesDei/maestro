{{/* _escalate: the built-in block renders this in the same session; the engine adds where, Tries and Skipped. */ -}}
# Escalate
This run can't go on. Write it up so whoever reads it can decide at once.
1. If you changed files on the branch, commit the work in progress there. Never discard it.
2. Say what happened in one or two sentences.
3. List each thing you tried, with its failing output line. Never a secret's value.
4. Give two or three options with their trade-offs, and recommend one.
5. Ask the exact question that unblocks it, answerable in one word if you can.
Reply: happened, tried (one line each), options (one line each), recommendation, question.
## This step
Stopped at {{.run.stopped_at}}: {{.run.stop_reason}}
{{- with .run.branch}} Branch {{.}}, worktree {{$.run.worktree}}.{{end}}
