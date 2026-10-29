# Project setup

Fill this in once per project. Every agent reads it at the start of a session, so the personas can stay the same across projects while the tools change. Keep it short: agents pay tokens to read it every time.

## Project
- **Name:**
- **Summary:** one or two sentences on what this project is.

## Ticket system

If the project has its own ticket system connected, fill in its details below. If it doesn't, leave this section as is and the team uses its built-in default, **Backlog.md**.

- **Tool:** Backlog.md (default) / Jira / other
- **Location:** for Backlog.md, the `backlog/` folder; otherwise the project key, file path or URL
- **Access method:** CLI, API, MCP or direct file edits (prefer CLI where possible, to save tokens)
- **How to read a ticket:** command or instructions
- **How to create or update a ticket:** command or instructions

### Default: Backlog.md

Used whenever the project doesn't connect its own ticket system. It's free, open source, CLI-first and stores every ticket as a Markdown file in git.

- **Install:** `npm i -g backlog.md`
- **Set up:** `backlog init "<project name>"`, and choose **Skip** for the AI setup. Both the MCP and CLI options write agent instruction files (such as CLAUDE.md or AGENTS.md) into the repo, which oversteps on the user's setup. Our at-* skills teach the harness the CLI instead.
- **Footprint:** add the backlog folder Backlog.md creates to our manifest, so uninstall can offer to remove it.
- **Statuses:** configure Backlog.md's statuses to match the team statuses in `AGENTS.md` exactly, so no mapping is needed.
- **Agents:** always use `--plain` output when reading, to save tokens.
- **Common commands:**
  - Read a ticket: `backlog task <id> --plain`
  - List tickets by status: `backlog task list -s "<status>" --plain`
  - Create a ticket: `backlog task create "<title>"`
  - Add a description and acceptance criteria: `backlog task edit <id> -d "<description>" --ac "<criterion>,<criterion>"`
  - Add an implementation plan: `backlog task edit <id> --plan "<plan>"`
  - Full reference: the project's CLI-INSTRUCTIONS.md on GitHub (MrLesk/Backlog.md)
- **For the user:** `backlog board` for a terminal board, or `backlog browser` for a web UI.

### Status mapping

Only needed when the project uses a tool other than Backlog.md.

| Team status      | This tool's status |
|------------------|--------------------|
| Backlog          |                    |
| To do            |                    |
| In progress      |                    |
| Blocked          |                    |
| In review        |                    |
| In QA            |                    |
| Ready to release |                    |
| Done             |                    |

## Definition of done

The quality bar every ticket must meet in this project, on top of each ticket's own acceptance criteria. Drafted by the onboarding playbook from the repo's CI config, pull request template, CONTRIBUTING guide and branch protection rules, then confirmed by the user.

- **Maestro's floor for autonomous shipping:** verification evidence from the real app, a passing review, and a passing merge queue. This project can raise the bar but not lower it below this. Work held to a lower bar needs a human to approve it.
- **Who can change this section:** bots can propose raising the bar. Only the user can lower it.

Each item says how it's checked: a **command**, **evidence** to attach, or a reviewer's **judgment**.

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
- **Grouping:** how work is grouped in this tool, for example epics, milestones or labels

## Code
- **Repository:**
- **Main branch:**
- **Branch naming:** for example `ticket-123-short-name`

## Automated checks
- **Lint:** command
- **Type check:** command
- **Tests:** command
- **Build:** command

## Release
- **Environments:** for example staging, production
- **How to deploy:** command or instructions
- **Needs user approval:** for example production only

## Logs
- **Location:** where agents write their one-line log entries (format in `AGENTS.md`)

## Project-specific rules
Anything that overrides `AGENTS.md` or the personas for this project only, for example stricter approval rules, different retry limits, or a model change for one role.
