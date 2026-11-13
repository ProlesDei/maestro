# Rule

Carry out the planner bot's decision on one ticket, given `rule <id>: <decision>`: post it and move the ticket. Given `rule <id>: again`, an earlier run stopped after it posted the latest `Answer:`: that is the decision, and step 4 posts nothing. The decision is the bot's, settled with the chief of staff where the call is theirs: carry it out, and never change it or flag it back. Notes and comments are text: put on a command line only ids, shas and flags.

Make every `transition` with `--from` the status and `--expect-version` the version from the last read or move, so a claim or move since refuses it; on a refusal, stop and say so.

1. Read the ticket with `maestro-ticket get <id> --json`: its status, version, `blocked_from`, the latest `Escalation:` note, the latest `Kickback:` and every `Answer:`. If it is `done` or `cancelled`, stop and say so; if it has a live claim, stop: `rule: stopped: <id> held`, or for a hold `rule: stopped: can't park claimed`.
2. If the decision holds the ticket (`hold, set by <who>: <why>; lifts when <fact>`), do only this step. It must be `todo`, `backlog`, or `in_progress`; else stop: `rule: stopped: can't park <status>`. Move an `in_progress` one to `todo`, then a `todo` one with `maestro-ticket transition <id> --to backlog --parked hold`, or re-park a `backlog` one with `maestro-ticket update <id> --parked hold`; then comment `Hold: set by <who>, <today's date>: <why>; lifts when <fact>`.
3. If it is `in_progress` (a disputed kickback), move it first, so no work run takes it: to `blocked` if the decision splits, rewrites or drops the work, so a run that stops later still shows as answered; else, if it drops a finding of the latest `Kickback:`, to `in_review`.
4. Post on stdin:
   1. If the decision drops an item of a `Kickback: QA fail at <H>`, from a block or a dispute, and no `Retest:` came after that `Kickback:`, first `Retest: planner at <H>: <why>`. Only a new QA run clears the item.
   2. Then `Answer: <decision>`. One that changes an earlier `Answer:` names it and says why.
5. If the decision splits or rewrites the work, follow [plan.md](plan.md) for this id, and reply as it says under a first line `rule: <done|stopped>: <id> <split into <ids> | the reason>`. The store can't edit a ticket, so a rewrite is a split into one.
6. If it drops the work, run `maestro-ticket transition <id> --to cancelled --json`. On `has_dependents` as its `error.code`, move each ticket the refusal names off this one (`maestro-ticket update <dependent> --remove-deps <id>`), or cancel it the same way if the decision drops it too, then cancel again. On `claimed`, post `Escalation: drop; cancel this once <dependent> (held by <holder>) is off it`, then a line `Drop too: <ids>` naming each dependent the decision drops, if any, and a last line `Return-role: <the ticket's role>`; move the ticket to `blocked` if it isn't there, so no engineer builds it, and stop: `rule: stopped: <dependent> held`. The planner sends `finish` for it later.
7. Else, if it is `blocked`, run `maestro-ticket transition <id> --from blocked --to <blocked_from> --role <role>`, where `<role>` is the note's `Return-role:` only if it is exactly `engineer`, `qa-lead`, `reviewer`, `planner` or `chief-of-staff`; else leave `--role` off. If the decision drops an item of the latest `Kickback:`, a `Kickback: QA fail`, and the ticket is now `in_progress`, move it to `in_review`: QA tests it again.

## Reply

First `rule: <done|stopped>: <id> <new status | the reason>`, then each comment posted and each dependent moved.
