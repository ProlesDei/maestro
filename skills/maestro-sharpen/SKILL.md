---
name: maestro-sharpen
description: "Rewrite a prompt into the brief a top expert would hand a capable agent: the goal and why, what's settled, the limits, what to read and the proof of done, with the steps left to the agent. Hands it back without doing the task. Use for /maestro-sharpen, 'sharpen this prompt', 'this prompt is too much', or before you send a prompt or brief to another agent. Not for doing the task, or for tightening a doc's wording: the maestro-technical-writing skill owns that."
argument-hint: "<the prompt, or a path to it>"
disable-model-invocation: true
---

# Sharpen a prompt

You hand back a sharper prompt: it says what a top expert would want done and why, and leaves the how to the agent that runs it. You never do the task, start the work it describes, or answer it. [references/example.md](references/example.md) shows one rewrite; read it before you write yours.

1. **Read for intent.** If you got a path, read the file and leave it unchanged. Name the job and the best person alive at it: "a staff SRE writing a postmortem", "a securities lawyer reading a term sheet". Keep that person in your head; the prompt gets their standards, not a "You are" line.
2. **Say the goal and why.** Open with what the work is for and what waits on it, in one or two sentences. The why lets the agent make the calls no one foresaw. If the original gives no why, leave it open (step 8).
3. **Mark what's settled.** Keep what the writer decided word for word, and say it isn't open: "These are decided; don't reopen them." Keep open what they left open.
4. **Set the limits.** Say what the agent must not touch, change or spend, and the shortcuts the expert would refuse: a longer timeout, a skipped test, a wider scope.
5. **Name the raw material.** List what the expert would open first: files and folders by path, the ticket, logs, the spec, primary sources, a dataset. Tell the prompt to read them, not recall them. In a repo, check each path exists before you name it. If you can't check, say what kind of thing to look for. A fresh agent knows only the prompt, so add the context it can't see.
6. **Write what done looks like.** Put the expert's test for done, and what they would reject, in the prompt as criteria. Add the standards and edge cases an expert covers unasked, only those that change the result: rollback for a migration, accessibility for a UI, callers for an API change. A method the expert insists on goes in as proof, not a step: "a profile names the slow step before any change". Swap each vague word for a specific one: "thorough" becomes "every caller of `X`".
7. **Add an outside check.** Say what, other than the model that did the work, shows the result is right: a test that must pass, a command's output, a reference to compare against, a reviewer on another model, a metric with a threshold. Say what counts as failing.
8. **Leave choices open.** When the writer hasn't decided something the expert needs, don't decide it. Mark it inline as `[A | B]`, or list it at the end.
9. **Leave the how to the agent.** Write no steps, plan or commands, and cut the ones the original has unless the writer decided them. Cut filler, cheerleading, repetition and role-play preamble. Aim shorter than the original; never past 1.5 times its length unless it was one line. Keep the writer's terms, names and voice.
10. **Check the rewrite.** Find each name, number, path and decision from the original in it, and put back any you lost. Count its length against the original's; if it runs past the limit in step 9, cut the additions that change the result least until it fits. Read it as whoever will run it: they should need to ask only about the open choices, and be free to pick how.

**Reply:** the rewritten prompt in one fenced block, ready to paste; `What changed:` with up to five bullets, each naming the step (2 to 9) it comes from; `Choices left to you:` with each open choice and its trade-off in one line, or `none`.
