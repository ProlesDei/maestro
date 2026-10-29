# Evals

Golden tasks: one known situation per playbook, with the result it must produce. They catch regressions before real work sees a change to a playbook, skill or persona, and they are step 5 of the improvement loop.

Phase 5 is done when every playbook has a golden task here that passes.

## Layout

- `evals/<role>/<playbook>.md`: the golden task for `playbooks/<role>/<playbook>.md`. More cases for the same playbook go in `<playbook>--<case>.md`.
- `evals/fixtures/`: the sample repos and board states tasks start from ([fixtures](fixtures/README.md)).
- `evals/check.py`: the repo's format check: playbooks, golden tasks, skills, runner requests, ticket writes (a write to a held ticket passes `--holder` and `--token`, decision 0434), links, repo paths and README's diagram embeds. Run `python3 evals/check.py`; it exits `0` when clean.

## Format

- **Frontmatter**
  - `playbook`: `<role>/<name>`, the playbook under test.
  - `fixture`: the fixture it starts from.
  - `status`: `draft` (never run), `passing` or `failing`, as of the last run.
- **Body**
  - **Setup:** the board and repo state on top of the fixture: tickets with type, status, role, owner, `Tries`, comments; the trust level; anything in the inbox or logs.
  - **Input:** what starts the playbook: the intake candidate, the user's message, or the schedule.
  - **Expected:** the end state: ticket status, role and `Tries`; the comments by prefix; branches, commits and pull requests; messages to the user; the steps run and the steps recorded as `Skipped:`.
  - **Must not:** what fails the task outright, whatever else happened.
  - **Checks:** how the result is judged. `cmd:` lines are deterministic commands and their expected result. `judge:` lines are rubric items scored by a model on the other family to the one under test.

## How a task runs

The eval runner isn't built yet (the runner command `evals` is its interface). It will:

1. Copy the fixture into a scratch dir with its own git repo and ticket store, and apply Setup.
2. Run the bot with the role's profile on the Input, through the real runner and a real harness session, with a small budget.
3. Run every `cmd:` check, then every `judge:` check, and record pass or fail with the evidence.
4. Leave nothing outside the scratch dir.

Until then every task is `draft`. A task becomes `passing` only from a recorded run.

## Rules

- One situation per task, small enough to run in a few minutes.
- Checks read the store, git and the logs, never the bot's own summary.
- No secrets in fixtures. Use fake values that look nothing like real keys.
