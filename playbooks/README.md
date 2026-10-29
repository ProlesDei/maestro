# Playbooks

Step-by-step plans for each role's jobs. Playbooks are part of the bot profile: the bot runtime loads them, and they are never installed into the target repo.

## How they run

1. Intake (`at-check-intake`) lists the waiting work in order.
2. At "Pick the playbook", the bot picks the playbook whose `trigger` matches the top candidate, then claims it (`at-claim`) from the playbook's `claims` status.
3. The steps run in one harness session for this ticket. Same session across the playbook's steps, fresh session per ticket.
4. For each step the bot picks the model tier (`model-selection`), writes a prompt under the prompt contract that names the step's harness skill, and checks the structured reply: goal met? evidence included?
5. Result:
   - Step done, more to do: next step.
   - Not right, within budget: sharper prompt.
   - Stuck or over budget: set Blocked, then wrap up.
   - Playbook done: wrap up (`at-wrap-up`), then clean up.

## Layout

`playbooks/<role>/<job>.md`. Role folder names match `bot-profiles/<name>/`. Copy `_template/playbook.md` to start.

## Format

- **Frontmatter**
  - `name`: the file name without `.md`.
  - `description`: one sentence on what the playbook does.
  - `role`: the role folder name.
  - `trigger`: the intake category that starts it: `request`, `sent-back`, `new-work` or `scheduled`.
  - `claims`: the at-ticket status it claims from, or `none`.
- **Body**: Goal, When to use, Steps, Stop and block when, Wrap-up.
- **Each step**: Goal, Skill (harness skill name, or `none`), Tier, Done when, Evidence.

## Rules

- Files and folders starting with `_` are not playbooks. Readers skip them.
- Steps never paste file contents into prompts. They point to files, tickets and branches.
- Every step's reply follows the prompt contract.
- Every step names its harness skill, or says `none`.
- Budgets and retry limits come from `.agent-team/environment.md`, never from the playbook.
- A step marked **Gate** needs approval at the propose-only trust level: post the plan as an `Escalation:` comment and set Blocked instead of acting.
- Long steps run `at-ticket heartbeat`. A lost lease (exit `4`) means stop and wrap up without a status change.

## Shared conventions

- Comment prefixes, so intake can route without reading prose:
  - `Handoff:` the wrap-up note on every ticket session.
  - `Escalation:` needs a decision above the author's authority. On a `blocked` ticket it goes to the Chief of Staff; on a `todo` ticket it goes to the ticket's role.
  - `Approved:` / `Declined:` the Chief of Staff's answer to an escalation.
  - When a playbook says to block with an `Escalation:` note, that note is the wrap-up handoff note, so it stays the latest comment.
- Engineers work on branch `at/<ticket-id>-<slug>` in the worktree `$(git rev-parse --git-common-dir)/agent-team/worktrees/<ticket-id>/`.
- Project checks (build, lint, test) come from `.agent-team/project-setup.md`. Submit means `in_progress` → `in_review` after they pass.
- Closing a ticket with no code to release (a tracking ticket, a finished infrastructure request, an accepted suggestion): while you hold the lease, move it to `in_progress` if it isn't there already (from `todo` or `blocked`), then `in_progress` → `done`, each with `--expect-version` and a `--note`. Never use this for code: code goes through review, QA and release. at-ticket allows the move; the role's playbook and permissions are what keep code on the full path.

## Index

| Playbook | Role | Trigger | Claims |
|---|---|---|---|
| `chief-of-staff/triage-request` | chief-of-staff | request | none |
| `chief-of-staff/handle-escalation` | chief-of-staff | request | blocked |
| `chief-of-staff/status-brief` | chief-of-staff | scheduled | none |
| `planner/plan-project` | planner | request | todo |
| `planner/groom-board` | planner | scheduled | none |
| `senior-software-engineer/build-ticket` | senior-software-engineer | new-work | todo |
| `senior-software-engineer/fix-sent-back` | senior-software-engineer | sent-back | in_progress |
| `senior-software-engineer/take-escalation` | senior-software-engineer | request | todo |
| `junior-software-engineer/build-ticket` | junior-software-engineer | new-work | todo |
| `junior-software-engineer/fix-sent-back` | junior-software-engineer | sent-back | in_progress |
| `code-reviewer/review-change` | code-reviewer | new-work | in_review |
| `qa-lead/verify-ticket` | qa-lead | new-work | in_qa |
| `operations-manager/release` | operations-manager | new-work | ready_to_release |
| `operations-manager/infrastructure-request` | operations-manager | request | todo |
| `operations-manager/improvement-loop` | operations-manager | scheduled | none |
