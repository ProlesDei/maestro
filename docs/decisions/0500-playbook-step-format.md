# 0500 Playbook steps carry Role, Skill and Model lines

- **Context:** G5.3 and G4.1: every step said `Skill: none` and pasted its procedure, and 28 steps had the bot run commands. The step format had no field for who acts.
- **Choice:** Each step has `Role` (`harness`, `runner` or `bot`), `Skill` (one skill in backticks: `maestro-*` for harness, `runner` for runner, a bot skill or `none` for a bot decision), and `Model` (the `model-selection` tier, or `Bot`). `Tier` is renamed `Model`. Runner steps add `Run`; optional `Skip when`. `none` is allowed only for plain bot decisions (6 steps). `evals/check.py` enforces it.
- **Alternatives:** Keep `Tier` and add only `Skill`; a `Runs in:` field instead of `Role` (but the gate asks for Role/Skill/Model).
- **Why:** One look tells the bot whether to prompt, call the runner or decide, and the check makes drift fail loudly.
