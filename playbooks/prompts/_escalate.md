{{/* _escalate: the built-in block renders this in the same session, then posts the Escalation: note.
mode: none. with: none. The engine adds where, Tries, Skipped: lines and Return-role to the note itself.
reply: happened, tried, options, recommendation, question
*/ -}}
# Escalate
This playbook can't go on. Write it up so whoever reads it can decide in one go.
1. If you changed files on the branch, commit the work in progress there. Never discard it.
2. Say what happened in one or two sentences, and each thing that was tried with its failing output line, never a
   secret's value.
3. Give two or three options with their trade-offs, and recommend one.
4. Ask the exact question that unblocks it, answerable in one word if you can.
Reply fields: happened, tried (one line each), options (one line each), recommendation, question.
## This step
Stopped at {{.run.stopped_at}}: {{.run.stop_reason}}
{{- with .run.branch}} Branch {{.}}, worktree {{$.run.worktree}}.{{end}}
