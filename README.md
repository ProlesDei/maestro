# Maestro

Maestro takes a ticket to a merged pull request with coding agents. It is tooling only: files you install into a repo and commands you run. One playbook, a ticket tool, three skills and three bot personas. Nothing in it runs on its own; a person or a bot starts every job ([decisions](docs/decisions.md)).

## Install

Needs git 2.31+, Go 1.26+, `gh`, and Claude Code or Codex.

```sh
(cd tools/maestro-ticket && go install ./cmd/maestro-ticket)
(cd tools/maestro-play && go install ./cmd/maestro-play)
scripts/install.sh <target-repo>
export MAESTRO_PLAYBOOKS=<maestro>/playbooks
```

The installer copies each folder in `skills/` into the target's `.claude/skills/` and `.agents/skills/`, writes `.maestro/project/project-setup.md` from [templates/project-setup.md](templates/project-setup.md), creates the ticket store and hides its copies from git. It refuses, writing nothing, if a skill folder already exists. Fill in `project-setup.md`: the main branch and the check commands `/maestro-work` runs.

`maestro-play` finds the playbook through `MAESTRO_PLAYBOOKS`, the full path to this repo's `playbooks/` folder. Put the `export` in your shell profile; `/maestro-work` stops without it.

Sessions run with all permissions. To limit them, set `MAESTRO_CLAUDE_ARGS` or `MAESTRO_CODEX_ARGS`.

Runs time out: a session after 30m, a check after 15m, a git or gh call after 5m. Set `MAESTRO_SESSION_TIMEOUT` and `MAESTRO_CHECK_TIMEOUT` to change them; details in [tools/maestro-play](tools/maestro-play/README.md).

To use your own ticket method, add `--tickets <dir>`: a folder with `maestro-create-ticket/` and, if your tickets live elsewhere, `maestro-work/`, each with a `SKILL.md`. The installer copies them in place of Maestro's. Keep those names: the personas call them.

## Use

In the target repo, in Claude Code (or headless: `claude -p "/maestro-work T-12" --output-format json`):

- `/maestro-create-ticket <what needs doing>` drafts the ticket, creates it in `maestro-ticket` and prints its id.
- `/maestro-work <id>` runs `maestro-play run engineer/work --ticket <id>`. It claims the ticket, makes a worktree on `maestro/<id>`, plans, builds, runs the checks, verifies each criterion, opens a PR and moves the ticket to `in_review`. The ticket's type picks the mode: feature, bug, refactor or perf. When a run blocks, the ticket moves to `blocked` with an `Escalation:` note; answer it and run again as [skills/maestro-work](skills/maestro-work/SKILL.md#unblock) shows.
- `/maestro-review <pr>` reviews the PR at its head sha, from another model family than the author when one is installed. GO merges the PR and moves the ticket to `done`. NO-GO moves it back to `in_progress` with a `Kickback:` note, and the next `/maestro-work <id>` starts again at build with that feedback.

## Your own skills

`skills/` is your slot. Maestro ships `maestro-work`, `maestro-create-ticket` and `maestro-review`; the prefix keeps them clear of your own skills. Add `skills/<name>/SKILL.md` and name it in `skills/use.yaml` to have prompt steps use it. See [skills/README.md](skills/README.md).

## Personas

[bot-profiles/](bot-profiles/README.md) holds three personas for bot tools such as Grok Bot, plus the [team rules](bot-profiles/team-rules.md) every bot follows:

- `chief-of-staff`: the one agent the owner talks to. Drives Claude with `/maestro-create-ticket`, `/maestro-work` and `/maestro-review`; never touches code.
- `engineer`: runs `/maestro-work` and unblocks a run that stops.
- `reviewer`: runs `/maestro-review` on PRs another model family wrote.

## Tools

- [tools/maestro-play](tools/maestro-play/README.md): the playbook engine. Runs, resumes, renders and lints playbooks. The one playbook is [playbooks/engineer/work.yaml](playbooks/engineer/work.yaml); the format is in [playbooks/README.md](playbooks/README.md).
- [tools/maestro-ticket](tools/maestro-ticket/README.md): the ticket store and CLI. SQLite, leases and fencing tokens.
