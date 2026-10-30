---
name: maestro-technical-writing
description: "Write or rewrite a doc, README, PR description, commit message or ticket body so a reader gets it on the first read: pick its shape, write to plain-word rules, check every claim. Use for /maestro-technical-writing, 'write this up', 'tighten this', 'make it plain', or whenever you write text a person will read later. Not for code, a quick chat answer, or stripping AI tells from finished text: the maestro-unslop skill owns that."
---

# Technical writing

Write so a tired engineer gets it on the first read, and hand back the finished text. Never put in a claim you haven't checked. This skill owns the shapes and the writing rules; the **maestro-unslop** skill owns the list of AI tells.

## Start

Open a todo list with one item per phase below.

## Phase A: Pick the shape

1. If `## Conventions` in `.maestro/project/project-setup.md`, or a repo template such as `.github/pull_request_template.md`, sets a shape for this text, use that shape.
2. Else pick the row for what the reader wants. A text that does two jobs becomes two texts that link each other.

| The reader wants to | Write a | Shape |
|---|---|---|
| learn by doing | tutorial | one path to a working result; say what they should see after each step |
| finish a task | how-to | the goal, what they need first, numbered steps, a result they can check |
| look something up | reference | dry, complete tables: every flag, value and error; no story |
| understand a choice | explanation or decision | the context, the choice, the options turned down, and why |
| start using a project | README | what it is in one sentence, why to use it, a quickstart that works when pasted, links to the rest |
| review a change | PR description | what changed, why, how you checked it (commands and results), the risk, what is out of scope |
| trace the history | commit message | a subject of 72 characters or fewer that says what changed; a body wrapped at 72 that says why and what the diff can't show; no file list |
| build something | ticket body | the goal, acceptance criteria someone can test, pointers by path; the **maestro-create-ticket** skill owns the full shape |

## Phase B: Write to the rules

1. **Lead with the point.** Put the conclusion, the decision or the action first. Detail comes after.
2. **Cut every word you can.** If a sentence survives without a word, cut the word.
3. **Use the short, everyday word.** No jargon, foreign phrase or science word where a common word works. Keep a technical term when no everyday word means the same. Name the real file, flag or command, not a synonym for it.
4. **Write in the active voice.** Name who acts: "the server sends the request". Write steps as commands: "Run `make test`."
5. **Drop stock figures of speech.** No metaphor, simile or idiom you have seen in print. State the plain fact.
6. **Be concrete.** Give numbers, names, paths and examples, not adjectives or praise: not "the change can cause issues" but "renaming the column fails the build".
7. **Use strong verbs,** not nouns made from verbs: "decide", not "make a decision".
8. **Keep sentences short.** One idea each: 20 words at most for a step, 25 for anything else. Use a table or a diagram when it shows more than prose.
9. **Help the reader follow.** Put the condition before the step: "If the build fails, run `make clean`." Number steps, one action each. Call each thing by one name, and give each word one meaning. Keep "only" and "not" next to the word they change. Make each "it" and "this" point at one thing.
10. **Format for lookup.** Put code, commands, paths, flags and values in code font, and each command the reader runs in its own code block. Use Markdown only where it renders; a commit message is plain text. Make link text name the target, never "click here". Write dates as 2026-10-29, give units and time zones, and spell out an abbreviation the first time.

[references/example.md](references/example.md) shows one paragraph rewritten to these rules.

## Phase C: Check before you hand it on

1. Read it once as the reader. Can they act or decide without asking you?
2. Match each command, number and result to a run you made, copied from that run. Cut a claim you can't back, or say you didn't check it.
3. If a rule makes a sentence worse, fix the sentence another way. Never bend the meaning to obey a rule.
4. Fix the AI tells in the text the way Phase B of the **maestro-unslop** skill ([SKILL.md](../maestro-unslop/SKILL.md)) says.

**Reply:** the finished text, then one line for each rule you broke on purpose and why, or `no exceptions`.
