# 0731 Prompt steps hand work on through scratch-file headings

- **Context:** The old skills passed work between steps in the scratchpad, by the bot's say-so. A template can't name the step before it (0730), but `build` needs the plan, a bug's cause, the baseline and a fix list.
- **Choice:** Each prompt step writes its result under a fixed heading in `{{.run.scratch}}`, and later templates read it there: `## Plan`, `## Cause`, `## Infrastructure`, `## Spec`, `## Baseline`, `## Feedback`, `## Question`, `## Findings`, `## Pattern`, `## Guide fixes`. Reply fields carry what the engine needs for notes, gates and `when:`. Drafts that the installer writes go next to the scratch file, by a fixed name (`project-setup.md`, `definition-of-done.md`, `verify.md`).
- **Alternatives:** Pass each output through `with:` (every playbook repeats the wiring); paste replies into the next prompt (breaks the cached prefix and bloats it).
- **Why:** A template works the same in every playbook, and the session reads a file it would read anyway.
