---
name: maestro-unslop
description: "Use for /maestro-unslop, 'unslop this', 'clean up this writing', 'make this sound less like AI', or before you send a doc, PR body or message that reads as generated. Removes the tells of AI text and keeps the meaning, the facts and the author's voice."
argument-hint: "<text, or a path to it>"
disable-model-invocation: true
---

# Unslop

Edit the text so a careful person could have written it. Change how it says things, never what it says. Add no facts, examples or opinions.

## 1. Read it whole

Before you change a word, note the text's purpose, its reader and the author's own terms.

## 2. Remove the tells

**Words.** Swap each for the plain word, or cut it:
- delve, leverage, utilize, robust, seamless, crucial, pivotal, vital, comprehensive, cutting-edge, game-changer
- tapestry, landscape, realm, journey, navigate, empower, unlock, elevate, streamline, foster, harness
- showcase, underscore, testament, meticulous, nuanced, holistic, synergy, paradigm, ever-evolving, bustling, vibrant
- "in order to", "a wide range of", "plays a key role in", "it is worth noting that"

**Constructions:**
- "It's not X, it's Y" and "not just X but Y", when nobody claimed X
- "Whether you're X or Y…", "In today's fast-paced world…", "Let's dive in", "Here's the thing"
- triads by reflex: three adjectives, three parallel clauses, three bullets when two facts exist
- rhetorical questions answered in the next sentence
- "In conclusion", "Overall", "In summary", "Moreover", "Furthermore", "Additionally" at the start of a sentence
- a closing summary that restates the paragraph above it
- stacked hedges ("may potentially help to somewhat…"), false cheer, "Great question", "I hope this helps", offers to do more
- em dashes as all-purpose punctuation: use a comma, colon, parentheses or a full stop

**Format:** a heading over three lines of text, bullets where a sentence would do, bold on every other phrase, emoji bullets, Title Case Headings (use sentence case), a table for two items.

## 3. Rewrite sentence by sentence

1. Give each sentence a concrete subject and verb. Put numbers where there were adjectives. Use the active voice where the actor matters.
2. Cut each sentence that says nothing, then check that nothing was lost.
3. Leave untouched: code blocks, quotes, names, links, numbers, technical terms, and the author's deliberate style.

## 4. Read it again

Read it aloud in your head. If it sounds like a press release, go round again.

Reply: the cleaned text, then one line, `Changed: <n> word swaps, <n> sentences cut, <n> constructions rewritten, <n> format fixes.` For a file, edit it in place and show the diff instead of the text.
