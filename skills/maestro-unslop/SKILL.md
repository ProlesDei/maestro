---
name: maestro-unslop
description: Use when you're cleaning up writing. Removes the tells of AI-generated text (inflated words, stock phrases, reflexive triads and bullets, hedging, filler) and keeps the meaning, the facts and the author's voice.
argument-hint: "<text, or a path to it>"
disable-model-invocation: true
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Unslop

Edit the text so it reads like a careful person wrote it. Change how it's said, never what it says. Don't add facts, examples or opinions.

## Tells to remove

**Words.** Replace each one with the plain word, or cut it:
- delve, leverage, utilize, robust, seamless, crucial, pivotal, vital, comprehensive, cutting-edge, game-changer
- tapestry, landscape, realm, journey, navigate, empower, unlock, elevate, streamline, foster, harness
- showcase, underscore, testament, meticulous, nuanced, holistic, synergy, paradigm, ever-evolving, bustling, vibrant
- "in order to", "a wide range of", "plays a key role in", "it is worth noting that"

**Constructions:**
- "It's not X, it's Y" and "not just X but Y", when nobody claimed X
- "Whether you're X or Y…", "In today's fast-paced world…", "Let's dive in", "Here's the thing"
- triads by reflex: three adjectives, three parallel clauses, three bullets when two facts exist
- rhetorical questions answered in the next sentence
- signposting: "In conclusion", "Overall", "In summary", "Moreover", "Furthermore", "Additionally" at the start of a sentence
- a closing summary that restates the paragraph above it
- stacked hedges ("may potentially help to somewhat…"), false enthusiasm, "Great question", "I hope this helps", offers to do more
- em dashes used as general-purpose punctuation. Use a comma, colon, parentheses or a full stop.

**Format:**
- a heading over three lines of text
- bullets where a sentence would do
- bold on every other phrase
- emoji bullets
- Title Case Headings (use sentence case)
- tables for two items

## Method

1. Read the whole text first and note its purpose, its reader and the author's own terms.
2. Go sentence by sentence. Give each one a concrete subject and verb, put numbers where there were adjectives, and use the active voice where the actor matters.
3. Cut each sentence that says nothing, and check that nothing was lost when it went.
4. Leave untouched: code blocks, quotes, names, links, numbers, technical terms, and the author's deliberate style.
5. Read it aloud in your head. If it sounds like a press release, go again.

## Reply

The cleaned text, then one line: `Changed: <n> word swaps, <n> sentences cut, <n> constructions rewritten, <n> format fixes.` In a file, edit it in place and show the diff instead.
