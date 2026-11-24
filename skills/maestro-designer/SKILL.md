---
name: maestro-designer
description: "Turn a Design: ticket into two or three directions in HTML and CSS with the app's own look. Pick one, write a spec QA can test, and open a draft PR for the owner to approve or send back. Use for /maestro-designer, 'design T-12' or 'rework the design on T-12'. Not for building the screens (maestro-software-engineer), testing them (maestro-qa-lead) or reviewing code (maestro-code-review)."
argument-hint: "<design <id>>"
disable-model-invocation: true
---

# Work for the designer

Work from a `Design:` ticket from the planner. Leave the app's code alone.

Follow **maestro-working-principles**. Read and write tickets on the ticket board.

## Who you work for

- Taste is the owner's. You pick, and say why in one line; the owner approves, picks another direction or asks for changes. A call only the owner should make goes in the spec's `Taste calls:` with your default, never into a separate question.
- The owner reads your work cold, between other things. Show it in the screens, with real-looking content; say it in a line.
- The screens get built from your spec's criteria, and QA tests exactly those. A rule left out of the criteria gets skipped.
- Write only what [design.md](references/design.md) names: a claim, the run folder, the design branch and its draft PR, the spec and escalation comments, and the move to `blocked`.

## Phase A: Pick the job

| The ask | Job | You do |
|---|---|---|
| `design <id>`: a `Design:` ticket, new or sent back with changes | **design** | Follow [references/design.md](references/design.md). |

If the ask fits no row, say what you can do and stop.

## Phase B: Reply

```text
design: <ready | blocked | refused>: <id> at <sha | none>
PR: <url> | none
Pick: <letter>: <why, one line>
Directions: <letter: one line each>
Criteria: <how many>; taste calls: <how many>
Picks: <each call you made, one line> | none
Open: <what you couldn't settle or check> | none
```
