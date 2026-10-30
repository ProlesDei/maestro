# Maestro

Maestro takes a ticket to a merged pull request with coding agents. It is tooling only: files you install into a repo and commands you run. One playbook, a ticket tool, three skills and three bot personas. Nothing in it runs on its own; a person or a bot starts every job ([decisions](docs/decisions.md)).

## Install

Needs git 2.31+, Go 1.26+, `gh`, and Claude Code or Codex.

```sh
(cd tools/maestro-ticket && go install ./cmd/maestro-ticket)
(cd tools/maestro-play && go install ./cmd/maestro-play)
scripts/install.sh <target-repo>
```

The installer copies each folder in `skills/` into the target's `.claude/skills/` and `.agents/skills/`, writes `.maestro/project/project-setup.md` from [templates/project-setup.md](templates/project-setup.md), creates the ticket store and hides its copies from git. It refuses, writing nothing, if a skill folder already exists. Fill in `project-setup.md`: the main branch and the check commands `/work` runs.

To use your own ticket method, add `--tickets <dir>`: a folder with your own `create-ticket` skill, and a `work` skill if your tickets live elsewhere. The installer copies those in place of Maestro's.

## Use

In the target repo, in Claude Code (or headless: `claude -p "/work T-12" --output-format json`):

- `/create-ticket <what needs doing>` drafts the ticket, creates it in `maestro-ticket` and prints its id.
- `/work <id>` runs `maestro-play run engineer/work --ticket <id>`. It claims the ticket, makes a worktree on `maestro/<id>`, plans, builds, runs the checks, verifies each criterion, opens a PR and moves the ticket to `in_review`. The ticket's type picks the mode: feature, bug, refactor or perf. `/work` relays the run's questions to you and stops when it blocks.
- `/review <pr>` reviews the PR at its head sha, best from a different model family than the author. GO merges the PR and moves the ticket to `done`. NO-GO moves it back to `in_progress` with a `Kickback:` note, and the next `/work <id>` starts again at build with that feedback.

## Your own skills

`skills/` is your slot. Maestro ships `work`, `create-ticket` and `review`. Add `skills/<name>/SKILL.md` and name it in `skills/use.yaml` to have prompt steps use it. See [skills/README.md](skills/README.md).

## Personas

[bot-profiles/](bot-profiles/README.md) holds three personas for bot tools such as Grok Bot, plus the [team rules](bot-profiles/team-rules.md) every bot follows:

- `chief-of-staff`: the one agent the owner talks to. Drives Claude with `/create-ticket`, `/work` and `/review`; never touches code.
- `engineer`: runs `/work` and unblocks a run that stops.
- `reviewer`: runs `/review` on PRs another model family wrote.

## Tools

- [tools/maestro-play](tools/maestro-play/README.md): the playbook engine. Runs, resumes, renders and lints playbooks. The one playbook is [playbooks/engineer/work.yaml](playbooks/engineer/work.yaml); the format is in [playbooks/README.md](playbooks/README.md).
- [tools/maestro-ticket](tools/maestro-ticket/README.md): the ticket store and CLI. SQLite, leases and fencing tokens.
