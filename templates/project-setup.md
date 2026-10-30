# Project setup

`scripts/install.sh` copies this to `.maestro/project/project-setup.md` once; fill it in there. `maestro-play` parses two sections, so keep their shape:

- `## Main branch`: one line, the branch PRs merge into.
- `## Checks`: only bullets of a name, a colon and the command in backticks. `check all` runs them in order in the ticket's worktree; a non-zero exit, or a run past the check timeout (15m by default), fails the check. Checks must leave `git status` clean: gitignore what they write, or the run stops at `head`.

Sessions read the rest as plain text.

## Project

- Name: <name>
- Summary: <one or two sentences on what it does and who uses it>

## Main branch

main

## Checks

- lint: `make lint`
- test: `make test`

## Run

<how to start the app locally, and the URL or command to check it works; the QA lead uses this>

## Conventions

<branch and commit style, where tests live, files never to touch>
