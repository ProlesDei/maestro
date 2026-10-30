# work

Takes one ticket to an open PR at `in_review`, with a test for each acceptance criterion. Run it from the target repo:

```sh
maestro-play run work                 # the next ticket: sent back first, then todo
maestro-play run work --ticket T-12   # this ticket
```

It needs `maestro-play` and `maestro-ticket` on `PATH`, `$MAESTRO_PLAYBOOKS` set to this repo's `playbooks/`, and `.maestro/project/project-setup.md` in the target repo. `--bot` defaults to `$MAESTRO_BOT`, else `user`. A bot passes its own id.

It prints one JSON object: `id` (the run id), `state`, `playbook`, `ticket`, `step` and `reason`. The exit code says how it ended: 0 done, 1 failed, 3 blocked, 4 lease lost, 7 stopped. A run can take a while, so wait for the process to exit.

## Steps

1. Claim the ticket. With no `--ticket`, take the first unclaimed `in_progress` ticket whose latest comment starts `Kickback:` (review's NO-GO) or `Answer:` (unblocked), else the first `todo`, by priority. A ticket another run holds, or one waiting on deps, is skipped.
2. `start` moves `todo` to `in_progress`; a sent-back ticket skips it.
3. `worktree` makes or reuses `.maestro/worktrees/<id>` on branch `maestro/<id>`.
4. `plan` ([plan.md](plan.md), frontier model) writes the plan to the scratch file. A Kickback skips it and goes straight to build with the feedback.
5. `build` ([build.md](build.md), frontier model) writes the tests and the change, and commits. The ticket's `Type:` line picks the mode: `bug`, `refactor` or `perf`, else `feature`. `plan.md` and `build.md` branch on it.
6. `checks` runs each command under `## Checks` in `project-setup.md`; a failure goes back to `build`.
7. `verify` ([verify.md](verify.md), mid model) proves each criterion on the running code; a `failed` reply goes back to `build`.
8. `head` and `pr` read the sha and open the pull request.
9. `done` posts the `Handoff:` note (PR url, branch, sha, checks, criteria, `author-family`) and moves the ticket to `in_review`.

Any `blocked` reply, or a step that fails with no `on:` route, escalates: `_escalate.md` runs in the same session, the engine posts an `Escalation:` note and moves the ticket to `blocked`.

## After a run

| `state` | Next |
|---|---|
| `done` | The ticket is `in_review` with a `Handoff:` note. Run `/maestro-qa-lead <id>`, then `/maestro-review <id>`. |
| `blocked` | A blocked run is over; `resume` won't restart it. Answer the `Escalation:` note as [Unblock](../../skills/maestro-chief-of-staff/SKILL.md#unblock) shows, then run it again. |
| `failed`, `lost`, `stopped`, or `refused` in `reason` | Read `reason`. Never work around a refused step or run one by hand. |
| no JSON | `maestro-play resume <run id>` once, with the id the run printed. |
