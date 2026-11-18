# Project setup

`scripts/install.sh apply` copies this to `.maestro/project/project-setup.md` once; [INSTALL.md](https://github.com/ProlesDei/maestro/blob/main/INSTALL.md) has your agent fill it in with you. `install.sh check` reads three sections, so keep their shape:

- `## Main branch`: one line, the branch PRs merge into.
- `## Checks`: only bullets of a name, a colon and the command in backticks. Engineers, review and QA run them in order in a worktree; a non-zero exit fails the check. Checks must leave `git status` clean: gitignore what they write.
- `## Proof`: one word, `qa` or `engineer`: who proves each criterion on the running app. `qa`, the default when the section is missing: engineers prove each criterion with one real run, and QA proves the rest before it merges, so engineers spend no build run on QA's work. `engineer`: engineers also prove each criterion in full, with evidence, before review.

Sessions read the rest as plain text, but for `## Cache`, whose bullets `maestro-ticket cache` reads.

## Project

- Name: Maestro
- Summary: Infrastructure for a fully agentic workflow: a ticket tool, skills and personas that a team installs into its own repo. This repo builds Maestro and runs on it.

## Main branch

main

## Checks

- ticket-fmt: `cd tools/maestro-ticket && ! gofmt -l . | grep .`
- ticket-vet: `cd tools/maestro-ticket && go vet ./...`
- ticket-test: `cd tools/maestro-ticket && maestro-ticket slot --max 4 -- go test -race ./...`
- install: `maestro-ticket slot --max 4 -- bash scripts/install_test.sh`
- review-prep: `maestro-ticket slot --max 4 -- bash scripts/review_prep_test.sh`
- qa-prep: `maestro-ticket slot --max 4 -- bash scripts/qa_prep_test.sh`
- screens: `maestro-ticket slot --max 4 -- bash scripts/screens_test.sh`
- design-pr: `maestro-ticket slot --max 4 -- bash scripts/design_pr_test.sh`

## Run

Maestro has no server; the app is the ticket CLI and the installer. To try a change for real, build from the worktree into a scratch folder and install into a scratch repo:

```sh
s=$(mktemp -d) && GOBIN=$s/bin
(cd tools/maestro-ticket && GOBIN=$GOBIN go install ./cmd/maestro-ticket)
git init -q $s/repo && git -C $s/repo commit -q --allow-empty -m init
mkdir -p $s/repo/.maestro/project
printf 'maestro-sha: %s\nharnesses: claude codex\nskills-home: hidden\ncode-graph: none\nbrowser: none\n' $(git rev-parse HEAD) >$s/repo/.maestro/project/install.yaml
PATH=$GOBIN:$PATH bash scripts/install.sh apply $s/repo
cd $s/repo && PATH=$GOBIN:$PATH maestro-ticket create --title "try it" --status todo
```

`install.sh apply` refuses uncommitted Maestro: commit first, since it pins `HEAD`. To try INSTALL.md itself, give a fresh agent session in the scratch repo only "follow INSTALL.md in <worktree>". Never test against this repo's own ticket store; it holds the real backlog.

## QA

- Build and install as `## Run` says, from the PR's head, into a fresh scratch repo with its own store; never this repo's store.
- Flows that must never break:
  - `install.sh apply` into a fresh repo, `install.sh check` on it, a second `apply` that changes nothing, then `uninstall`.
  - A ticket's path through the CLI: `create`, `look`, `pickup`, a note, a `transition` to `done`.
  - A changed skill: its trial prompts, run in the scratch repo as AGENTS.md says.
- No screens, so no screen sizes and no browser.

## Proof

qa

## Design

none

## Cache

none

## Outside steps

none

## Review

- `scripts/install.sh`: it writes into a team's repo and must never write over a file it didn't place.
- `tools/maestro-ticket/sqlitestore/`: the one store hundreds of sessions write at once.
- `skills/maestro-code-review/scripts/review-prep.sh`: the gates every review trusts.

## Skills

- Our own working skills drop the `maestro-` prefix and live once in `.maestro/skills/<name>/`. `.claude/skills/` and `.agents/skills/` hold only symlinks to them, so every harness reads one copy: from each harness folder, `ln -s ../../.maestro/skills/<name>`. Never put a real folder in a harness folder.
- To rename or retire one, remove its folder and both symlinks.
- `skills/` holds the skills Maestro ships; AGENTS.md says how to change them. Each harness folder also holds a `maestro-<name>` symlink to each, `ln -s ../../skills/maestro-<name>`, so sessions here run the shipped skills from the checkout they start in. Add or remove that pair of symlinks when you add, rename or retire a shipped skill.

## Conventions

Follow AGENTS.md. `shellcheck scripts/*.sh` isn't a check because shellcheck isn't installed on every machine; run it by hand after changing `scripts/`.
