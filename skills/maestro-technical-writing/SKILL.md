---
name: maestro-technical-writing
description: "Pick one type for a text, write it to the owner's rules, fit it to its shape, and check every sentence. Use for /maestro-technical-writing, 'write this up', 'tighten this', 'make it plain', or whenever you write or review a doc, README, PR description, commit message, ticket body or reply."
---

# Technical writing

Write so a tired engineer gets it on the first read. This skill owns text types, the writing rules and the shapes; the **maestro-unslop** skill owns the list of AI tells and word swaps.

## Start

Open a todo list with one item per phase below.

## Phase A: Pick one type

Each text does one job. If it does two, split it and link the parts.

| The reader wants to | Type | Shape |
|---|---|---|
| learn by doing | tutorial | one path to a working result; say what they should see after each step |
| finish a task | how-to | the goal, what they need first, numbered steps, a result they can check |
| look something up | reference | dry, complete tables: every flag, value and error; no story |
| understand why | explanation | the context, the choice, the options turned down, the reasons |

## Phase B: Write to the rules

1. **Lead with the point.** Put the conclusion, the decision or the action first. Detail comes after.
2. **Cut every word you can.** If a sentence survives without a word, cut the word.
3. **Use the short, everyday word.** No jargon, foreign phrase or science word where a common word works. Name the real file, flag or command, not a synonym for it.
4. **Write in the active voice.** Name who acts: "the server sends the request". Write steps as commands: "Run `make test`."
5. **Drop stock figures of speech.** No metaphor, simile or idiom you have seen in print. State the plain fact.
6. **Be concrete.** Give numbers, names, paths and examples, not adjectives: not "the change can cause issues" but "renaming the column fails the build".
7. **Use strong verbs,** not nouns made from verbs.
8. **Keep sentences short.** One idea each: 20 words at most for a step, 25 for anything else. Use a table or a diagram when it shows more than prose.
9. **Help the reader follow.** Put the condition before the step: "If the build fails, run `make clean`." Number steps, one action each. Call each thing by one name, and give each word one meaning. Keep "only" and "not" next to the word they change. Make each "it" and "this" point at one thing.
10. **Format for lookup.** Put code, commands, paths, flags and values in code font, each command in its own code block. Make link text name the target, never "click here". Write dates as 2026-10-29, give units and time zones, and spell out an abbreviation the first time.

## Phase C: Fit the shape

- **README:** what it is in one sentence, why to use it, a quickstart that works when pasted, and links to the rest.
- **PR description:** what changed, why, how you checked it (commands and results), the risk, and what is out of scope. No boasting; a reviewer gets it in one read.
- **Commit message:** a subject of 72 characters or fewer that says what changed, then a body that says why and what the diff can't show. No file list.
- **Ticket body:** the goal, acceptance criteria someone can test, and pointers by path. The **maestro-create-ticket** skill owns the full ticket shape.
- **Decision:** the context, the choice, the options turned down, and why.

## Phase D: Check before you hand it on

1. Read it once as the reader. Can they act or decide without asking you?
2. Check that you ran each command and copied it from that run, and that each term means one thing.
3. If a rule makes a sentence worse, fix the sentence another way. [references/example.md](references/example.md) shows one rewrite.
4. Run the **maestro-unslop** skill ([SKILL.md](../maestro-unslop/SKILL.md)) on the text.

**Reply:** the finished text, then one line for each rule you broke on purpose and why, or `no exceptions`.
