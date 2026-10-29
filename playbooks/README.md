# Playbooks

Step-by-step plans for each role's jobs. Playbooks are part of the bot profile: the bot runtime loads them, and they are never installed into the target repo.

## How they run

1. Intake (`maestro-check-intake`) lists the waiting work in order.
2. At "Pick the playbook", the bot picks the playbook whose `trigger`, `claims` and `types` match the top candidate, then claims it (`maestro-claim`). A ticket body line `Playbook: <name>` picks a playbook when several match (for example `refactor` or `performance`). A sent-back ticket (`in_progress`) that no `sent-back` playbook takes for its role and type resumes the playbook that claims it from `todo`, from the step after Start; Start's `worktree add`, if it has one, still runs to reuse the worktree.
3. The steps run in order. Harness steps share one harness session for this ticket. Fresh session per ticket.
4. For each step the bot does what its **Role** line says: prompts the harness under the prompt contract, naming the step's skill, or asks the runner for a named command, or decides itself. It checks the reply: goal met? evidence included?
5. Result:
   - Step done, more to do: next step.
   - Not right, within budget: sharper prompt.
   - Stuck or over budget: escalate (see Escalation), then wrap up.
   - Playbook done: wrap up, then clean up, as `prompt-contract` says: one batched prompt naming `maestro-propose-memory` (only when there's something durable), `maestro-handoff-note` or `maestro-escalate`, and `maestro-log-entry`.

## Layout

`playbooks/<role>/<job>.md`. Role folder names match `bot-profiles/<name>/`. Copy `_template/playbook.md` to start. Each playbook has a golden task at `evals/<role>/<job>.md` (see [evals](../evals/README.md)).

## Format

- **Frontmatter**
  - `name`: the file name without `.md`.
  - `description`: one sentence on what the playbook does.
  - `role`: the role folder name.
  - `trigger`: the intake category that starts it: `request`, `sent-back`, `new-work` or `scheduled`.
  - `claims`: the maestro-ticket status it claims from, or `none`.
  - `types`: the ticket types it takes (`feature`, `bug`, `chore`, `docs`, `investigation`), `any`, or `none` when it claims nothing.
- **Body**: Goal, When to use, Steps, Stop and block when, Wrap-up.
- **Each step**, in this order:
  - **Goal:** what this step must achieve.
  - **Role:** who acts. `harness`: a harness session does the work, loading the named skill. `runner`: the runner runs a named command (below), no tokens. `bot`: the bot decides or messages, with no harness prompt.
  - **Skill:** the one skill that holds the procedure, in backticks. Harness steps name a harness skill (`maestro-*`). Runner steps name `runner`. Bot steps name a bot skill, or `none` for a plain decision.
  - **Model:** `Frontier`, `Mid-tier` or `No tokens` (from `model-selection`), or `Bot` for the bot's own model. A qualifier may follow a comma.
  - **Run:** runner steps only: the command and its arguments.
  - **Skip when:** only if the step can be skipped: the condition.
  - **Done when:** the check that proves it.
  - **Evidence:** what the reply must include.

## Rules

- Files and folders starting with `_` are not playbooks. Readers skip them.
- Steps name a skill. They don't paste a procedure: if a step needs more than a few lines of how-to, it belongs in the skill.
- Steps never paste file contents into prompts. They point to files, tickets and branches.
- Every step's reply follows the prompt contract.
- Bots never run commands or touch files. A step with no judgment is a runner step (decision 0011).
- Follow every step in order. A step you don't run stays on the list: record `Skipped: <step> — <reason>` in the scratchpad and the handoff note. Skip a step only by its own `Skip when:` line or a jump the playbook names; anything else is a stop and escalate.
- Budgets, retry limits and thresholds come from preflight, never from the playbook.
- A step marked **Gate** runs only when the trust level allows it (runner `trust`). Below that, post an `Escalation (chief-of-staff):` note with the plan, risk and rollback, set Blocked, and run it only after an `Approved:` that cites the user's approval.

## Runner commands

A runner step's **Run:** line uses only the requests in the Requests table of [`docs/runner.md`](../docs/runner.md), which also says which roles may send each one and from which trust level. `<id>` is the ticket id; `<main branch>` comes from `.maestro/project/project-setup.md`.

## Tickets

Ship and scout lifecycles, statuses, who sets `done`, Tries and the escalation chain are team rules (`bot-profiles/team-rules.md`). Playbooks add:

- **Type:** the store's `--type` is the lifecycle: `scout` for `investigation` tickets and infrastructure requests, which ship no code, `ship` (the default) for the rest. The kind (`feature`, `bug`, `chore`, `docs`, `investigation`), which the frontmatter `types` match, is the body line `Type: <kind>` (decision 0430).
- **Owner:** the role that owns the ticket's workstream, normally its Planner: `create --owner <role>` (decision 0433). The owner closes a scout ticket with `--actor <role>`; only `--actor-role operations-manager` closes a ship ticket. A scout ticket goes `in_progress` → `in_review` → `done`; maestro-ticket refuses `in_progress` → `done`. A ship ticket that turns out to need no code is `cancelled` with a `--note`.
- **Lease:** `maestro-claim` returns the lease token (`claim.token`). Every `transition` and `comment` on a ticket you hold passes `--holder <holder> --token <token>`, in a runner **Run:** line or a skill; without them maestro-ticket refuses the write. Writes to tickets nobody holds (create, grooming fixes, answering an escalation you didn't claim) pass neither. A write that exits `4` with `lease_lost` stops the session (decision 0431).
- **Dependencies and areas:** `create --deps` (or `update --add-deps`) for tickets that must be `done` first, and `--areas` for the code paths a ship ticket touches. `claim --next` skips a ticket until its dependencies are `done` and while a live claim holds an overlapping area; a claim by id exits `4` with `waiting_on_deps` or `area_busy` (decisions 0227, 0228). Never use `blocked` to make a ticket wait for another one: `blocked` means waiting on a decision or something outside the team, with an `Escalation` note saying what.

## Tries

`Tries` is a counter on the ticket. Decision 0502 has the details.

- maestro-ticket adds the try itself when review or QA moves the ticket from `in_review` or `in_qa` back to `in_progress` (decision 0201). Nothing else counts.
- Failing checks loop back to the build step; the step budget is the limit.
- Junior Engineer: a ticket that comes back with `Tries` at 2 goes to the Senior Engineer without another attempt.
- Senior Engineer: the takeover note records `tries-at-takeover` (0 for the senior's own tickets). A ticket that comes back with `Tries` 3 above that is blocked with an `Escalation:` note to the owner.

## Escalation

Use `maestro-escalate` to write the note. The prefix routes it:

- `Escalation:` on a `blocked` ticket goes to the ticket's owner. The owner answers and unblocks it (`planner/answer-escalation`), or forwards it.
- `Escalation (chief-of-staff):` on a `blocked` ticket goes to the Chief of Staff. Use it when the owner forwards, when the owner is the one asking, and for Gate approvals (only the Chief of Staff and the user can approve above the trust level).
- `Escalation:` on a `todo` ticket hands it to the ticket's new role (Junior to Senior).
- `Approved:` / `Declined:` is the answer, on the same ticket.

## Shared conventions

- Comment prefixes, so intake can route without reading prose: `Handoff:` (the wrap-up note on every ticket session), the escalation prefixes above, `Approved:` / `Declined:`, and `Report:` (a scout's findings).
- When a playbook says to block with an `Escalation` note, that note is the wrap-up handoff note, so it stays the latest comment.
- Engineers work on branch `maestro/<id>-<slug>` in the worktree from runner `worktree add`. `<slug>`: up to five lowercase words from the title, joined by hyphens.
- Project checks (build, lint, test) come from `.maestro/project/project-setup.md`. Submit means `in_progress` → `in_review` after they pass and `maestro-verify` has its evidence.
- Engineers end every handoff with `author-family: <family>` so review runs on the other family (`model-selection`).

## Index

| Playbook | Role | Trigger | Claims | Types |
|---|---|---|---|---|
| [`chief-of-staff/triage-request`](chief-of-staff/triage-request.md) | chief-of-staff | request | none | none |
| [`chief-of-staff/handle-escalation`](chief-of-staff/handle-escalation.md) | chief-of-staff | request | blocked | any |
| [`chief-of-staff/status-brief`](chief-of-staff/status-brief.md) | chief-of-staff | scheduled | none | none |
| [`chief-of-staff/digest`](chief-of-staff/digest.md) | chief-of-staff | request | none | none |
| [`chief-of-staff/recap`](chief-of-staff/recap.md) | chief-of-staff | request | none | none |
| [`chief-of-staff/setup-project`](chief-of-staff/setup-project.md) | chief-of-staff | request | none | none |
| [`chief-of-staff/definition-of-done`](chief-of-staff/definition-of-done.md) | chief-of-staff | request | none | none |
| [`planner/plan-project`](planner/plan-project.md) | planner | request | todo | investigation |
| [`planner/answer-escalation`](planner/answer-escalation.md) | planner | request | blocked | any |
| [`planner/accept-report`](planner/accept-report.md) | planner | new-work | in_review | investigation |
| [`planner/groom-board`](planner/groom-board.md) | planner | scheduled | none | none |
| [`senior-software-engineer/build-ticket`](senior-software-engineer/build-ticket.md) | senior-software-engineer | new-work | todo | feature, chore, docs |
| [`senior-software-engineer/bug-fix`](senior-software-engineer/bug-fix.md) | senior-software-engineer | new-work | todo | bug |
| [`senior-software-engineer/refactor`](senior-software-engineer/refactor.md) | senior-software-engineer | new-work | todo | chore |
| [`senior-software-engineer/performance`](senior-software-engineer/performance.md) | senior-software-engineer | new-work | todo | feature, bug, chore |
| [`senior-software-engineer/scout-investigation`](senior-software-engineer/scout-investigation.md) | senior-software-engineer | new-work | todo | investigation |
| [`senior-software-engineer/fix-sent-back`](senior-software-engineer/fix-sent-back.md) | senior-software-engineer | sent-back | in_progress | feature, bug, chore, docs |
| [`senior-software-engineer/take-escalation`](senior-software-engineer/take-escalation.md) | senior-software-engineer | request | todo | feature, bug, chore, docs |
| [`junior-software-engineer/build-ticket`](junior-software-engineer/build-ticket.md) | junior-software-engineer | new-work | todo | feature, bug, chore, docs |
| [`junior-software-engineer/fix-sent-back`](junior-software-engineer/fix-sent-back.md) | junior-software-engineer | sent-back | in_progress | feature, bug, chore, docs |
| [`code-reviewer/review-change`](code-reviewer/review-change.md) | code-reviewer | new-work | in_review | feature, bug, chore, docs |
| [`qa-lead/verify-ticket`](qa-lead/verify-ticket.md) | qa-lead | new-work | in_qa | feature, bug, chore, docs |
| [`qa-lead/verification-skill`](qa-lead/verification-skill.md) | qa-lead | request | todo | chore |
| [`operations-manager/release`](operations-manager/release.md) | operations-manager | new-work | ready_to_release | feature, bug, chore, docs |
| [`operations-manager/infrastructure-request`](operations-manager/infrastructure-request.md) | operations-manager | request | todo | chore |
| [`operations-manager/improvement-loop`](operations-manager/improvement-loop.md) | operations-manager | scheduled | none | none |
| [`operations-manager/memory-curation`](operations-manager/memory-curation.md) | operations-manager | scheduled | none | none |
| [`operations-manager/housekeeping`](operations-manager/housekeeping.md) | operations-manager | scheduled | none | none |
