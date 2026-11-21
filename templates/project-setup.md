# Project setup

`scripts/install.sh apply` copies this to `.maestro/project/project-setup.md` once; [INSTALL.md](https://github.com/ProlesDei/maestro/blob/main/INSTALL.md) has your agent fill it in with you. `install.sh check` reads three sections, so keep their shape:

- `## Main branch`: one line, the branch PRs merge into.
- `## Checks`: only bullets of a name, a colon and the command in backticks. Engineers, review and QA run them in order in a worktree; a non-zero exit fails the check. Checks must leave `git status` clean: gitignore what they write.
- `## Proof`: one word, `qa` or `engineer`: who proves each criterion on the running app. `qa`, the default when the section is missing: engineers prove each criterion with one real run, and QA proves the rest before it merges, so engineers spend no build run on QA's work. `engineer`: engineers also prove each criterion in full, with evidence, before review.

Sessions read the rest as plain text, but for `## Cache`, whose bullets `maestro-ticket cache` reads.

For a deterministic check, use `maestro-ticket check` in place of `slot` to share a pass between review and QA on the same PR head, base, and clean tree. In the check command set `MAESTRO_CHECK_ENVIRONMENT` to the identity of its pinned tools and other external inputs, such as the digest of its build image. Keep checks that depend on live state or need to produce local artifacts as ordinary commands. Review and QA supply `MAESTRO_CHECK_CANDIDATE`.

## Project

- Name: <name>
- Summary: <one or two sentences on what it does and who uses it>

## Runtime

Grok webhook by default. For T3, record the board ID, served roles and eligible profile aliases here. Keep connection files and account credentials in the OS accounts that use them.

## Main branch

main

## Checks

<each check a bullet of a name, a colon and the command in backticks, such as - lint: `make lint`. Wrap a check that takes minutes, strains the machine or writes temp files, such as a full build, in `maestro-ticket slot`, such as - build: `maestro-ticket slot --max 4 -- make build`, so many sessions at once don't make it slow or flaky, and each run's temp files go when it ends>

## Run

<how to start the app locally, and the URL or command to check it works; `/maestro-qa-lead` and `maestro-prove-complete` read this>

## QA

<how to test each PR, read by `/maestro-qa-lead`: how to build and start the app the way production does; the critical user flows for reviewed PRs, one per line; how to load data like production's; test accounts, by environment variable name; the screen sizes to check; a browser tool only if QA uses one other than Playwright CLI>

## Proof

qa

## Design

<one line, read by `/maestro-designer`: the path of the team's design rules file (tokens, type, spacing, motion, the checks no script runs), or none; then `dark theme: yes` if the app has one, so designs are shot in both>

## Cache

<leave as it is to cache nothing. Else one bullet per folder an install fills that works when copied to another path, as a name, a colon, every file that decides its content, `->` and the folder, such as ``- node: `package-lock.json`, `.nvmrc` -> `node_modules` ``; a Python `.venv` doesn't survive a copy, and build output depends on the source, which no key file names. Gitignore each folder, and have the check that installs skip the install when the folder is there. `maestro-ticket cache restore` copies one into a fresh worktree; QA saves it after a merge>

## Outside steps

<each step a person or outside system takes before the app can be tested, one per line, with what needs it (running the app, one flow), who takes it, and the command or page that shows it's done, such as a feature flag turned on, a login, a VPN, or a reserved test host and how long to reserve it for. A session checks the steps its work needs before it starts the app or a test that calls an outside system, and stops, never waits, when one isn't done. Write none if there are none>

## Review

<the paths or functions whose code every review treats as high risk, such as `src/auth/` or `redact()` in `src/log.py`; `/maestro-code-review` reviews a changed code line there at tier 2, with its red team, but not a test or comment change. Name the code that does the risky work, not every file near it>

## Conventions

<commit style, files never to touch, and the shared helpers new code must use instead of its own, such as one for safe file paths or for errors that never print a secret; engineers and review hold every change to them>
