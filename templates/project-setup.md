# Project setup

Installed as `.maestro/project/project-setup.md`. Fill it in once per project; harness sessions and the runner read it, so the personas stay the same across projects while the tools change. Keep it short: sessions pay tokens to read it.

## Project
- **Name:**
- **Summary:** one or two sentences on what this project is.

## Ticket mirror

Agents coordinate only in `maestro-ticket`. A ticket tool for humans is a mirror: a synced view, never where agents work.

- **Mirror:** none (default) / Backlog.md / Jira / GitHub Issues / Linear
- **Location:** for Backlog.md, `.maestro/backlog/` (decision 0142); otherwise the project key or URL
- **Status mapping:** only for tools whose statuses differ from the team statuses in `bot-profiles/team-rules.md`

| Team status      | Mirror's status |
|------------------|-----------------|
| Backlog          |                 |
| To do            |                 |
| In progress      |                 |
| Blocked          |                 |
| In review        |                 |
| In QA            |                 |
| Ready to release |                 |
| Done             |                 |
| Cancelled        |                 |

Backlog.md setup: `backlog init "<project name>" --defaults --integration-mode none --cwd .maestro`, so it writes no agent instruction files into the repo and keeps its folder in the manifest. Humans can browse it with `backlog board --cwd .maestro`.

## Definition of done

The quality bar every ticket must meet in this project, on top of each ticket's own acceptance criteria. Drafted by the onboarding playbook from the repo's CI config, pull request template, CONTRIBUTING guide and branch protection rules, then confirmed by the user.

- **Maestro's floor for autonomous shipping:** verification evidence from the real app, a passing review, and a passing merge queue. This project can raise the bar but not lower it below this. Work held to a lower bar needs a human to approve it.
- **Who can change this section:** bots can propose raising the bar. Only the user can lower it.

Each item says how it's checked: a **command**, **evidence** to attach, or a reviewer's **judgment**.

Feature, bug, chore and docs are ship tickets; investigation is a scout ticket.

| Item | How it's checked | Feature | Bug | Chore | Docs | Investigation |
|---|---|---|---|---|---|---|
| Tests pass | Command: | ✓ | ✓ | ✓ | | |
| New behaviour has tests | Judgment | ✓ | ✓ | | | |
| Coverage threshold | Command: | | | | | |
| Bug reproduced before the fix | Evidence: failing case | | ✓ | | | |
| Verified on the real app | Evidence: | ✓ | ✓ | | | |
| Docs updated | Judgment | ✓ | | | ✓ | |
| Changelog entry | Command: | | | | | |
| Answer backed by evidence | Judgment | | | | | ✓ |
| Extra items for this project | | | | | | |

Higher-risk changes, like migrations, auth or payments, can add items here, such as a rollback plan or a dry run.

## Board settings
Leave blank to use the team's defaults.
- **Work in progress limit:** default is one ticket In progress per agent
- **Default budgets:** for example Junior tickets 50k tokens, Senior tickets 150k tokens
- **Stale threshold:** how long a ticket can sit in one status before the Planner chases it, for example 1 day
- **Archive Done tickets after:** for example 1 week

## Code
- **Repository:**
- **Main branch:**
- **Branch naming:** Maestro always uses `maestro/<ticket-id>-<slug>`, and never touches other branches

## Automated checks
The runner runs these by name (`check lint`, `check test`). Engineers' and QA's sessions may run them too.
- **Lint:** command
- **Type check:** command
- **Tests:** command
- **Build:** command

## Release
- **Environments:** for example staging, production
- **How to deploy:** command or instructions
- **Smoke check:** a read-only command per environment that shows the release is healthy
- **Rollback:** command per environment that undoes the last deploy
- **Needs user approval:** for example production only

## Project-specific rules
Anything stricter than the team rules for this project only, for example extra approval steps, lower retry limits, or a model change for one role. Rules here can raise Maestro's bar, never lower it.
