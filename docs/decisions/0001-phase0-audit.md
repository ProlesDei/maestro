# 0001 Phase 0 audit of main

- **Context:** PR #3 merged without the ten-pass review, and the brief asks for a gap list against it. Audited `main` at `17b6a3e` (tree `81260e5`) on 2026-10-28. Audit only: nothing was fixed.
- **Choice:** File every finding here with a severity (0004), evidence and an owning phase. Each phase turns its findings into tickets or fixes (0003).
- **Alternatives:** Fix as found (out of scope for this run).
- **Why:** One list, ordered by phase, so no gap is lost before ticketing works.

## Gates (run in `tools/at-ticket`, Go 1.27.3)

| Command | Result |
|---|---|
| `go test -race -count=1 ./...` | exit 0: cli 3.97s, render 1.04s, sqlitestore 14.99s, ticket 1.03s (43 tests, including the 200-process claim race) |
| `go vet ./...` | exit 0, no output |
| `gofmt -l .` | exit 0, no output |
| `staticcheck ./...` (v0.8.2, built from the module cache, 0005) | exit 0, no output |

Mutation testing on a scratch copy: 11 of 15 mutations killed. Survivors: switching `_txlock=immediate` to `deferred` (M1) and dropping the version guard in `applyClaim` (M5), both covered by SQLite's snapshot checks plus the busy retry, so they're redundant guards rather than test holes; lease expiry at the exact millisecond (M4, `<=` → `<`); the re-check of `user_version` inside the migrate transaction (M10).

## Review findings (ten passes)

Repros (R1–R5) ran as tests on the scratch copy and passed, meaning the bad behaviour happens.

| id | sev | pass | evidence | phase |
|---|---|---|---|---|
| F1 | must-fix | red-team, spec | **Review and QA can be skipped.** `ticket.go:87` lets `blocked` go to any earlier or later status. R1: `todo → in_progress → blocked → ready_to_release → done` is accepted for actor `jse`. The store doesn't remember the status a ticket was blocked from. | 2 |
| F2 | must-fix | concurrency | **Claim replay after lease loss reports success.** `idempotent` (`sqlitestore.go:308-316`) replays the stored claim. R2: A claims with key K, the lease expires, B claims, A retries K and gets `err=nil, holder=jse/a` while the store says `jse/b`. A works on a ticket it doesn't hold. | 2 |
| F3 | must-fix | concurrency | **No fencing.** R3: a session whose lease expired can comment while another holds the lease (`Comment`, `sqlitestore.go:702-732`, has no holder check), and can transition once the new holder releases (`sqlitestore.go:663` only checks a *live* claim). Same-holder re-claim doesn't bump `version` (`:517-522`). | 2 |
| F4 | must-fix | failure modes | **Expired leases are silent.** R4: an hour past expiry, `LastSeq` is unchanged and there are 0 `lease_expired` events. The event is only written when someone re-claims (`:524-531`), so an event-driven Chief of Staff never learns a session died. | 2, 6 |
| F5 | must-fix | spec | **Fields the settled decisions need don't exist:** no ticket type (ship/scout), no Tries counter, no `backlog` status (`ticket.go:60-75, 198-210`). The retry rule and per-type lifecycles have nothing to store or enforce. Status list: 0008. | 2 |
| F6 | should | spec, red-team | **Anyone can move an unclaimed ticket, and anyone can set Done.** R5: `in_progress` by actor `random`, then `in_progress → done` by `junior`, both accepted (`ticket.go:83`, `sqlitestore.go:663`). Brief: only the Operations Manager sets Done. Identity model: 0010; scout closing: 0009. | 2 |
| F7 | should | failure modes | **A busy retry can duplicate read results.** `List` and `Events` append to a slice declared outside the retried closure (`sqlitestore.go:401, 736` with `read` at `:194-203`). A reader `SQLITE_BUSY` mid-scan repeats rows, breaking "events never repeat". Rare in WAL, cheap to fix. | 2 |
| F8 | should | data | **Backlog.md renders into whichever worktree runs it.** `cli.go:509-520` defaults to `<toplevel>/.agent-team/Backlog.md`. In a scratch repo on branch `at/T-1`, `render` left `?? .agent-team/` for the next `git add -A`. | 2 |
| F9 | should | failure modes | **No environment checks.** `storePath` (`cli.go:220-232`) takes any git common dir. A bare repo got a store at `bare.git/agent-team/tickets.db` and `doctor` said ok. No network-filesystem or lock checks. | 2 |
| F10 | later | data | Idempotency keys are global, not scoped per holder, and the table grows forever (`sqlitestore.go:78-84`). | 2 |
| F11 | later | performance | `list` and `events` default to no limit (`cli.go:336, 478`), and `render` loads every ticket. Fine now, not at swarm scale. | 2 |
| F12 | later | data | The `lease_expired` event names the *new* claimer as actor (`:525`). Releasing your own expired lease logs `released`, not `lease_expired` (`:609-612`). | 2 |
| F13 | later | failure modes | `Doctor` ignores `rows.Err()` after `foreign_key_check` (`:818-826`). `ID.UnmarshalJSON` accepts 0 and negatives on the numeric path (`ticket.go:29-34`). A relative `--store` resolves against the process cwd, not `Env.Dir` (`cli.go:221`). | 2 |
| F14 | later | test potency | No tests for lease expiry at the exact instant (M4), concurrent first open/migrate (M10), or `BEGIN IMMEDIATE` being in force (M1). | 2 |

Clean passes: **security**: no secrets, all SQL takes parameters (only fixed column lists and `?` placeholders are concatenated), render escapes `|` and newlines (mutation M14 killed). **Contracts**: exit codes and JSON error shape match the README and are covered by `cli_test.go`. **Data**: WAL, `synchronous=NORMAL`, foreign keys and busy timeout are verified by `doctor` and its test, and the driver (modernc v1.61.0) honours these DSN keys (`sqlite.go:348-385` in the module cache). Skeptic: dropped "deferred tx lock would double-claim" and "missing version guard" (M1/M5 show SQLite already prevents both), and kept F7 at should because no repro was possible.

## Ticket engine bullets (Phase 2)

| Bullet | State | Evidence |
|---|---|---|
| Heartbeats from a background process | missing | `heartbeat` is a CLI command only (`cli.go:393-410`). `claim` starts nothing (`cli.go:363-391`). |
| Fencing tokens | partial | `version` bumps on claim, release and transition (`sqlitestore.go:533, 624, 674`), and `--expect-version` is optional (`:660`). No lease number; comments unfenced; same-holder re-claim doesn't bump. F3. |
| Area locks | missing | No areas in the schema (`sqlitestore.go:36-85`) or in `pickClaimable` (`:497-512`). |
| Dependencies | missing | No dependency table. `pickClaimable` filters by status, lease and role only (`:497-506`). |
| Lease reaper | missing | Expiry is lazy, logged only on re-claim (`:524-531`), with no per-ticket expiry count. F4. |
| Workstreams and budgets in the store | missing | Schema has tickets, comments, events and idempotency only. |
| Schema migrations | partial | `user_version` and a refuse-newer check (`:31, 149-176`, tested by `TestNewerSchemaRefused`). No stepwise migrations: any older version re-runs the v1 schema. |
| Busy timeout | present | `busyTimeoutMS = 5000` (`:32, 118`), checked by `doctor` (`:805`). |
| `BEGIN IMMEDIATE` for every write | present | Writer pool uses `_txlock=immediate` with one connection (`:119-123`). All writes go through `write` (`:180`). |
| Tiny transactions | present | Each write is one ticket-sized transaction. No I/O or git inside a transaction. |
| Regular checkpoints | missing | Only SQLite's default auto-checkpoint. No `wal_checkpoint` call or schedule. |
| Database clock for lease expiry | missing | Expiry uses Go's clock (`nowMS`, `:232`; `Options.Now`, `:90`). Same OS clock on one host, but not what the brief asks for. |
| Backups: export and snapshot, on a schedule | missing | No such command (`cli.go:76-88`). |
| Environment detection | missing | F9. |
| Render Backlog.md in one place only | missing | F8. |
| Race tests: kill mid-transaction, expiry vs heartbeat, write after re-claim | missing | `race_test.go` covers ClaimNext, comments, optimistic transition and idempotent create only. `TestHeartbeatAfterExpiryFails` is sequential. |
| External tools are mirrors | partial | at-ticket is the store (correct). No sync code (fine for now). `docs/design/project-setup.md` and `docs/design/agent-team.mermaid` still treat Jira or Backlog.md as where agents work. |

## Gaps by phase

**Phase 0 (this phase)**
- G0.1 must-fix: `docs/design/team-rules-draft.md` is listed in the brief (`HANDOFF.md:40`) but isn't in the repo, in commit `9c99331`, or anywhere on the box. Phase 4 needs it. Needs Paul.
- G0.2 should: findings aren't tickets yet (0003). The "checkout matches main" check is unverified from the box (0002).

**Phase 1: rename** (waiting for approval)
- G1.1 must-fix: 203 lines in 42 files outside `docs/design/` use old names, including 10 Go files, the module path `tools/at-ticket`, `AT_TICKET_STORE`/`AT_TICKET_ACTOR` (`cli.go:61, 137, 224`), the store path (`cli.go:231`) and the render path (`cli.go:520`). Scope: 0007. `docs/design/maestro-usage.py` already uses Maestro names.

**Phase 2: harden at-ticket.** See the bullet table and F1–F14. Also:
- G2.1 must-fix: the CLI has nothing for Tries, fencing tokens, workstreams, dependencies or areas (no `comment --holder`, no `--deps`, no `--areas`). Skills can't follow the brief until these exist.

**Phase 3: cost**
- G3.1 must-fix: nothing under `tools/` for usage. The prototype has no tests in the repo despite "tested" in the brief, and no Grok parser (`maestro-usage.py:222` TODO). `grok` on this box has a `usage` subcommand worth checking. No harness call is wrapped.

**Phase 4: rules, skills, personas**
- G4.1 must-fix: bots run commands. `skills/model-selection/SKILL.md:18` has the bot runtime run "No tokens" steps in the repo, and 28 playbook steps use it (worktree add, build, test, merge, push, deploy). Persona lines tell bots to read code or run checks: `senior-software-engineer:20-24`, `junior-software-engineer:20-24`, `code-reviewer:20`, `qa-lead:24-27`, `operations-manager:21, 51`. Where scripts run instead: 0011.
- G4.2 must-fix: heartbeats from LLM turns. `prompt-contract:18`, `playbooks/README.md:40`, `senior build-ticket:47`.
- G4.3 must-fix: no skill has an internal or manual-only flag. Bot and harness skills share `skills/`.
- G4.4 must-fix: missing harness skills: escalate and verify. Handoff note, log entry and propose memory are bundled inside `at-wrap-up`. `at-wrap-up:22` commits memory proposals on the ticket branch, which bypasses the curated inbox.
- G4.5 must-fix: the Chief of Staff works at ticket level (`chief-of-staff:31, 37-38, 68`; `triage-request:38-42`; `handle-escalation`). One Planner owns the whole board (`planner:16, 35-43`), with no workstream sessions.
- G4.6 must-fix: personas never mention Goal → Workstream → Ticket, ship and scout tickets, trust levels, the kill switch (`harness-cli:12` puts it in `environment.md`, not a stop file), preflight, memory tiers or the secrets rule.
- G4.7 must-fix: personas point to rules "in `AGENTS.md`" (`senior:19`, `junior:19, 28`, `code-reviewer:39`, `qa-lead:30`, `operations-manager:31`). Maestro must not rely on the user's AGENTS.md.
- G4.8 should: rules are duplicated across files: secrets in 8 places, junior-escalates-after-2 in 5, opposite-model-family in 4, plus session reuse and the retry loop. Phase 4's done criteria need each in one file.
- G4.9 should: `harness-cli` preflight's "git clean" (`:19`) fails once F8 leaves Backlog.md behind. Permissions (`:74-77`) ignore trust level: Ops can push at level 1 and engineers can't open PRs. Grok and Codex details are unmarked as verified (Claude Code and Grok flags were checked against `--help` on this box and exist).
- G4.10 should: `docs/design/project-setup.md:11-34` makes Backlog.md or Jira where agents work, and points to statuses "in `AGENTS.md`" (`:26`). Both contradict the brief. Fix it before it becomes the install template. Delete the stale design diagrams (0006).
- G4.11 should: stale docs. `README.md:12` and `AGENTS.md:13` describe the old `.agent-team/skills` install. `README.md:207` says "the workflow exists and works". `optimizations.md` is referenced (`README.md:116`, bot-anatomy) but exists nowhere. AGENTS.md's commands don't say to run them from `tools/at-ticket`.

**Phase 5: playbooks**
- G5.1 must-fix: the retry rule is wrong. Failing checks count as a try in `junior build-ticket:57`, `junior fix-sent-back:59`, `senior build-ticket:57`, `senior fix-sent-back:57` and `take-escalation:61`. Review and QA send-backs don't add a try (`review-change:61`, `verify-ticket:64`), and Senior never blocks after 3 more.
- G5.2 must-fix: Done is set by roles other than Ops (`playbooks/README.md:51`, `groom-board:42, 54`, `handle-escalation:52`). The store allows it (F6).
- G5.3 must-fix: all 57 steps say `Skill: none`, so procedures are pasted inline instead of naming skills.
- G5.4 must-fix: none of the 10 playbooks the brief asks for exist. There's no `evals/`, and no rule to keep skipped steps listed with a reason (silent skips at `triage-request:45`, `release:60`, `improvement-loop:50`).
- G5.5 should: Blocked is used for dependencies and fake tracking tickets (`plan-project:46, 63`). The escalation chain skips the ticket owner (`playbooks/README.md:46`, `take-escalation:65`).

**Phase 6: wake loop**
- G6.1 must-fix: no watcher. `events --since` exists (`cli.go:476-506`) but there's no blocking or follow mode, so a watcher must poll. Expired leases emit no event (F4). The digest and recap commands don't exist.

**Phase 7: installer**
- G7.1 must-fix: no installer, manifest, update or uninstall. `skills/README.md:7-8` and `prompt-contract:13` assume skills live in `.agent-team/skills/`, which Claude Code doesn't discover. The brief wants per-harness generation, hidden via `.git/info/exclude`. `docs/design/install-footprint.mermaid` uses a source layout (`bot/`, `harness/`, `targets/`, `templates/`, `evals/`) the repo doesn't have yet.

**Phase 8+**
- G8.1 later: bots aren't set up, and the active-bets list is empty (needs Paul).
- G9.1 must-fix in Phase 9: `release:39` merges straight to main and pushes, with no PR, merge queue, trust check or rollback plan. That breaks trust level 1 ("PRs only") the moment the bots run, so gate it before Phase 8 dogfooding.
- G10.1 later: `improvement-loop:47` has no evals, baseline or approval-by-risk step.

## Counts

Review findings: 5 must-fix, 4 should, 5 later. Gaps: 18 must-fix, 6 should, 2 later (G9.1 counted as must-fix, see 0012). Total: 23 must-fix, 10 should, 7 later.
