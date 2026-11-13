# Project setup

`scripts/install.sh apply` copies this to `.maestro/project/project-setup.md` once; [INSTALL.md](https://github.com/ProlesDei/maestro/blob/main/INSTALL.md) has your agent fill it in with you. `install.sh check` reads two sections, so keep their shape:

- `## Main branch`: one line, the branch PRs merge into.
- `## Checks`: only bullets of a name, a colon and the command in backticks. Engineers, review and QA run them in order in a worktree; a non-zero exit fails the check. Checks must leave `git status` clean: gitignore what they write.

Sessions read the rest as plain text.

## Project

- Name: <name>
- Summary: <one or two sentences on what it does and who uses it>

## Main branch

main

## Checks

<each check a bullet of a name, a colon and the command in backticks, such as - test: `make test`. Wrap a check that takes minutes, strains the machine or writes temp files, such as a test run, in `maestro-ticket slot`, such as - test: `maestro-ticket slot --max 4 -- make test`, so many sessions at once don't make it slow or flaky, and each run's temp files go when it ends>

## Run

<how to start the app locally, and the URL or command to check it works; `/maestro-qa-lead` and `maestro-prove-complete` read this>

## QA

<what every QA run covers, read by `/maestro-qa-lead`: how to build and start the app the way production does; the user flows that must never break, one per line; how to load data like production's; test accounts, by environment variable name; the screen sizes to check; a browser tool only if QA uses one other than Playwright CLI>

## Review

<paths every review treats as high risk, such as `src/auth/` or `migrations/`; `/maestro-code-review` reads this>

## Conventions

<commit style, where tests live, files never to touch, and the shared helpers new code must use instead of its own, such as one for safe file paths or for errors that never print a secret; engineers and review hold every change to them>
