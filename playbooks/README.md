# Playbooks

A playbook is a YAML list of typed steps that `maestro-play` runs in order. Maestro ships one, `engineer/work.yaml`; `/maestro-work` runs it as `maestro-play run engineer/work --ticket <id>`. Playbooks are never installed into the target repo. Engine detail (commands, built-in requests, template data and functions, exit codes) is in [tools/maestro-play/README.md](../tools/maestro-play/README.md).

## What work.yaml does

1. Claims the ticket. It takes a `todo` ticket, or an `in_progress` one whose latest comment starts `Kickback:` (review's NO-GO).
2. `start` moves `todo` to `in_progress`; a Kickback skips it.
3. `worktree` makes or reuses `.maestro/worktrees/<id>` on branch `maestro/<id>`.
4. `plan` writes the plan to the scratch file. A Kickback skips it and goes straight to build with the feedback.
5. `build` writes the tests and the change, and commits. The ticket's `Type:` line picks the mode: `bug`, `refactor` or `perf`, else `feature`. `plan.md` and `build.md` branch on it.
6. `checks` runs `check all`; a failure goes back to `build`.
7. `verify` proves each criterion on the running code; a `failed` reply goes back to `build`.
8. `head` and `pr` read the sha and open the pull request.
9. `done` posts the `Handoff:` note (PR url, branch, sha, checks, criteria, `author-family`) and moves the ticket to `in_review`.

Any `blocked` reply, or a step that fails with no `on:` route, escalates: `_escalate.md` runs in the same session and the engine posts an `Escalation:` note.

## Step keys work.yaml uses

| Key | Does |
|---|---|
| `ticket: <maestro-ticket args>` | a ticket call; the engine adds `--json`, the lease and the idempotency key |
| `run: <request>` | a built-in request: `worktree add`, `check all`, `head`, `open pr` |
| `prompt: <template>` | renders `prompts/_contract.md` then `prompts/<template>.md` and sends it to the harness session |
| `model:` | `mid` (default) or `frontier` |
| `with: {k: <template>}` | rendered first, read in the prompt as `.step.with.k` |
| `out: [fields]` | the reply fields a `done` or `failed` reply must carry |
| `retry: {max, tier_up}` | asks again when a reply misses a field |
| `when:` and `reason:` | a template that renders `true` or `false`; false skips the step and logs the reason |
| `on: {outcome: step}` | where an outcome goes; by default `ok` and `done` go on, anything else blocks |
| `done: {to, note}` | the wrap-up: post the note, move the ticket, release the lease |

## Add a playbook

1. Copy `engineer/work.yaml` to `playbooks/<role>/<job>.yaml`. `name` is the file name and `role` the folder, one of `chief-of-staff`, `engineer`, `reviewer`.
2. Make any step with no judgment a `run` or `ticket` step, never a prompt.
3. Give each prompt template a one-line goal, about six numbered steps and a closing `Reply:` line, in 25 lines or fewer. Put fixed text first and the `## This step` variables last, so the harness can cache the prefix.
4. Prompts point to files, tickets and branches; they never paste them. They never name a skill or a model id.
5. Run `go test ./...` in `tools/maestro-play/`. It lints and renders every playbook here.
