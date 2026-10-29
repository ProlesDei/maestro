{{/* curate: decide what the team remembers. Memory is true, small, and cites its evidence and files.
mode: "" (all four parts in order) | proposals | flagged | budget | commit
with: none. Optional with.scan: the output path of the runner's `memory scan`; else a `scan` step's output_path.
reply: proposals, flagged: verdicts, changed. budget: archived, size_before, size_after, changed. commit: commit, files
An unknown mode fails render, so lint catches a misspelled one.
*/ -}}
# Curate memory
Memory lives in `.maestro/project/memory/` in the worktree below: project memories by kind, role lessons in
`roles/<role>.md`, retired ones in `archive/`. The inbox is `memory-inbox/` in the git common dir's `maestro/` folder.
Write only in that memory folder, delete only inbox files you handled, and never repeat a secret: say which proposal held one.
{{- if not (or (eq .step.mode "") (eq .step.mode "proposals") (eq .step.mode "flagged") (eq .step.mode "budget") (eq .step.mode "commit"))}}{{template "unknown-mode"}}{{end}}
{{- if or (eq .step.mode "") (eq .step.mode "proposals")}}
## Proposals
Each proposal in the scan: accept, merge into an existing memory, or reject, with the reason. Accept only durable
facts (a convention, pitfall, decision, command, codebase fact or role lesson) that cite evidence, name their `about`
files and aren't already there. Reject one-offs, ticket chatter, copies of code, anything the code or git history
already says, and anything holding a secret. A memory that `git status` shows as uncommitted was written by an
earlier try of this step: it counts as accepted, not as already there. Write accepted ones with `about`, `source`, `evidence` and a
`git hash-object` checksum per `about` file. Delete rejected inbox files.
{{- end}}
{{- if or (eq .step.mode "") (eq .step.mode "flagged")}}
## Flagged
Each memory the scan flagged, against its `about` files now: still true, refresh the checksums; partly wrong,
correct it; no longer true, move it to `archive/`.
{{- end}}
{{- if or (eq .step.mode "") (eq .step.mode "budget")}}
## Budget
Measure the memory folder again, after any changes above. Over the scan's budget: move the least-used and oldest
memories to `archive/` until it's under. Never delete one.
{{- end}}
{{- if or (eq .step.mode "") (eq .step.mode "commit")}}
## Commit
Commit the changes in the worktree, with the counts in the message. Then delete the inbox files of the proposals
your earlier verdicts in this run accepted or merged, and no other.
{{- end}}
Reply fields:
{{- if or (eq .step.mode "") (eq .step.mode "proposals") (eq .step.mode "flagged")}} verdicts (each: the proposal or
memory path, the verdict: accept, merge or reject for a proposal, true, corrected or archived for a memory, and the reason),
{{- end}}
{{- if or (eq .step.mode "") (eq .step.mode "budget")}} archived (each path moved), size_before, size_after,
{{- end}}
{{- if ne .step.mode "commit"}} changed (each memory path you wrote, moved or deleted, or empty).{{end}}
{{- if or (eq .step.mode "") (eq .step.mode "commit")}} commit (or empty), files (each path in it).{{end}}
## This step
{{- $scan := index .step.with "scan"}}{{if not $scan}}{{with index .steps "scan"}}{{$scan = index . "output_path"}}{{end}}{{end}}
Worktree {{.run.worktree}}{{if $scan}}, scan {{$scan}}{{else}}, no scan output: read the inbox and recheck every memory's checksums yourself{{end}}.
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
