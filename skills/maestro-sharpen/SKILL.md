---
name: maestro-sharpen
description: "Rewrite a prompt so it asks for what a top expert would do, then hand it back without doing the task. Use for /maestro-sharpen, 'sharpen this prompt', 'make this prompt deeper', or before you send a prompt or brief that should go deeper, not longer. Not for doing the task, or for tightening a doc's wording: the maestro-technical-writing skill owns that."
argument-hint: "<the prompt, or a path to it>"
disable-model-invocation: true
---

# Sharpen a prompt

You hand back a deeper prompt. You never do the task, start the work it describes, or answer it. [references/example.md](references/example.md) shows one rewrite; read it before you write yours.

1. **Read for intent.** If you got a path, read the file and leave it unchanged. Name the job and the best person alive at it: "a staff SRE writing a postmortem", "a securities lawyer reading a term sheet". Keep that person in your head; the prompt gets their standards, not a "You are" line.
2. **Write what done looks like.** Put the expert's test for done, and what they would reject, in the prompt as criteria. Keep what the writer decided word for word; keep open what they left open.
3. **Add what the expert does unasked.** Add the standards, hidden needs, edge cases and failure modes an expert covers without being told, and only those that change the result: rollback for a migration, accessibility for a UI, the base rate for a forecast, callers for an API change.
4. **Name the raw material.** List what the expert would open first: files and folders by path, the ticket, logs, the spec, primary sources, a dataset. Tell the prompt to read them, not recall them. In a repo, check each path exists before you name it. If you can't check, say what kind of thing to look for. A fresh agent knows only the prompt, so add the context it can't see.
5. **Name the methods.** Make each named technique a step, not an adjective: five whys, mutation test, pre-mortem, differential diagnosis, bisect, characterisation test, red-team pass.
6. **Add an outside check.** Say what, other than the model that did the work, shows the result is right: a test that must pass, a command's output, a reference to compare against, a reviewer on another model, a metric with a threshold. Say what counts as failing.
7. **Go deep, not big.** Swap each vague word for a specific one: "thorough" becomes "check each caller of `X`". Cut filler, cheerleading, repetition and role-play preamble. Stay within 1.5 times the original length, unless the original was one line; if you go over, cut the additions that change the result least. Keep the writer's terms, names and voice.
8. **Leave choices open.** When the writer hasn't decided something the expert needs, don't decide it. Mark it inline as `[A | B]`, or list it at the end.
9. **Check the rewrite.** Find each name, number, path and decision from the original in it, and put back any you lost. Read it as whoever will run it: they should need to ask only about the open choices.

**Reply:** the rewritten prompt in one fenced block, ready to paste; `What changed:` with up to five bullets, each naming the step (2 to 6) it comes from; `Choices left to you:` with each open choice and its trade-off in one line, or `none`.
