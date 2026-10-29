# Evals

Golden tasks: one known situation per playbook, with the result it must produce. They catch regressions before real work sees a change to a playbook, prompt template or persona, and they are step 5 of the improvement loop.

Phase 5 is done when every playbook has a golden task here that passes.

## Layout

- `evals/<role>/<playbook>.md`: the golden task for `playbooks/<role>/<playbook>.yaml`. More cases for the same playbook go in `<playbook>--<case>.md`.
- `evals/fixtures/`: the sample repos and board states tasks start from ([fixtures](fixtures/README.md)).
- `evals/check.py`: the repo's docs check: golden tasks and that every playbook has one, no Markdown playbooks left, `skills/` holding only the user's flat skill folders, each with frontmatter a harness can parse, no persona, playbook or prompt template naming a skill, links, repo paths and README's diagram embeds. Run `python3 evals/check.py`; it exits `0` when clean. Its tests: `python3 evals/test_check.py`.
- Playbook steps, templates, runner requests, ticket writes and `skills/use.yaml` are checked by `maestro-play lint` (`go test ./...` in `tools/maestro-play/`), which owns the playbook schema.

## Format

- **Frontmatter**
  - `playbook`: `<role>/<name>`, the playbook under test.
  - `fixture`: the fixture it starts from.
  - `status`: `draft` (never run), `passing` or `failing`, as of the last run.
- **Body**
  - **Setup:** the board and repo state on top of the fixture: tickets with type, status, role, owner, `Tries`, comments; the trust level; anything in the inbox or logs.
  - **Input:** what starts the playbook: the intake candidate, the user's message, or the schedule.
  - **Expected:** the end state: ticket status, role and `Tries`; the comments by prefix; branches, commits and pull requests; messages to the user; the step ids that ran, in order, and the ones recorded as `Skipped:`.
  - **Must not:** what fails the task outright, whatever else happened.
  - **Checks:** how the result is judged. `cmd:` lines are deterministic commands and their expected result. `judge:` lines are rubric items scored by a model on the other family to the one under test.

## How a task runs

The eval runner isn't built yet (the runner command `evals` is its interface). It will:

1. Copy the fixture into a scratch dir with its own git repo and ticket store, and apply Setup.
2. Start the playbook on the Input with the runner's `play` request, through the real runner and a real harness session, with a small budget. The bot with the role's profile answers any `ask` step.
3. Run every `cmd:` check, then every `judge:` check, and record pass or fail with the evidence.
4. Leave nothing outside the scratch dir.

Until then every task is `draft`. A task becomes `passing` only from a recorded run. Before that, `maestro-play render <role>/<job> --step <id> --ticket <id>` on the task's fixture shows each prompt step as it would be sent, without running it.

## Rules

- One situation per task, small enough to run in a few minutes.
- Checks read the store, git and the logs, never the bot's own summary.
- No secrets in fixtures. Use fake values that look nothing like real keys.
