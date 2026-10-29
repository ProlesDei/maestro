# 0508 Golden task format and a Python format check

- **Context:** Phase 5 is done when every playbook has a passing golden task in `evals/`. Nothing defined the format, and no eval runner exists.
- **Choice:** One task per playbook at `evals/<role>/<playbook>.md` with Setup, Input, Expected, Must not and Checks (`cmd:` deterministic, `judge:` scored by the other model family). Fixtures are described in `evals/fixtures/README.md`. Every task is `draft` until a recorded run. `evals/check.py` (Python 3, standard library) checks playbook format, the index, runner commands, skill names against `skills/` plus `evals/skills-needed.md`, who may set `done`, golden task coverage and markdown links.
- **Alternatives:** A Go checker (needs a module outside `tools/`, which is out of this slice); a shell and awk script (fragile for link and table parsing).
- **Why:** The format lets tasks be written now and run later; the checker is the gate this phase can actually pass. Python 3 is already used by `docs/design/maestro-usage.py`. Tasks passing on a real run is still open.
