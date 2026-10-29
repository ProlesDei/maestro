# 0788 Slice 1 must accept use.yaml `commands`, or every run fails

- **Context:** Slice 1 (`dc104b1`) reads `skills/use.yaml` with unknown keys refused, and its `Uses` has only `prompts` and `bots`. The `commands` list (0781) is unknown to it. In a trial merge of all six slices, `TestRepoPlaybooksLint` fails on it. `engine.New` fails the same way, so every `maestro-play run` and `resume` would stop with `field commands not found in type playbook.Uses`. 0781 called this a lint follow-up, which understates it.
- **Choice:** **Merge follow-up for slice 1:**
  - `Uses` gets `Commands []string yaml:"commands"`.
  - Lint gives `commands` the same name and folder checks as `prompts`. It also fails a command skill that isn't manual-only both ways (`disable-model-invocation: true` and Codex's `agents/openai.yaml` policy, 0786). It also fails a `prompts` skill whose body calls a `commands` skill outside an "in your own session" line. `check.py` can't run these checks, because it doesn't parse `use.yaml` (0760).
  - The engine never names a command in a prompt.
  - Until then, slice 1's lint test fails on this slice's `use.yaml`. Leave it failing: the gate is doing its job.
- **Alternatives:** Move `commands` out of `use.yaml` (then two files say what gets installed, against 0781); merge this slice only after slice 1 adds the field (0700 C says no slice waits).
- **Why:** The fix is one field and two lint rules, in the slice that owns the schema.
