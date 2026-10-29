{{/* propose: put the pattern's change where the right person can approve it.
mode: tuning (commit it, or revert a measured one, in the tuning worktree) | proposal (draft proposal tickets; the engine creates and routes them)
with: class (how the bot classed the pattern's change: tuning, prompt, playbook, persona, drop, or empty for no pattern). Reads `## Pattern` and `## Measured` from the scratch file.
reply: tuning: commit, baseline, window. proposal: proposals
An unknown mode, or a step with no with.class, fails render, so lint catches it.
*/ -}}
{{- $has := false}}{{range $k, $v := .step.with}}{{if eq $k "class"}}{{$has = true}}{{end}}{{end}}
{{- if not $has}}{{template "needs-with-class"}}{{end}}
{{- $class := print (index .step.with "class")}}
{{- $proposal := or (eq $class "prompt") (eq $class "playbook") (eq $class "persona") -}}
# Propose a change
Put the change under `## Pattern` in the scratch file, and the reverts under `## Measured`, where the right person
can approve them. Never change your own role's budgets, trust levels or permissions, and never edit playbooks,
prompts, personas or Maestro's rules: those are proposals only. Only the class below decides what's adopted or
proposed; a change classed drop is neither.
{{- if eq .step.mode "tuning"}}
## Tuning
1. In `.maestro/project/budgets.json` in the worktree below, and nothing else:
   {{- if eq $class "tuning"}} make the pattern's change, and{{end}} undo each tuning change `## Measured` says to
   revert (`git revert <commit>`), only if that commit changed nothing but `budgets.json`.
2. Commit, with the old and new value, the baseline, the metric and the window in the message. If the branch already
   has that commit, from a run that stopped, reply it instead of committing again.
3. For a new change, append one line to today's `logs/<YYYY-MM-DD>.jsonl` (UTC) in the git common dir's `maestro/`
   folder: `{"kind":"adopted","change":...,"metric":...,"baseline":...,"window":...,"commit":...}`, so a later loop
   can measure it. If a line with this commit is already there, add none.
Reply fields: commit, baseline (the metric's value before the change), window (when to measure it).
{{- else if eq .step.mode "proposal"}}
## Proposal
Write no ticket. Draft
{{- if $proposal}} one proposal for the pattern's change, class {{$class}}, and{{end}} one to undo each change
`## Measured` says to revert that isn't a tuning, class revert. The engine creates each proposal ticket, posts its
report and routes it to the Chief of Staff.
Reply fields: proposals (each: title, starting `Proposal: `; class; pattern (what repeats, and how often); evidence
(the ticket ids and log `file:line`s); change; expected_effect; measure (the metric and window)).
{{- else}}{{template "unknown-mode"}}
{{- end}}
## This step
Class {{if $class}}{{$class}}{{else}}none (no pattern){{end}}. Scratch {{.run.scratch}}{{with .run.worktree}}, worktree {{.}}{{end}}.
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
