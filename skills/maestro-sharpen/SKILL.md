---
name: maestro-sharpen
description: "Rewrite a prompt so it asks for what a top expert would do, then hand it back without doing the task. Use for /maestro-sharpen, 'sharpen this prompt', 'make this prompt deeper', or before you send a prompt that should go deeper, not longer."
argument-hint: "<the prompt, or a path to it>"
disable-model-invocation: true
---

# Sharpen a prompt

You hand back a deeper prompt. You never do the task, start the work it describes, or answer it.

1. **Read for intent.** Name the job and the best person alive at it: "a staff SRE writing a postmortem", "a securities lawyer reading a term sheet". Say what done looks like to them and what they would reject. Keep what the writer decided word for word; keep open what they left open.
2. **Add what the expert does unasked.** Add the standards, hidden needs, edge cases and failure modes an expert covers without being told, and only those that change the result: rollback for a migration, accessibility for a UI, the base rate for a forecast, callers for an API change.
3. **Name the raw material.** List what the expert would open first: files and folders by path, the ticket, logs, the spec, primary sources, a dataset. Tell the prompt to read them, not recall them. If you don't know whether they exist, say what kind of thing to look for.
4. **Name the methods.** Make each named technique a step, not an adjective: five whys, mutation test, pre-mortem, differential diagnosis, bisect, characterisation test, red-team pass.
5. **Add an outside check.** Say what, other than the model that did the work, shows the result is right: a test that must pass, a command's output, a reference to compare against, a reviewer on another model, a metric with a threshold. Say what counts as failing.
6. **Go deep, not big.** Swap each vague word for a specific one: "thorough" becomes "check each caller of `X`". Cut filler, cheerleading, repetition and role-play preamble. Stay within 1.5 times the original length, unless the original was one line. Keep the writer's terms, names and voice.
7. **Leave choices open.** When the writer hasn't decided something the expert needs, don't decide it. Mark it inline as `[A | B]`, or list it at the end.

**Reply:** the rewritten prompt in one fenced block, ready to paste; `What changed:` with up to five bullets, each naming the step (2 to 5) it comes from; `Choices left to you:` with each open choice and its trade-off in one line, or `none`.
