# AI tells

The tells **maestro-unslop** fixes, by group. Each line names the tell, then the fix. A listed word is a tell only as filler or as a figure of speech; in its literal sense it stays.

## Words

Swap for the plain word:

- utilize, leverage: use. facilitate, empower: help. initiate, commence: start. numerous, a wide range of: many. align with: match.
- in order to: to. due to the fact that: because. in the event that: if. it is worth noting that, it is important to note that, notably: cut.
- serves as, stands as, boasts, features: is, has. make a decision, perform a check: decide, check.
- enhance, bolster, elevate, streamline: say what changed ("cuts p99 latency to 18 ms").

Cut, or replace with the plain fact:

- delve, tapestry, testament, realm, journey, navigate, unlock, foster, showcase, underscore, highlight (as in "highlights its value"), meticulous, nuanced, holistic, synergy, paradigm, ever-evolving, bustling, vibrant, nestled, in the heart of, intricate, interplay, garner, enduring, insights, multifaceted, universally.
- robust, seamless, comprehensive, cutting-edge, game-changer, crucial, pivotal, vital, key (as an adjective), landscape (as in "the AI landscape").
- Newer models: genuinely, honestly, actually, truly, quietly (as fillers), load-bearing, full stop, worth flagging, the real X is, here's the kicker, here's the catch.

Name the real thing instead of these metaphor nouns: substrate, vector (as in "a vector for"), surface (as in "API surface"), primitive (as a noun), scaffolding, bedrock, wedge, lever, ratchet, endgame, north star, flywheel, nexus, harness (as a metaphor).

Keep one hedge where the claim is unsure; cut the rest of a stack ("may potentially help to somewhat"). A single "perhaps" or "tends to" reads as human, so leave it.

## Constructions

- "It's not X, it's Y", "not just X but Y", "Y rather than X", "Not X. Not Y. Just Z.", when nobody claimed X: state Y.
- An "-ing" tail with a vague claim ("..., highlighting its importance", "ensuring quality"): cut it, or say what happens.
- Inflated stakes ("marking a pivotal moment", "shaping the future of", "every millisecond counts"): cut.
- A false range ("from startups to enterprises") whose ends sit on no one scale: list the real cases.
- Stock openers ("In today's fast-paced world", "Whether you're X or Y", "Let's dive in", "Here's the thing"): cut.
- Vague sources ("experts believe", "studies show", "the data tells us"): name the source or cut the claim.
- Lists of three by reflex (three adjectives, three clauses, three bullets for two facts): use the real number.
- A question answered in the next sentence: state the answer.
- Arguing with no one ("This isn't about speed", "Some might say"): cut the claim nobody made.
- "Despite these challenges" before a cheerful close, or a stock close ("the future looks bright"): cut, or give the real next step.
- A summary that repeats the paragraph above it, or "In summary", "Overall", "In conclusion": cut.
- The same transition ("Moreover", "Additionally", "Furthermore") opening sentence after sentence: cut most. One is fine.
- A punchy one-liner closing each paragraph, or a saying built to be quoted ("Ship it or kill it"): state the fact.
- A coined label for an ordinary thing ("the review tax"): name the thing.
- Text about the text ("This section explains", "Let's look at"): cut, and say the thing.
- Three names for one thing: pick one and keep it.
- A sentence that names a feeling, not a fact ("SQL you can read"): give the mechanism or the number, or cut it. If it could sit unchanged in another project's docs, cut it.
- Code that acts like a person ("the cache knows when to expire", "the parser wants a date"): say what the code does.
- Dropped articles, arrows and verbless fragments ("bad date → exit 2"): write whole sentences.

## Punctuation and format

- Em dashes: Claude still uses more than people do. Use a full stop, a comma or parentheses, and vary the choice: text with only commas and full stops is a newer tell. Keep a person's deliberate dashes.
- A colon joining two clauses to set up a reveal: use a full stop. A colon before a list or an example is fine.
- Every sentence 8 to 20 words with the same beat: vary the length.
- Curly quotes and apostrophes: straight ones.
- Title Case Headings: sentence case. A skipped heading level: fix the level. A heading over three lines of text: cut it.
- Bold on every other phrase: bold nothing, or one warning. A bold label that repeats its line ("**Speed:** It's faster"): make it a sentence. A bold lead-in that names the item and adds new detail is fine.
- Emoji in headings or bullets: cut.
- Bullets where a sentence would do, or a table for two items: write prose.
- Markdown where it won't render, such as a commit message or a plain-text field: plain text.

## Chat leftovers

- "Great question", "Certainly!", "You're absolutely right", "I hope this helps", "Let me know if", offers to do more: cut.
- Preambles ("Here's the rewritten version:", "Here are three options:"): cut.
- Placeholders ("2025-XX-XX", "INSERT_URL", "[Your name]"): fill from the source, or flag them. Never invent the value.
- Leftover markup (`contentReference[oaicite:1]`, `?utm_source=chatgpt.com`): strip it.
- Claims about the work with no check behind them, in a PR body or commit message ("preserved all functionality", "ensured consistency", "no breaking changes"): give what you ran and what it showed, or cut.
- Source hedges ("based on available information", "as of my last update"): cut, or give the date and source.

Sources: [Wikipedia: Signs of AI writing](https://en.wikipedia.org/wiki/Wikipedia:Signs_of_AI_writing), Kobak et al., [Delving into LLM-assisted writing](https://arxiv.org/abs/2406.07016) (2024), pstack's [unslop](https://github.com/cursor/plugins/tree/main/pstack/skills/unslop), and [humanizer](https://github.com/blader/humanizer).
