# 0600 Wake a Grok Bot from a ticket change

- **Context:** Phase 6 needs a ticket change to wake the right bot with no tokens spent in between (HANDOFF.md). The plan: write an outbox row in the same transaction as the change, a hook delivers it with retries and dedupe by event id, and a no-AI sweep catches expired leases. Checked 2026-10-29 on the box (Debian 13, curl 8.14.1). Research only: no `tools/` code changed, and no Grok Bot routine was created or called.
- **Choice:** Use a Grok Bot routine webhook per bot, called by a box-side deliverer that reads the existing `events` table as its outbox, sends one combined wake per bot (not one per event), and lets the bot drop repeat wakes with its existing wake gate. Fallback: a scheduled routine, used only as a slow safety net. **Status: needs Paul** for the bot-side setup listed at the end.
- **Alternatives:** GitHub issue comments, email to the Bot's address, a 5-minute polling routine, or Bot-to-Bot messages from the Chief of Staff (costs below).
- **Why:** The webhook is the only trigger that is instant, needs nothing besides HTTPS, and costs a model run only when something changed. Outbound HTTPS works from the box.

## Findings

| # | Finding | Status | Source |
|---|---|---|---|
| a1 | Routine triggers: schedule; Slack message; GitHub, Linear, Sentry or PagerDuty event; email to the Bot's own address; webhook call. Event triggers come through Cursor account integrations, separate from the Slack or GitHub apps | VERIFIED (docs) | [GB-r], [CU-r] |
| a2 | Webhook: the routine's Webhook section shows `POST to` (URL), `key` (secret) and the full `Authorization: Bearer <key>` header. Optional JSON body, passed to the Bot with the routine's instruction. `200` = run accepted and started, not finished. Anything else = no run | VERIFIED (docs) | [GB-r] |
| a3 | Limits: 200 runs/h per routine, 300/h across all routines, 50 routines per Bot, schedules at least 5 minutes apart. A run refused with `Automation run rate limit exceeded` is not retried by Grok: the sender must send again. Every run spends usage | VERIFIED (docs) | [GB-r] |
| a4 | Each routine must be created by asking the Bot in chat. Only events after the trigger existed count. Routines can be paused, including automatically after a long time away | VERIFIED (docs) | [GB-r] |
| a5 | Other wakes: Bot-to-Bot async message ("the receiving Bot wakes"), @-tag on X. No api.x.ai endpoint to message a Bot (0140) | VERIFIED (docs) / API: UNVERIFIED | [GB-c], 0140 |
| a6 | `/home/box/reference/` has only `app-ui.md` (a routine list lives in the per-agent info pane) and `debugging-the-box.md`. Nothing on webhooks. `~/.grok/docs` is Grok Build, not Grok Bot: nothing on routines | VERIFIED (box) | `ls`, `grep` |
| b1 | Outbound HTTPS POST works: `curl -X POST https://httpbin.org/post` with a JSON body, `Authorization` and `Idempotency-Key` headers → `200` in 0.49 s, body and headers echoed intact. TLS verified normally (Let's Encrypt chain, no `-k`). Traffic goes through the sandbox egress tunnel (`SAND_EGRESS_TUNNEL_PROXY_ADDR`, remote IP 198.18.0.1) | VERIFIED (box) | curl run |
| b2 | `docs.x.ai`, `cursor.com`, `api.cursor.com`, `api.github.com` all answer from the box. The real webhook host isn't known yet, so reaching it is UNVERIFIED | VERIFIED (box) / host: UNVERIFIED | curl run |
| b3 | `curl -H @file` reads the header from a 0600 file, so the key never shows in `ps` or shell history | VERIFIED (box) | curl run |
| c1 | The key travels in a header, not the URL. Whether the URL itself is also guessable or secret: UNVERIFIED. Key rotation exists ("uses the current key") but how to rotate: UNVERIFIED | VERIFIED (docs) / rest UNVERIFIED | [GB-r] |
| c2 | No idempotency key, dedupe or queue is documented: every accepted POST is a new run. What happens when a POST arrives while that routine's last run is still going (queued, run in parallel, or refused): UNVERIFIED | UNVERIFIED | [GB-r], [CU-r] silent |
| c3 | The `events` table is already an outbox: written in the same `BEGIN IMMEDIATE` transaction as the change, append-only by trigger, `seq` only grows and commits in order. A separate outbox row isn't needed, only a delivery cursor | VERIFIED (code) | `tools/maestro-ticket/README.md`, `sqlitestore.go:64-76` |
| c4 | A lease that expires writes no event: the ticket just becomes claimable. So the hook alone never sees an expiry, and the sweep is needed | VERIFIED (code) | `tools/maestro-ticket/README.md` (Leases) |
| d1 | No cron on the box (`crontab` not found), no systemd user bus, PID 1 is `tini`. A box-side sweep must be a process someone starts, or run as part of other CLI calls | VERIFIED (box) | `which`, `systemctl --user`, `/proc/1/cmdline` |
| d2 | SMTP egress is blocked: TCP to `smtp.gmail.com` on 587 and 25 times out. No `sendmail`/`msmtp`/`mail` | VERIFIED (box) | `/dev/tcp` probe, `which` |
| d3 | `gh` is installed but not logged in. `api.github.com/repos/ProlesDei/maestro` returns 404 without auth, so the repo is private (comments would not be public) | VERIFIED (box) | `gh auth status`, curl |
| d4 | Email trigger: one address per account at `mail.grokbot.com`, can't be renamed or deleted. New mail wakes nothing unless a routine watches for it. Routines run only for senders that pass email authentication, can't filter by subject, and mail from the last minute may not show yet | VERIFIED (docs) | [GB-ca] |

## Fallbacks and their cost

| Option | Latency | Cost per wake | Setup cost | Problems |
|---|---|---|---|---|
| Webhook (chosen) | Seconds to start a run (start delay UNVERIFIED) | One run, only when something changed | One routine per bot, key stored on box | Busy behaviour and URL secrecy unknown (c1, c2) |
| GitHub issue comment | UNVERIFIED | One run per matching event, plus a GitHub API call | GitHub token on the box (none now, d3), Cursor GitHub integration on the bot side | Puts ticket traffic into GitHub; needs a narrow match rule or every comment wakes the Bot |
| Email to the Bot | Up to a minute or more (d4) | One run per email | Box can't send SMTP (d2): needs an HTTPS mail API account and a sending domain that passes SPF/DKIM | Heaviest setup; spam scan can drop it; the address is permanent |
| 5-minute polling routine | Up to 5 min | One run every 5 min whether or not anything changed: 288 runs/day per bot, 12/h per routine (84/h for seven bots, under 300) | None on the box | Breaks Phase 6's "zero tokens in between"; docs warn short intervals "can use a week of usage in a day" |
| Bot-to-Bot from Chief of Staff | Seconds (UNVERIFIED) | One Chief of Staff turn plus one run of the target | Chief of Staff must itself be woken first | Doubles cost; Chief of Staff becomes a single point of failure |

## Recommended design

1. **Outbox = `events`.** Add a `deliveries` cursor table (per target bot: last delivered `seq`, attempts, next try time, last status). No second row per change.
2. **Routing.** A pure function maps an event (type, status, role) to the bot that must act, as the Phase 6 done criterion asks. One webhook routine per bot.
3. **Deliverer.** After a write commits, the CLI starts a detached `deliver` that, per bot with undelivered events, waits a short debounce (a few seconds), then sends one POST: body `{"wake_id":"<bot>:<max_seq>","since":<cursor>,"max_seq":N}`, header from `~/.config/maestro/wake/<bot>.header` via `curl -H @file` or the Go equivalent. One delivery per bot at a time (a lock file). Combining events keeps a busy hour under 200 runs per routine.
4. **Retries.** `200`: advance the cursor. `429`/rate-limit error, `5xx`, timeout: back off (for example 30 s, 2 m, 10 m, capped), keep the cursor. `401`/`403`/`404`: stop and report (wrong key, paused or deleted routine). A timeout after Grok accepted can cause a second run, so retries must be safe on the bot side.
5. **Dedupe on the bot side.** The routine instruction runs the existing wake gate (`maestro-ticket events --since <cursor> --limit 1 --json`) first and ends if nothing is new. A repeat or late wake costs one short run, never repeated work. Grok has no dedupe of its own (c2).
6. **Sweep (no AI).** A `sweep` command finds tickets whose lease expired while still needing work and queues a wake for them, and retries failed deliveries. Run it at the start of every `deliver`, and from a loop the bot runtime starts on the box (no cron, d1). Safety net only: one slow scheduled routine (hourly or less) per bot that runs the wake gate, in case the box-side loop is down.
7. **Secrets.** `~/.config/maestro/` mode 0700, one header file per bot mode 0600, never in the repo or the ticket store, never on a command line. `doctor` should check the modes. Rotate by replacing the file.

## Needed from the bot side

For each of the seven bots (or at least the Chief of Staff first):

1. Ask the Bot to create a routine with a webhook trigger and the instruction from step 5. Copy the `POST to` URL and the `key` from the routine's Webhook section on desktop.
2. Tell us the URL's host and shape, and whether the URL alone is secret (c1).
3. How to rotate the key, and whether rotating changes the URL.
4. Run two `Test` calls, then two POSTs a few seconds apart while the first run is busy, and report what happened: both ran, the second queued, or the second was refused, and with which status code (c2).
5. The time from a `200` to the run starting, from the Bot's run history.
6. Whether a routine can run `maestro-ticket` on this box (the Bot's computer) or runs somewhere else.
7. Whether the JSON body has a size limit, and how the body reaches the instruction.

Sources: [GB-r] https://docs.x.ai/grok-bot/skills-routines-and-automations (last updated 2026-10-28) · [CU-r] https://cursor.com/help/grok-bot/routines · [GB-c] https://docs.x.ai/grok-bot/chat-and-collaboration · [GB-ca] https://docs.x.ai/grok-bot/computer-and-apps#give-your-bots-an-email-address. Pages fetched with curl on the box and read as text, so wording is close to the source.
