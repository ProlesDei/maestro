---
name: maestro-log-entry
description: Append this session's one-line retro and log record to the shared log; use it last at every wrap-up, and at the end of scheduled duties with no ticket.
disable-model-invocation: true
metadata:
  internal: true
---

# Log entry

You leave one record per session so the improvement loop can find patterns. Usage and cost are recorded by the runner, so you don't log them.

## Inputs

- Ticket id (or the duty name), role, holder `<bot-id>/<session-id>`, playbook.
- The status the ticket ended in, and the Tries count from the last read.
- If the session failed or escalated, the reason in a few words.

## Steps

1. **Retro.** One line, under 120 characters: what slowed me down? Be specific. "Nothing" is a fine answer.
2. **Append** one JSON line to `$(git rev-parse --git-common-dir)/maestro/logs/<YYYY-MM-DD>.jsonl` (UTC date). Never rewrite existing lines.

```json
{"ts":"2026-10-28T14:02:11Z","ticket":"T-12","role":"junior-software-engineer","holder":"jse-1/6f1c...","playbook":"build-ticket","status":"in_review","tries":1,"failure":null,"retro":"test fixture setup took three tries"}
```

## Reply

```yaml
status: done
summary: logged T-12
evidence:
  - logs/2026-10-28.jsonl +1 line
retro: test fixture setup took three tries
next: clean up
questions: []
```

## Rules

- One line per session. A retried prompt checks for its own `holder` and `ticket` in today's file first and doesn't add a second line.
