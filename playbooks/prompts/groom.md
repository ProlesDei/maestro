{{/* groom: find what keeps a workstream's board from being accurate, and draft a fix for each. Writes nothing.
mode: none
with: none. Optional with.cursor (else the playbook's .cursor, else 0): the events seq to read from. Optional with.workstream: the owner role whose tickets to read.
reply: fixes, next_seq
*/ -}}
# Groom the board
Read; write no ticket. Find what keeps the board from being accurate, and draft a fix for each; the engine applies them.
1. Read `maestro-ticket list --json` for the workstream's tickets, `maestro-ticket events --since <cursor> --json`
   (keep its `next_seq`), and `get` for each ticket you judge. The stale-work threshold is in `.maestro/project/budgets.json`.
2. Draft a fix, with the ticket id and the version you read, for each of these:
   - `backlog` tickets that are now ready: transition to `todo`.
   - Dependencies missing, wrong or circular: an update with add_deps or remove_deps.
   - `blocked` tickets that only wait on another ticket: two fixes in this order, an update with add_deps, then a
     transition back to `blocked_from` (`todo` for a ticket created blocked). An update doesn't change the version.
   - Duplicates: transition the newer to `cancelled`, with the note `Duplicate of T-<n>`.
   - Missing acceptance criteria, kind, role or owner: a comment with the fix, or with what's missing.
   - Priority out of order: a comment with the fix.
   - Expired leases (`lease_expired` events; a ticket whose `lease_expiries` keeps growing is a candidate for
     escalation), and `in_progress` tickets with no lease and no event past the threshold: a comment for their role.
3. Never draft a move to `done`. Leave alone any ticket with a live lease.
Reply fields: fixes (each: ticket, version, command: transition | update | comment, and that command's fields:
to and from for a transition; add_deps, remove_deps and areas, as lists, for an update; note, the transition note
or the comment body), next_seq.
## This step
{{- $cursor := index .step.with "cursor"}}{{if not $cursor}}{{$cursor = index $ "cursor"}}{{end}}
Cursor {{or $cursor 0}}{{with index .step.with "workstream"}}, workstream {{.}}{{end}}.
{{- if .retry}} Your last reply was missing: {{.retry.reason}}.{{end}}
