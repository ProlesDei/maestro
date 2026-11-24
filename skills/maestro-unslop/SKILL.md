---
name: maestro-unslop
description: "Strip the tells of AI text, keep every fact and the author's voice, and check that nothing was lost or added. Use for /maestro-unslop, 'unslop this', 'clean up this writing', 'make this sound less like AI', or before you send a doc, PR body, commit message or reply that reads as generated. Not for deciding what a text says or how it is laid out: the maestro-technical-writing skill owns that."
argument-hint: "<text, or a path to it>"
disable-model-invocation: true
---

# Unslop

Edit the text so a careful person could have written it, and prove each fact made it through. Change how it says things, never what it says: add no facts, reasons, examples or opinions. This skill owns the list of AI tells, in [references/tells.md](references/tells.md).

## Start

Open a todo list with one item per phase below.

## Phase A: Fence off what stays

1. Read the text whole. Note its reader, where it will show (a page that renders Markdown, a commit message, a chat), and who wrote it: an agent, or a person with a style of their own.
2. List every fact: each number, name, path, command, flag, date and link, and each claim a reader could check. Phase C checks the new text against this list.
3. Mark what you never edit: code blocks, inline code, quotes, names, titles of works, links, numbers, and a listed word used in its literal sense (a test harness, a vector type, a landscape page).

## Phase B: Fix the tells

1. Read [references/tells.md](references/tells.md) in full.
2. For a file, run `grep -nE '—|–|“|”|‘|’' <file>` to find the dashes and curly quotes a reader's eye skips.
3. Go through the text once for each group in the list: words, constructions, punctuation and format, chat leftovers. Fix each hit the way its line says.
4. Weigh who wrote it. In an agent's text, fix every hit. In a person's text, fix a hit only when it gets in the reader's way, and keep their asides, humor, opinions and deliberate style.
5. Rewrite each sentence you touch to the rules in Phase B of the **maestro-technical-writing** skill ([SKILL.md](../maestro-technical-writing/SKILL.md)).
6. Never trade one tell for another: no new dashes, no colon mid-sentence where a full stop works, no run of sentences all the same length, no new list of three.

## Phase C: Prove it

1. Find each item from the Phase A list in the new text, and put back any you dropped.
2. Check the reverse: every claim in the new text was in the old one. Cut any reason, purpose or number you added, even a likely one.
3. Search the new text once more for the tells that outlast a first pass: "not X but Y", a punchy closing line, lists of three, dashes, bold.
4. Read it as its reader. If it still sounds like a press release, go round Phase B again.

**Reply:** the cleaned text (for a file, edit it in place and show the diff), then `Facts kept: <n> of <n>`, each fact you cut and why, and each tell you left on purpose and why.
