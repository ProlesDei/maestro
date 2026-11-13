# Project setup

`scripts/install.sh apply` copies this to `.maestro/project/project-setup.md` once; [INSTALL.md](https://github.com/ProlesDei/maestro/blob/main/INSTALL.md) has your agent fill it in with you. `install.sh check` reads two sections, so keep their shape:

- `## Main branch`: one line, the branch PRs merge into.
- `## Checks`: only bullets of a name, a colon and the command in backticks. Engineers run them in order in the ticket's worktree; a non-zero exit fails the check. Checks must leave `git status` clean: gitignore what they write.

Sessions read the rest as plain text.

## Project

- Name: Maestro
- Summary: Infrastructure for a fully agentic workflow: a ticket tool, skills and personas that a team installs into its own repo. This repo builds Maestro and runs on it.

## Main branch

main

## Checks

- ticket-fmt: `cd tools/maestro-ticket && ! gofmt -l . | grep .`
- ticket-vet: `cd tools/maestro-ticket && go vet ./...`
- ticket-test: `cd tools/maestro-ticket && go test -race ./...`
- install: `bash scripts/install_test.sh`
- review-prep: `bash scripts/review_prep_test.sh`

## Run

Maestro has no server; the app is the ticket CLI and the installer. To try a change for real, build from the worktree into a scratch folder and install into a scratch repo:

```sh
s=$(mktemp -d) && GOBIN=$s/bin
(cd tools/maestro-ticket && GOBIN=$GOBIN go install ./cmd/maestro-ticket)
git init -q $s/repo && git -C $s/repo commit -q --allow-empty -m init
mkdir -p $s/repo/.maestro/project
printf 'maestro-sha: %s\nharnesses: claude codex\nskills-home: hidden\n' $(git rev-parse HEAD) >$s/repo/.maestro/project/install.yaml
PATH=$GOBIN:$PATH bash scripts/install.sh apply $s/repo
cd $s/repo && PATH=$GOBIN:$PATH maestro-ticket create --title "try it" --status todo
```

`install.sh apply` refuses uncommitted Maestro: commit first, since it pins `HEAD`. To try INSTALL.md itself, give a fresh agent session in the scratch repo only "follow INSTALL.md in <worktree>". Never test against this repo's own ticket store; it holds the real backlog.

## Skills

- Our own working skills drop the `maestro-` prefix and live once in `.maestro/skills/<name>/`. `.claude/skills/` and `.agents/skills/` hold only symlinks to them, so every harness reads one copy: from each harness folder, `ln -s ../../.maestro/skills/<name>`. Never put a real folder in a harness folder.
- To rename or retire one, remove its folder and both symlinks.
- `skills/` holds the skills Maestro ships; AGENTS.md says how to change them.

## Conventions

Follow AGENTS.md. `shellcheck scripts/*.sh` isn't a check because shellcheck isn't installed on every machine; run it by hand after changing `scripts/`.
