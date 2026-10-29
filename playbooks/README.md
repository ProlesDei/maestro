# Playbooks

Each role's jobs, as workflows a small engine runs step by step. `maestro-play` (`tools/maestro-play/`) runs them, and a bot starts one with the runner's `play` request (`docs/runner.md`). Playbooks stay with the bot runtime and are never installed into the target repo.

## Layout

- `playbooks/<role>/<job>.yaml`: one playbook. Role folder names match `bot-profiles/<name>/`. Copy `_template/playbook.yaml` to start.
- `playbooks/prompts/<template>.md`: the step templates prompt steps render. `_contract.md` comes first in every prompt and `_escalate.md` is the built-in `block`'s prompt.
- `playbooks/rules.md`: the rules every harness session follows. `maestro-play` passes it in the launch flags; it's never installed.
- `evals/<role>/<job>.md`: each playbook's golden task (see [evals](../evals/README.md)).

## How a run goes

1. **Preflight.** The trust level, budgets, limits and model families come back as `.env` and `.limits`. Any failure and the run doesn't start.
2. **Intake.** `--ticket <id>` runs the playbook on that ticket. `--next` takes the first ticket and playbook whose `intake` matches, in category order: `request`, `sent-back`, `new-work`. Scheduled playbooks, and request playbooks with no status, run by name when the bot says they're due (decision 0713).
3. **Claim**, when `claim: true`. The token, the holder and every step's output go into the run state, and the claim's keepalive holds the lease while the run lives.
4. **Steps**, in order unless an `on:` sends the run somewhere else.
5. **`done` or `block`**, both built in (below).
6. **Clean-up**, then `session close`.

After a crash, `maestro-play resume <run-id>` starts again at the first unfinished step, with the same idempotency keys. A run waiting at an `ask` step resumes the same way, with `--choice`.

## Format

```yaml
name: build-ticket                  # the file name
role: senior-software-engineer      # the folder name
description: one line
intake:                             # what --next takes: one entry, or a list (any entry matches)
  category: new-work                # request | sent-back | new-work | scheduled
  status: todo                      # one or a list; request and scheduled may leave it out, and then --next never picks it
  role: any                         # default: the playbook's role
  type: ship                        # ship | scout; default both
  kinds: [feature, chore]           # the body's "Type: <kind>" line (0430)
  owner: planner                    # the ticket's owner role
  title: "Plan:"                    # the title starts with it
  latest: "Handoff:"                # the latest comment starts with it (list --latest-prefix)
  unless_latest: "Escalation:"      # (list --not-latest-prefix)
  body: "Playbook: bug-fix"         # some body line starts with it
  unless_body: "Playbook: "
claim: true                         # the engine claims, keeps the token, the keepalive heartbeats
input: optional                     # required | optional: run --input <text> is .input.text
cursor: {name: groom, save: '{{.steps.events.json.next_seq}}'}   # .cursor, stored per bot at done only
session: ticket                     # ticket or run: one harness session for all prompts (default); step: one each
steps: [...]
done: {to: <tmpl>, note: <tmpl>, role: <tmpl>, actor: <tmpl>, log: <tmpl>}
block: {to: <tmpl>, status: blocked}  # to: owner | chief-of-staff | <role>; status: blocked | todo | keep
```

Unknown keys are errors. Step ids, `out:` fields, `with:` keys and `ask` choices are lower case letters, digits and `_`, so templates can name them.

| Step key | Does | Outcomes for `on:` |
|---|---|---|
| `ticket: <subcommand args>` | `maestro-ticket get`, `list`, `events`, `doctor`, `create`, `update`, `comment` or `transition` (or a template that renders one). The engine adds `--json`; on writes `--idempotency-key` and `--actor <role>`; on `update`, `comment` and `transition` of the run's own ticket `--holder` and `--token` while it holds the lease; on `transition` `--actor-role <the run's role>`. A step can't set those, `--store` or `--body-file` (`body:` is the body), in either dash form, even from a rendered value. It re-reads the ticket after every write. Claim, heartbeat and release are the engine's; a step that lets the run's own lease go (`--release`, or a move to `done` or `cancelled`) ends it, so later writes carry no token and the wrap-up releases nothing. `body:` is rendered and passed as `--body` (`--note` on `transition`) when it isn't empty | `ok`, `fail`, `exit:N` |
| `run: <runner request>` | any request in `docs/runner.md` the role may send, except `preflight`, `session *` and `ticket`, which the engine sends, and `play`, since a run never starts another (decision 0787), and without the runner's own `--json`, `--bot`, `--role`, `--ticket` and `--run`. A list of requests is sent in order and stops at the first that fails; the record is the last one's, and under `each:` that failure also leaves later items unrun | `ok`, `fail`, `exit:N` |
| `prompt: <template>` | `usage check` (exit 2 blocks, with no escalation prompt); `_contract.md`, the skills `use.yaml` names, then `prompts/<template>.md`; `session start` or `resume` with `model:` (`mid` default, `frontier`) and, for `family: not-author`, `--not-family` from the latest `Handoff:` note's last `author-family:` line. `mode:` is `.mode` and `.step.mode`; `with:` (or `vars:`) values are rendered and read as `.step.with.<k>`; `session: fresh` starts a new session for this step | `done`, `partial`, `blocked`, `failed` |
| `gate: <template>` | renders `true` or `false` (lint fails a `gate`, `when` or `until` that renders anything else on empty data) | `ok`, `fail` |
| `wait: {step, until, every: 1m, timeout: 30m}` | re-runs a `run` or `ticket` step until `until` renders `true` | `ok`, `timeout` |
| `ask: {question, choices, timeout: 4h}` | pauses the run; the lease is stretched to `timeout` while it waits | each choice |

Every step also takes:
- `when:` (a template, or `{if, reason}`) and `reason:`: false skips the step and records `Skipped: <id> — <reason>`.
- `on:` maps an outcome to a step id, `next`, `block`, `done` (the `done:` wrap-up now) or `end` (stop without a note or a move; still release, log and clean up). By default `ok`, `done`, a skip and every `ask` choice go to `next`, anything else to `block`. A `fail` entry also covers every `exit:N` not listed. Running off the last step is `done`.
- `each: <dot path>`: run the step once per list item, with `.item` and `.index`; `when:` is checked per item, keys get `:<index>`, and `.steps.<id>.results` holds one record per item. The outcome is `fail` if any item failed, after trying them all. An `ask` step with `each:` pauses once per item.
- `block: {to, status}`: this step's escalation goes there instead of the playbook's.

`.limits.step_visits` (default 5) caps how often one step runs; passing it blocks. Prompt steps take `retry: {max, tier_up}` (default `max` is `.limits.retries`, else 1): a reply that misses a field or evidence is asked again in the same session with `.retry.reason`, one tier up if `tier_up`.

A prompt's reply has `status` and `summary`, optional `evidence`, `retro`, `memory`, `processes` (`[{pid, command}]`) and `questions`, plus the step's `out:` fields. A `done` or `failed` reply needs every `out:` field; a `done` reply needs evidence. A `commit` or `sha` field must be one git sha (7 to 40 hex digits; a `failed` reply may leave it empty), because notes write it as a `sha:` trailer. An `ask` answer's `--text` and a run's `--input` are put on one line, so neither can add a trailer to a note (decision 0792). A `blocked` reply blocks with its summary and questions.

### Templates

Go `text/template` with `missingkey=error`. Every name exists from the start with a zero value, so a template can name a step that hasn't run, and lint fails a misspelled one:

| Name | Holds |
|---|---|
| `.ticket` | `get --json`'s ticket and comments, plus `kind` (the body's `Type:` line), `latest` (the latest comment) and `trailers` (for every `Key: value` comment line's key, the value `latestLine` gives; read with `index`) |
| `.project` | preflight's `project` (`main_branch`, `name`, `setup`, `environments`) |
| `.env` | preflight's other fields (`harness`, `family`, `families`, `trust`, `budgets`); `family` is the last session's model family |
| `.limits` | preflight's `limits` (`step_visits`, `retries`, `step_budget`) |
| `.run` | `id`, `date` (UTC `YYYY-MM-DD`), `playbook`, `role`, `bot`, `holder`, `token`, `session`, `branch`, `worktree`, `scratch`, `scratch_dir`, `evidence_dir`, `skipped`, `stopped_at`, `stop_reason` |
| `.steps.<id>` | every step: `status`, `outcome`, `visits`, `skipped`, `exit_code`, `output`, `output_path`, `summary`, `json` (a ticket step's parsed output, a run step's reply), `result`, `error`, `reply`, `choice`, `text`, `question`, `results`, `value`, `polls`, `session`, `tier`, `family`; run steps also the request's reply fields (`tools/maestro-play/playbook/requests.go`); prompt steps' `reply` has the base fields and `out:` |
| `.step` | `id`, `mode`, `with` (and `vars`, the same map) |
| `.item`, `.index` | the current item of an `each:` step |
| `.input` | `{text}` from `--input` |
| `.cursor` | the stored cursor (`0` the first time) |
| `.retry` | nil, or `{reason, n}` on a retried prompt |
| `.mode` | the prompt step's `mode:` |

Functions: `json`, `join` (a list and a separator, either order), `quote` (shell-quote one argument), `int`, `add`, `latest <ticket> <prefix>` (the newest comment starting with it), `line <key> <text>` (the last `key: ` line's value, since a note writes its trailers after any reply text), `latestLine <ticket> <key>`, `hasPrefix`, `contains`, `trim`, `lower`, `default`, and `index`, which gives nothing instead of failing on a step that hasn't run, a missing key or an index out of range. Integral JSON numbers are ints. A `ticket:` or `run:` line is rendered, then split like a shell would (quotes, no expansion): wrap a value that may have spaces in `quote`, and spread a list with `join`.

A prompt template is checked against each playbook step that uses it, with that step's `with:` keys, so `.steps.checks` in `build.md` needs a `checks` step in every playbook whose steps use `build`, and `.step.with.cursor` needs `with: {cursor: ...}`. For something only some steps have, use `{{with index .steps "checks"}}…{{end}}`, which lint doesn't check. `go test ./...` in `tools/maestro-play/` also renders every prompt step, done note and escalation of these playbooks on a tiny-cli ticket (`tools/maestro-play/testdata/`).

### Built in

- **done:** each reply's `memory` goes to `<common>/memory-inbox/<run-id>-<step>.json`; the `note` is posted as a comment; the ticket moves to `to` (with `--role`, `--actor` if set) with `--release`, or with `to: keep` or no `to` is only released; the cursor is saved; one line goes to `<common>/logs/<date>.jsonl` with the replies' `retro` and the rendered `log`.
- **end:** the memory, the release and the log line; no note, no move, no cursor.
- **block:** `_escalate.md` in the same session (not when the budget ran out), then the `Escalation:` or `Escalation (chief-of-staff):` comment with what happened, what was tried, the options, the recommendation, the question, where, the tries, the skips and `Return-role:`, each reply value on one line so none can pose as a trailer (the ticket's role when the run took it, 0436); the ticket moves to `blocked` for the owner (`--role <owner>`) or the Chief of Staff (`--role chief-of-staff`), to `todo` for any other role (a hand-off), or `keep`: release only (0435). A ticket already `blocked` is forwarded with `keep`. A run with no ticket files the note on a new `blocked` ship ticket for its role (`Type: chore`), as `Escalation (chief-of-staff):`, and names it in `reason`. Then the log line.
- **clean-up:** kills the processes replies listed while their command line still matches, removes the worktree if `git status --porcelain` is empty, and sends `session close`.

No playbook repeats these.

## Rules

- Files and folders starting with `_` are not playbooks. Readers skip them.
- A step with no judgment is a `run`, `ticket` or `gate` step, never a prompt (decision 0011).
- Prompts point to files, tickets and branches. They never paste file contents.
- Templates put fixed text first and the `## This step` variables last, so the stable prefix gets cached.
- Never name a skill or a model ID. Skills come from `skills/use.yaml`; the runner maps tiers to models.
- Budgets, retry limits and thresholds come from preflight (`.limits`), never from the playbook. Passing `.limits.step_visits` blocks the run.
- Only `operations-manager/release`, `operations-manager/infrastructure-request`, `planner/plan-project`, `planner/accept-report` and `chief-of-staff/handle-escalation` move a ticket to `done`; lint fails any other playbook that does (decision 0760).
- A step that needs a trust level sits behind a `gate` on it. Below that level the run blocks to the Chief of Staff with the plan, risk and rollback, and the step runs only after an `Approved:` that cites the user's approval.

## Tickets

Ship and scout lifecycles, statuses, who sets `done`, Tries and the escalation chain are team rules (`bot-profiles/team-rules.md`). Playbooks add:

- **Type:** the store's `--type` is the lifecycle: `scout` for `investigation` tickets and infrastructure requests, which ship no code, `ship` (the default) for the rest. The kind, which `intake.kinds` matches, is the body line `Type: <kind>` (decision 0430).
- **Owner:** the role that owns the ticket's workstream, normally its Planner: `create --owner <role>` (decision 0433). The owner closes a scout ticket with `--actor <role>`; only `--actor-role operations-manager` closes a ship ticket. A scout ticket goes `in_progress` → `in_review` → `done`; maestro-ticket refuses `in_progress` → `done`. A ship ticket that turns out to need no code is `cancelled` with a `--note`.
- **Lease:** on a claimed run the engine passes the holder and token on every write, so a playbook never writes them. A write that exits `4` with `lease_lost` stops the run with no more writes (decision 0431).
- **Dependencies and areas:** `create --deps` (or `update --add-deps`) for tickets that must be `done` first, and `--areas` for the code paths a ship ticket touches. `claim --next` skips a ticket until its dependencies are `done` and while a live claim holds an overlapping area; a claim by id exits `4` with `waiting_on_deps` or `area_busy` (decisions 0227, 0228). Never use `blocked` to make a ticket wait for another one: `blocked` means waiting on a decision or something outside the team, with an `Escalation` note saying what.

## Tries

`Tries` is a counter on the ticket. Decision 0502 has the details.

- maestro-ticket adds the try itself when review or QA moves the ticket from `in_review` or `in_qa` back to `in_progress` (decision 0201). Nothing else counts.
- Failing checks loop back to the build step; `.limits.step_visits` is the limit.
- Junior Engineer: a ticket that comes back with `Tries` at 2 goes to the Senior Engineer without another attempt.
- Senior Engineer: the takeover note records `tries-at-takeover` (0 for the senior's own tickets). A ticket that comes back with `Tries` 3 above that is blocked with an `Escalation:` note to the owner.

## Escalation

The built-in `block` writes the note. The prefix routes it:

- `Escalation:` on a `blocked` ticket goes to the ticket's owner. The owner answers and unblocks it (`planner/answer-escalation`), or forwards it.
- `Escalation (chief-of-staff):` on a `blocked` ticket goes to the Chief of Staff. Use it when the owner forwards, when the owner is the one asking, and for gate approvals (only the Chief of Staff and the user can approve above the trust level).
- `Escalation:` on a `todo` ticket hands it to the ticket's new role (Junior to Senior).
- `Approved:` / `Declined:` is the answer, on the same ticket.

## Shared conventions

- Comment prefixes, so intake can route without reading prose: `Handoff:` (the `done` note), the escalation prefixes above, `Approved:` / `Declined:`, and `Report:` (a scout's findings). The run's last note is always the latest comment.
- Engineers work on branch `maestro/<id>-<slug>` in the worktree from runner `worktree add`. `<slug>`: up to five lowercase words from the title, joined by hyphens.
- Project checks (build, lint, test) come from `.maestro/project/project-setup.md`. Submit means `in_progress` → `in_review` after they pass and the verify step has its evidence.
- An engineer's `done` note ends with `author-family: {{.env.family}}` and `author-role: {{.run.role}}`, so review runs on the other family and a release can send the ticket back to its author (0751).
