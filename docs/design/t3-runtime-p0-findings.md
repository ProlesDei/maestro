# T3 runtime P0 findings, 2026-11-20

Start S1 with native profile identity, subscription billing and timestamped allowance readings. Both harnesses exposed a weekly used fraction. Neither small job moved the displayed fraction, so the readings cannot price one job or establish the provider's update delay. Keep fresh work paced by projected subscription usage across eligible accounts, with no worker-count cap.

Source: PR #104, head `28734484c293e96b60c5fb6ccb303689f1aa4445`. The checkout matched `git ls-remote origin refs/pull/104/head` when the trial started. The governing files were AGENTS.md and docs/design/t3-runtime.md at that commit. Versions throughout: T3 0.1.9, Claude Code 2.1.329, Codex CLI 0.171.0. The trial used a private temporary Git repo, a local bare remote and a separate loopback T3 server and data directory. No ticket store was connected.

## Subscription readings

| Trial | Direct evidence before and after | Conclusion |
|---|---|---|
| WSL default Claude login | `claude auth status` reported an email, claude.ai, firstParty, max, max_20x. Interactive `/usage` gave 97% used for all models and 0% for Fable. Both reset Nov 25 at 3 pm America/Vancouver. Credits were off. Both percentages stayed the same across a successful Opus medium job that returned P0-OK. Samples were 6.6 seconds apart. | **Observed** for one Max subscription. The A/B labels belong to Maestro. The harness exposed an identity that can back a local alias. |
| Claude machine-readable event | A second small Opus job in stream-json mode emitted seven_day, utilization 0.97, resetsAt 1795644000, allowed_warning, isUsingOverage false. | **Observed** at 97%. The second profile emitted a different window with no numeric utilization. |
| Second Max login (follow-up) | The owner signed into a separate WSL profile at ~/.claude-max-b. Native profile metadata showed distinct account IDs and emails. Claude Code 2.1.329 reported claude.ai, firstParty and max_20x. Before and after a successful Opus medium P0-B-OK job, /usage showed 36% session use (reset 1:49 pm Vancouver), 24% weekly use (Nov 26, 2:59 am) and 0% Fable use (Nov 26, 3 am). Credits were off. The stream-json event reported five_hour, resetsAt 1795207800 and isUsingOverage false, with no numeric utilization. | **Observed** for the second Max subscription. Set CLAUDE_CONFIG_DIR to the second profile for both the meter and the job. Update delay is **inconclusive**. |
| Codex Pro | Native app-server account/read returned chatgpt, pro and an email. account/rateLimits/read returned limitId codex, usedPercent 1, windowDurationMins 10080, resetsAt 1795800882 (Nov 27 17:34:42 UTC), secondary null. A later check showed no credits. Primary readings matched at 19:40:04 and 19:40:35 UTC, before and after the Astra question. Medium review and a separate high-effort P0-HIGH turn also succeeded. | **Observed** for the Pro login. The meter exposed one window in this run. |
| Update delay and per-job spend | No visible change on either meter. Concurrent account activity was possible. Session token counts and Claude's estimated dollar cost use different units from the subscription meter. | **Inconclusive** for both providers. Treat a sample time as the time read. Neither provider supplied a verified last-update time in these samples. |

To repeat, check the native login before launching, leave inherited API credentials unset, and compare identity privately. On Claude, open /usage, run a one-line job with `claude -p --model opus --effort medium --output-format json`, then reopen /usage. For the event, add `--verbose --output-format stream-json` and select only rate_limit_event fields. On Codex, start `codex app-server` on stdio, send initialize, then account/read and account/rateLimits/read before and after the turn. Record only the plan, windows, percentages, reset times, credit status and a local alias. These readings establish the native subscription path. They do not measure an invoice.

## Chief handoff and uncertain send

1. Start `t3 serve` on a private loopback port with a new --base-dir. Add the scratch project. Use that base directory on every subsequent T3 command. Start a codex/gpt-6.2-astra thread that asks “Q-P0-827 Which color should the scratch label use?”
2. Separately send a user message containing D-P0-617. Discard the send receipt. Astra replied ACK-D. Leave the driver without a receipt.
3. Relay the human's reply verbatim in a user turn with the question ID on its own envelope line. Stop the handoff before any Maestro write. Terminate the scratch T3 server and start a new server process on the same data directory.
4. Start a fresh claudeAgent/claude-opus-5-5 thread. Give it the predecessor thread reference and the two marker IDs, without the reply. Have it use `t3 thread show --messages 20 --json` to read the conversation.

**Observed.** After the server restart, Opus returned “what? I don't get the question”, tied it to Q-P0-827, and marked the decision open. The question was in run 1 and the reply in run 4. The relay supplied the question ID. This tests a tagged handoff, not inference among several untagged questions.

**Observed.** The recovered history contained one user delivery of D-P0-617 in run 2 and an assistant ACK-D. A successor could recognize this settled send without resending it. The first trial left queued sends open. The follow-up below tests them. A crash before persistence, atomic claim transfer and exactly-once planner delivery remain **inconclusive**. The trial restarted T3 after the messages had reached history and stopped the scratch handoff before recording the reply. It exercised no Maestro or Atlas transaction.

### Busy thread and restart, 2026-11-20

**Observed on T3 0.1.9, Codex CLI 0.171.0, Astra on Pro.** Start a scratch turn that runs a short shell sleep. While it is active, send a second prompt with a unique marker and a fixed reply. Check the JSON send result, stop only the scratch server process, and restart on the same private data directory. Read the thread to completion without resending. Count matching user prompts and replies and compare their run IDs to the queued receipt.

| Stop | Evidence | Result |
|---|---|---|
| SIGTERM, twice | The CLI returned queued for QBUSY-831 and QUEUE-832 on separate runs. Each later appeared once with its expected reply under its original run ID. For QUEUE-832 the port had no listener before restart. | **Observed** |
| SIGKILL after queued receipt | ABRUPT-833 was still queued after restart. Once recovery finished, the transcript contained one user prompt and one ACK-833 under the original run ID ending in ordinal:14. | **Observed** |
| Send while scratch server was down | A separate send exited 1 between stopping and starting the server. Its OFFLINE-831 marker had no matching user message after recovery. | **Observed** |

For QUEUE-832, the scratch run table still said queued while the restarted server ran a recovery turn. During that interval, thread show omitted the queued prompt. The final reply arrived 52.5 seconds after the original request. Recovery kept the queued run ID ending in ordinal:10 even though the run table then showed ordinal 12. Use the full run ID when available. A missing transcript marker alone cannot justify a resend. The read-only table inspection was diagnostic; this trial establishes no public lookup API for a pending send whose receipt was lost.

## Review portability and chief response

Create a local Git pull request with `git request-pull`, and pin refs/pull/1/head. This fixture used base `48dbbce33991851c0b169cf3929712482f5d47ee` and head `d4f24d2690385a5bf39a3d7b01296268e5110f1b`. Its only criterion required “Color: red”. The diff replaced “Color: amber” with “Color: reed”.

In separate fresh clones, copy the pinned Maestro review skill (SHA-256 `7a599ee06903465a50977f1cc9aeb05f3d32e08e4610c65449165e9801f3ed92`) and a fixture instruction file containing marker JUNIPER-19. Ask each reviewer to load both files, inspect HEAD and the diff against origin/main, apply Phase F's blocker rule and return a report-only verdict. The native traces showed both harnesses reading the review instructions. Both returned the marker, the full pinned SHA, the changed value and REPORT-ONLY NO-GO.

| Reviewer | Fresh native review session | Chief completion during review | Conclusion |
|---|---|---|---|
| Codex, gpt-6.2-astra medium, Pro | 01a1fa7a-37b7-7fc1-9665-8e7f8e27e359 | 3.33 s, Astra returned PING | **Observed** |
| Claude Code, Opus 5.5 medium, Max | dd37a825-68b7-4cff-910b-ad919fea8ce1 | 2.85 s, Opus returned PING during a fresh repeat of the same review | **Observed** |

For latency, start the reviewer, wait two seconds, then time a one-line prompt through `t3 thread send --wait` to the chief on the same subscription. The review process was alive at both ends of each successful measurement. An initial standalone Claude ping exited 1 without a reply. The successful retry used the established T3 Opus chief. These are single end-to-end timings. Sustained headroom and tail latency remain **inconclusive**. The fixture tested explicit instruction loading and a commit-bound review. Hosted PR lookup, automatic role discovery, full skill gates and posting remain unqualified.

## Ticket order

| Finding | Effect on the plan | Ticket |
|---|---|---|
| Native identities and weekly meters are readable. Two distinct Max profiles are available. Update delay is unknown. | Bind aliases to verified native identities. Return each observed window, source and sample time. Keep missing or stale fields explicit. | **S1 first**, profile and harness settings |
| Concurrent chief pings succeeded. One job's percentage cost is unknown. | Reserve estimated consumption against each eligible account and reconcile with later readings. Measure more wakes before setting chief headroom. | S2, admission and recovery |
| A settled send and tagged owner reply survived the T3 restart. | Carry question and handoff IDs with the private thread reference. Test ambiguous sends and replay before acknowledging planner work. | S3, chief memory and delivery |
| Both reviewers found the same blocker at one head. | Use review as the first shared role. Qualify hosted review and sustained load before enabling automatic routing. | S1 adapters, then S2 routing |
