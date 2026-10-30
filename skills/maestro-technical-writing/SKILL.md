---
name: maestro-technical-writing
description: "Use when you write or review a doc, README, PR description, commit message, ticket body or reply, or when the user says \"write this up\", \"tighten this\" or \"make it plain\". Picks one type for the text, then checks every sentence against the owner's writing rules."
---

# Technical writing

Write so a tired engineer gets it on the first read. Pick the type, write to the rules, fit the shape, then check.

## 1. Pick one type

Each text does one job. If it does two, split it and link the parts.

| The reader wants to | Type | Shape |
|---|---|---|
| learn by doing | tutorial | one path to a working result; say what they should see after each step |
| finish a task | how-to | the goal, what they need first, numbered steps, a result they can check |
| look something up | reference | dry, complete tables: every flag, value and error; no story |
| understand why | explanation | the context, the choice, the options turned down, the reasons |

## 2. Write to the rules

1. **Lead with the point.** Put the conclusion, the decision or the action first. Detail comes after.
2. **Cut every word you can.** If a sentence survives without a word, cut the word. "In order to" is "to". "It is important to note that" is nothing.
3. **Use the short word.** "Use", not "utilise". "Start", not "initiate". "Help", not "facilitate".
4. **Write in the active voice.** Name who acts: "the server sends the request", not "the request is sent". Write steps as commands: "Run `make test`."
5. **Drop stock figures of speech.** No metaphor, simile or idiom you have seen in print. State the plain fact.
6. **Use everyday words.** No jargon, foreign phrase or science word where a common word works. Name the real file, flag or command, not a synonym for it.
7. **Be concrete.** Give numbers, names, paths and examples, not adjectives. Not "the change can cause issues" but "renaming the column fails the build".
8. **Use strong verbs.** "Decide", not "make a decision". "Check", not "perform a check".
9. **Keep sentences short.** One idea each: 20 words at most for a step, 25 for anything else. Use a table or a diagram when it shows more than prose.

## 3. Help the reader follow

- Put the condition before the step: "If the build fails, run `make clean`."
- Number the steps, one action each. Put each command in its own code block.
- Call each thing by one name throughout, and give each word one meaning.
- Put code, commands, paths, flags and values in code font. Make link text name the target; never write "click here".
- Write dates as 2026-10-29, give units and time zones, and spell out an abbreviation the first time.

## 4. Fit the shape

- **README:** what it is in one sentence, why to use it, a quickstart that works when pasted, and links to the rest.
- **PR description:** what changed, why, how you checked it (commands and results), the risk, and what is out of scope. No boasting; a reviewer should get it in one read.
- **Commit message:** a subject of 72 characters or fewer that says what changed, then a body that says why and what the diff can't show. No file list.
- **Ticket body:** the goal, acceptance criteria someone can test, and pointers by path. `/maestro-planner` and `/maestro-create-ticket` hold the full ticket shape.
- **Decision:** the context, the choice, the options turned down, and why.

## 5. Check before you hand it on

Read it once as the reader. Can they act or decide without asking you? Did you run each command and copy it from that run? Does each term mean one thing? Is each sentence still needed? If a rule makes a sentence worse, fix the sentence another way. [references/example.md](references/example.md) shows one rewrite. Last, run `/maestro-unslop` on it.

Reply: the finished text, then one line for each rule you broke on purpose and why, or "no exceptions".
