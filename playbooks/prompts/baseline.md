{{/* baseline: pin down how the code behaves today, committed before the change, and rerun it after.
mode: repro (a failing test for a bug) | characterise (tests that pass today, for a refactor) | trace (a benchmark) | after (rerun the baseline on the changed code)
with: none
reply: baseline, commit; characterise adds gaps, broke; trace adds hotspots; after replies baseline only
An unknown mode fails render, so lint catches a misspelled one.
*/ -}}
# Baseline
Pin down how the code behaves today, so the change can be judged against it. Change no code under test.
1. `git rev-parse --abbrev-ref HEAD` in the worktree must print the branch below. If not: failed. If
   `git status --porcelain` prints a deliberate break left by a try that stopped, undo it; anything else: failed, with the list.
2. Save every output under the evidence dir, as excerpts, never whole logs.
3. Write what you did under `## Baseline` in the scratch file: the kind, the exact command, the result, and the commit.
   The next steps read it there.
4. Never change a baseline to fit a result.
{{- if eq .step.mode "repro"}}
## Repro
Write the smallest test that fails on the unchanged code for the reason the ticket describes (a script if no test
can reach it). Run it, save the output as `repro-before.txt` and commit it. If it passes, fails for another
reason, or the behaviour is by design: blocked, with what you tried. Never guess a fix.
{{- else if eq .step.mode "characterise"}}
## Characterise
Write tests at the public edges the refactor could touch (exported functions, CLI output, API responses) that pass
on the unchanged code, and list what can't be tested. Commit them. Then prove they can fail: make one deliberate
break in the code, not the tests, run them, see one fail, and undo the break until `git status --porcelain` prints nothing.
{{- else if eq .step.mode "trace"}}
## Trace
If the ticket names no target, or the unchanged code already meets it: blocked, with the numbers.
Write a benchmark or trace script for the slow path and commit it. Run it at least five times on the unchanged code.
Save the numbers (median and spread) and a profile, and name the hot spots by `file:line`.
{{- else if eq .step.mode "after"}}
## After
Rerun the baseline from `## Baseline` the same way: same command, run count and machine. Change nothing in it.
Add the new numbers next to the earlier ones under `## Baseline`. If the ticket's target isn't met, or the change
isn't better by more than the earlier spread: failed, with both sets of numbers.
{{- else}}{{template "unknown-mode"}}
{{- end}}
Reply fields: baseline (kind, command, result: pass | fail, and median and spread for a trace)
{{- if ne .step.mode "after"}}, commit{{end}}
{{- if eq .step.mode "characterise"}}, gaps (what can't be tested, one line each, or empty), broke (the break you made and the test that failed){{end}}
{{- if eq .step.mode "trace"}}, hotspots (`file:line` each){{end}}.
## This step
Ticket {{.ticket.id}}, branch {{.run.branch}}, worktree {{.run.worktree}}, scratch {{.run.scratch}}, evidence dir {{.run.evidence_dir}}.
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
