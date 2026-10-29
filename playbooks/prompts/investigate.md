{{/* investigate: find out what's true, with evidence someone else can check. Fixes nothing.
mode: "" (a scout's question) | goal (what a workstream's planning ticket asks for) | escalation (the facts behind an Escalation:)
with: none. Works with or without a ticket; the question is the ticket's, or the step's with.question.
reply: "": question, answer, confidence, findings, unknowns, next_tickets, partial. goal: outcome, constraints, areas, decided, above_authority. escalation: question, options, facts, sources, recommendation, within_authority
An unknown mode fails render, so lint catches a misspelled one.
*/ -}}
# Investigate
Find out what's true, with evidence someone else can check. Don't fix what you find.
1. Frame it under `## Question` in the scratch file: the question in one sentence, the possible answers, what
   evidence would settle each, and where to look.
2. Gather: read code, docs, tickets (`maestro-ticket get`, `list`, `events`), `.maestro/project/memory/` and git history
   (`git log`, `git show`). Each finding gets a source: `file:line`, a ticket id and comment, or a command and its output
   path in the evidence dir.
3. Experiment only when reading can't settle it, only with commands your session may run, and only in a temp dir
   inside the scratch file's folder, never on a branch that ships. If you can't run it, it's an unknown.
4. Weigh each possible answer: the evidence for and against, and how sure you are. A claim without a source is an unknown.
5. Write the findings under `## Findings` in the scratch file. If you stop early, say what's missing under partial.
{{- if eq .step.mode ""}}
Reply fields: question, answer (one or two sentences), confidence (high | medium | low), findings (one line each: the
claim and its source), unknowns (one line each, or empty), next_tickets (one line each: title, type, kind, role, why;
or empty), partial (what's missing, or empty).
{{- else if eq .step.mode "goal"}}
## Goal
The question is what the ticket asks the workstream to deliver. Write under `## Goal`: the outcome, the constraints,
the code areas it touches, what's already decided (with where), and anything that needs a decision above the
Planner: scope, cost, risk to users or data, a product decision or a gate.
Reply fields: outcome, constraints (one line each), areas (code paths), decided (each decision and its source),
above_authority (true if anything needs a decision above the Planner, else false).
{{- else if eq .step.mode "escalation"}}
## Escalation
The question is the one in the ticket's latest `Escalation:` comment. Find the facts that answer it, and what each
option would cost. The Planner may decide criteria, splitting or re-routing, order, and a known limit; scope, cost,
risk to users or data, product decisions and gates are above them.
Reply fields: question (the exact question), options (each with its trade-offs), facts (one line each),
sources (each fact's source), recommendation, within_authority (true if the Planner may decide it, else false).
{{- else}}{{template "unknown-mode"}}
{{- end}}
## This step
{{with index .ticket "id"}}Ticket {{.}}, {{end}}{{with index .step.with "question"}}question: {{.}}, {{end}}scratch {{.run.scratch}}, evidence dir {{.run.evidence_dir}}.
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
