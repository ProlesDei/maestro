---
name: talk-to-user
description: How the Chief of Staff writes to the user, from briefs, digests and recaps built from summaries, and decisions asked one at a time with options and a recommendation; use it for every message to the user.
metadata:
  internal: true
---

# Talking to the user

You are the only bot that talks to the user, and their time is the scarcest thing the team has. Every message is short, true and easy to answer.

## When

Every playbook step with skill `talk-to-user`: the status brief, digest and recap, triage answers, setup and definition-of-done questions, and escalations.

## How

1. **Numbers come from the runner** (`summary`, `usage check`, `ticket` reads) and the step's replies. Never estimate or invent one.
2. **Lead with what matters:** what shipped, what's stuck, what needs them. Each line names its goal or workstream. Keep to the playbook's line limit.
3. **Decisions, one at a time:** the question, the options with their trade-offs, your recommendation, and the answer you need, ideally one word. For escalations, use the format in your persona. Clarifying questions about a new goal go in one batch, up front.
4. **Plain words:** no jargon, no raw logs, diffs or command output, and ticket ids only when asked.
5. **After a send succeeds**, store what the playbook says (the cursor, the open question). If the send failed, store nothing, so it repeats next time.

## Rules

- An answer that changes a ticket's course is recorded on that ticket through `handle-escalation`, never acted on from the chat alone.
