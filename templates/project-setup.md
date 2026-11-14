# Project setup

`scripts/install.sh apply` copies this to `.maestro/project/project-setup.md` once; [INSTALL.md](https://github.com/ProlesDei/maestro/blob/main/INSTALL.md) has your agent fill it in with you. `install.sh check` reads three sections, so keep their shape:

- `## Main branch`: one line, the branch PRs merge into.
- `## Checks`: only bullets of a name, a colon and the command in backticks. Engineers, review and QA run them in order in a worktree; a non-zero exit fails the check. Checks must leave `git status` clean: gitignore what they write.
- `## Proof`: one word, `qa` or `engineer`: who proves each criterion on the running app. `qa`, the default when the section is missing: engineers prove with tests and one real run of the changed path, and QA proves the rest before it merges, so engineers spend no build run on test-only work. `engineer`: engineers also prove each criterion in full, with evidence, before review.

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

## Proof

qa

## Outside steps

<each step a person or outside system takes before the app can be tested, one per line, with what needs it (running the app, one flow), who takes it, and the command or page that shows it's done, such as a feature flag turned on, a login, a VPN, or a reserved test host and how long to reserve it for. A session checks the steps its work needs before it starts the app or a test that calls an outside system, and stops, never waits, when one isn't done. Write none if there are none>

## Review

<the paths or functions whose code every review treats as high risk, such as `src/auth/` or `redact()` in `src/log.py`; `/maestro-code-review` reviews a changed code line there at tier 2, with its red team, but not a test or comment change. Name the code that does the risky work, not every file near it>

## Conventions

<commit style, where tests live, files never to touch, and the shared helpers new code must use instead of its own, such as one for safe file paths or for errors that never print a secret; engineers and review hold every change to them>
