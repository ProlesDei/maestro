# Project setup

`scripts/install.sh` copies this to `.maestro/project/project-setup.md` once; fill it in there. `maestro-play` parses two sections, so keep their shape:

- `## Main branch`: one line, the branch PRs merge into.
- `## Checks`: only bullets of a name, a colon and the command in backticks. `check all` runs them in order in the ticket's worktree; a non-zero exit, or a run past the check timeout (15m by default), fails the check. Checks must leave `git status` clean: gitignore what they write, or the run stops at `head`.

Sessions read the rest as plain text.

## Project

- Name: Maestro
- Summary: Infrastructure for a fully agentic workflow: a ticket tool, a playbook engine, skills and personas that a team installs into its own repo. This repo builds Maestro and runs on it.

## Main branch

main

## Checks

- play-fmt: `cd tools/maestro-play && ! gofmt -l . | grep .`
- play-vet: `cd tools/maestro-play && go vet ./...`
- play-test: `cd tools/maestro-play && go test -race ./...`
- ticket-fmt: `cd tools/maestro-ticket && ! gofmt -l . | grep .`
- ticket-vet: `cd tools/maestro-ticket && go vet ./...`
- ticket-test: `cd tools/maestro-ticket && go test -race ./...`
- install: `bash scripts/install_test.sh`

## Run

Maestro has no server; the app is the two CLIs and the installer. To try a change for real, build from the worktree into a scratch folder and install into a scratch repo:

```sh
s=$(mktemp -d) && GOBIN=$s/bin
(cd tools/maestro-ticket && GOBIN=$GOBIN go install ./cmd/maestro-ticket)
(cd tools/maestro-play && GOBIN=$GOBIN go install ./cmd/maestro-play)
git init -q $s/repo && git -C $s/repo commit -q --allow-empty -m init
PATH=$GOBIN:$PATH bash scripts/install.sh $s/repo
cd $s/repo && PATH=$GOBIN:$PATH maestro-ticket create --title "try it" --status todo
PATH=$GOBIN:$PATH maestro-play render work --step plan --ticket T-1 --playbooks <worktree>/playbooks
```

`maestro-play lint <worktree>/playbooks` checks the playbooks alone. Never test against this repo's own ticket store; it holds the real backlog.

## Tickets

maestro-ticket

## Skills

- Our own working skills drop the `maestro-` prefix and live once in `.maestro/skills/<name>/`. `.claude/skills/` and `.agents/skills/` hold only symlinks to them, so every harness reads one copy: from each harness folder, `ln -s ../../.maestro/skills/<name>`. Never put a real folder in a harness folder.
- To rename or retire one, remove its folder and both symlinks.
- `skills/` holds the skills Maestro ships; AGENTS.md says how to change them.

## Conventions

Follow AGENTS.md. `shellcheck scripts/*.sh` isn't a check because shellcheck isn't installed on every machine; run it by hand after changing `scripts/`.
