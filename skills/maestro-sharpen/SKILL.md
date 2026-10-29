---
name: maestro-sharpen
description: Make a prompt deeper, not bigger, before you send it. Adds what a top professional would do that you didn't ask for, the raw material they'd start from, their methods, and an outside check that the result is right. Hands back the rewritten prompt and leaves your choices open. Doesn't do the task.
argument-hint: "<the prompt, or a path to it>"
disable-model-invocation: true
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Sharpen a prompt

You rewrite the prompt. You never do the task, never start the work it describes, and never answer it.

## 1. Read it for intent

- What job is this, and who is the best person alive at it? Name the role, for example "a staff SRE doing a postmortem" or "a securities lawyer reviewing a term sheet".
- What would "done" look like to that person? What would they reject?
- What has the writer already decided (keep it word for word)? What did they leave open (keep it open)?

## 2. Add depth in four places

1. **What a top pro would do unasked.** These are the standards, hidden requirements, edge cases and failure modes that an expert covers by reflex, such as rollback for a migration, accessibility for a UI, the base rate for a forecast, or callers for an API change. Add only the ones that change the result.
2. **Raw material.** Name the exact inputs an expert would open first: files and directories by path, the ticket, the logs, the spec, prior decisions, the primary sources, a dataset. Tell the prompt to read them, not to recall them. If you know they exist, name them. If you don't, say what kind of thing to look for.
3. **Methods.** Use the named techniques the expert would use, such as a five-whys, a mutation test, a pre-mortem, a differential diagnosis, a bisect, a characterisation test, a red-team pass. Make each one a step, not an adjective.
4. **An outside check.** Say how the result gets verified by something other than the model that produced it: a test that has to pass, a command's output, a reference to compare against, a second reviewer on a different model, a metric with a threshold. Say what counts as failing it.

## 3. Keep it deep, not big

- Replace each vague word with a specific one ("thorough" becomes "check each caller of `X`").
- Cut filler, cheerleading, repetition and role-play preamble that changes nothing.
- Keep the rewrite within about 1.5× the original length, unless the original was a single line.
- Keep the writer's terms, names and voice.

## 4. Leave the choices open

When the writer hasn't decided something the expert would need decided, don't decide it. Put it inline as `[A | B]`, or list it at the end.

## Reply

1. The rewritten prompt, in one fenced block, ready to paste.
2. **What changed:** up to five bullets, each naming which of the four places it deepens.
3. **Choices left to you:** each open choice with its trade-off in one line, or "none".
