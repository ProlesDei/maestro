---
name: maestro-unslop
description: "Strip the tells of AI text and keep the meaning, the facts and the author's voice. Use for /maestro-unslop, 'unslop this', 'clean up this writing', 'make this sound less like AI', or before you send a doc, PR body or message that reads as generated."
argument-hint: "<text, or a path to it>"
disable-model-invocation: true
---

# Unslop

Edit the text so a careful person could have written it. Change how it says things, never what it says: add no facts, examples or opinions. This skill owns the list of AI tells and word swaps.

## Phase A: Find the tells

1. Read the text whole. Note its purpose, its reader and the author's own terms.
2. Swap each wordy form for the plain one:
   - utilize, leverage: use. facilitate, empower: help. initiate, commence: start. numerous, a wide range of: many.
   - in order to: to. due to the fact that: because. in the event that: if. it is worth noting that, it is important to note that: cut.
   - serves as, stands as, boasts: is, has. make a decision, perform a check: decide, check.
3. Cut or replace with the plain fact: delve, robust, seamless, crucial, pivotal, vital, comprehensive, cutting-edge, game-changer, tapestry, landscape, realm, journey, navigate, unlock, elevate, streamline, foster, harness, showcase, underscore, testament, meticulous, nuanced, holistic, synergy, paradigm, ever-evolving, bustling, vibrant, intricate, interplay, garner, enhance.
4. Replace metaphor nouns with the thing's real name: substrate, vector, surface (as in "API surface"), primitive (as a noun), scaffolding, bedrock, wedge, ratchet, endgame, north star, flywheel.
5. Rewrite these constructions:
   - "It's not X, it's Y" and "not just X but Y", when nobody claimed X
   - an "-ing" tail that adds a vague claim: "..., highlighting its importance", "ensuring quality"
   - "Whether you're X or Y...", "In today's fast-paced world...", "Let's dive in", "Here's the thing"
   - vague sources ("experts believe", "studies show"): name the source or cut the claim
   - triads by reflex: three adjectives, three parallel clauses, three bullets when two facts exist
   - a rhetorical question answered in the next sentence
   - "Moreover", "Furthermore", "Additionally", "Overall", "In conclusion" at the start of a sentence
   - a closing summary that restates the paragraph above it, or a generic close ("the future looks bright")
   - stacked hedges ("may potentially help to somewhat"), false cheer, "Great question", "I hope this helps", offers to do more
   - three names for one thing: pick one and keep it
   - a sentence that names a feeling, not a fact ("SQL you can read"): name the mechanism or the number, or cut it
   - dropped articles, arrows and verbless fragments: write whole sentences
   - em dashes: use a comma, colon, parentheses or a full stop
6. Fix the format: a heading over three lines of text, bullets where a sentence would do, bold on every other phrase, a bold label that repeats the line after it, emoji bullets, Title Case Headings (use sentence case), curly quotes, a table for two items.

## Phase B: Rewrite

1. Give each sentence a concrete subject and verb. Put numbers where there were adjectives. Use the active voice where the actor matters.
2. Cut each sentence that says nothing, then check that nothing was lost.
3. Leave untouched: code blocks, quotes, names, links, numbers, technical terms, and the author's deliberate style.
4. Read it again as its reader. If it sounds like a press release, go round again.

**Reply:** the cleaned text (for a file, edit it in place and show the diff), then `Changed: <n> word swaps, <n> sentences cut, <n> constructions rewritten, <n> format fixes.`
