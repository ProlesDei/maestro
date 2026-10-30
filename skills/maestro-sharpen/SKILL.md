---
name: maestro-sharpen
description: "Use when the user types /maestro-sharpen or says 'sharpen this prompt', 'make this prompt deeper', or before you send a prompt that should go deeper, not longer. Rewrites the prompt with what a top expert would add and hands it back. Never does the task."
argument-hint: "<the prompt, or a path to it>"
disable-model-invocation: true
---

# Sharpen a prompt

You rewrite the prompt. You never do the task, start the work it describes, or answer it.

1. **Read for intent.** Name the job and the best person alive at it, for example "a staff SRE writing a postmortem" or "a securities lawyer reading a term sheet". Say what done looks like to them and what they would reject. Note what the writer already decided (keep it word for word) and what they left open (keep it open).

2. **Add what the expert does unasked.** These are the standards, hidden needs, edge cases and failure modes an expert covers without being told: rollback for a migration, accessibility for a UI, the base rate for a forecast, callers for an API change. Add only the ones that change the result.

3. **Name the raw material.** List the exact inputs the expert would open first: files and folders by path, the ticket, the logs, the spec, primary sources, a dataset. Tell the prompt to read them, not recall them. If you don't know whether they exist, say what kind of thing to look for.

4. **Name the methods.** Use the named techniques the expert would use: five whys, mutation test, pre-mortem, differential diagnosis, bisect, characterisation test, red-team pass. Make each one a step, not an adjective.

5. **Add an outside check.** Say what, other than the model that did the work, shows the result is right: a test that must pass, a command's output, a reference to compare against, a reviewer on another model, a metric with a threshold. Say what counts as failing.

6. **Keep it deep, not big.** Swap each vague word for a specific one ("thorough" becomes "check each caller of `X`"). Cut filler, cheerleading, repetition and role-play preamble. Stay within about 1.5 times the original length, unless the original was one line. Keep the writer's terms, names and voice.

7. **Leave choices open.** When the writer hasn't decided something the expert would need, don't decide it. Mark it inline as `[A | B]`, or list it at the end.

Reply: the rewritten prompt in one fenced block, ready to paste; then `What changed:` with up to five bullets, each naming the step (2 to 5) it comes from; then `Choices left to you:` with each open choice and its trade-off in one line, or `none`.
