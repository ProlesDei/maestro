# Maestro: handoff brief

You're continuing work on **Maestro** in [ProlesDei/maestro](https://github.com/ProlesDei/maestro). This brief captures the current state and every decision made so far, including some that refine or add to what's already merged. Follow it. Where it's silent, use your judgment and log the decision.

## What Maestro is

An installable, harness-agnostic, multi-agent delivery system that takes any software repo from goal to shipped code: a 10x software engineer combined with a 10x product owner. People must be able to pass work through it with confidence, at swarm scale, with hundreds of concurrent sessions.

It has two layers:
1. **Seven role bots in Grok Bot**, each defined by `bot-profiles/<role>/persona.md`: Chief of Staff, Planner, Senior Software Engineer, Junior Software Engineer, Code Reviewer, QA Lead and Operations Manager. The bots live outside the target repo. They think, decide and prompt, and never touch files themselves. **All hands-on work happens in harness sessions** (Claude Code, Codex or Grok Build) running headless inside the target repo.
2. **The in-repo footprint**, installed into a target repo: rules, harness skills, setup files and the ticket engine. To install, Grok Bot reads the Maestro repo and sets up both the footprint and the seven bots.

**Inspiration, never copied:** pstack (Lauren Tan / poteto) for engineering rigour, and firstmate (kunchenguid) for running a crew. Borrow ideas freely, but write all text, skills and code fresh. The same goes for kunchenguid's `treehouse` and `no-mistakes`: study them, don't depend on them, unless the user approves a dependency.

## Current state (Oct 28, 2026)

**Merged on `main`:**
- **PR #1:** `bot-profiles/<role>/persona.md` for all seven roles, plus `_template/` and a README.
- **PR #2:** `docs/bot-anatomy.mermaid`, embedded in the README next to `docs/agent-team.mermaid`.
- **PR #3 (`d29d719`, 46 files):**
  - `skills/`: `at-check-intake`, `at-claim`, `at-wrap-up`, `at-clean-up`, `prompt-contract`, `harness-cli`, `model-selection`, plus a template.
  - `playbooks/`: one folder per role, with 15 playbooks plus a template.
  - `tools/at-ticket/`: the Go ticket CLI. It has 43 tests, and `go test -race` passes, including races between 200 workers and between 200 processes, with no double claims and no lost writes.

**Open gaps:**
- **Pushing is blocked.** The box's GitHub token gets HTTP 403 on the repo, so pushes and PRs go through a Cursor cloud agent. The user needs to provide a token with write access.
- **The local checkout at `/home/box/t3work/maestro` is behind `main`.** The box can't fetch, so it has PR #3's changes on an older base.
- **PR #3 skipped the full ten-pass review.** Run it on `main`.
- **Not built yet:** the installer, the Chief of Staff's wake loop on `at-ticket events`, real setup of the seven bots, and the active-bets list, which is still empty.

**New design files** are in `docs/design/` (added alongside this brief). Read them first:

| File | What it is |
|---|---|
| `bot-anatomy.mermaid` | Latest version of how any bot runs a session. Compare it with `docs/bot-anatomy.mermaid` and keep the newer content |
| `agent-team.mermaid` | Team structure and flow. Compare it with `docs/agent-team.mermaid` the same way |
| `install-footprint.mermaid` | What lands in a target repo, and what's never touched |
| `self-improvement.mermaid` | The improvement loop |
| `project-setup.md` | Per-project setup template, including the definition of done |
| `team-rules-draft.md` | A draft of shared team rules. It predates the bot/harness split, so mine it, don't install it as-is |
| `maestro-usage.py` | A tested prototype for zero-token usage tracking, budgets and circuit breakers |

Where this brief and a design file disagree, this brief wins.

## How you work

Keep the existing working agreement:
- **Coding, audits, research and PR reviews** run on Claude Opus 5.5 at high effort, through T3 Code or the Claude Code CLI, with the repo's `AGENTS.md` loaded first.
- **Split work into small, fast pieces.** Every piece ends with a check you actually ran. "It compiles" or "CI is green" is not evidence: run the command, run the test, read the record back.
- **A merge needs a clean ten-pass PR review and green local tests on the exact head commit.** CI passing alone isn't enough.
- **You have standing permission to merge** once those checks pass.

In addition:
- **Keep a decision log** at `docs/decisions/` with one short entry per decision: context, choice, alternatives and why. Log every judgment call where this brief is silent.
- **Write prose plainly.** Personas, skills and playbooks are read by bots every session, so every unnecessary word costs tokens forever.
- **Dogfood as soon as you can.** Once the footprint installs and the bots are set up, run Maestro's remaining work through Maestro.

**Ask the user only about:** changes to a settled decision below, spending real money, new third-party dependencies, anything irreversible outside the repo, and the items in "Needs the user". For everything else, choose a sensible default, log it and keep going. Batch questions into one message.

## Needs the user
1. A GitHub token with write access to ProlesDei/maestro, to unblock pushes, fetches and PRs.
2. Approval of the rename to Maestro names (see Naming). It's recommended, and cheapest now.
3. The first entries for the active-bets list.

## Settled decisions

### Bots and harnesses
- **Bots prompt, harnesses work.** A bot never edits files. Like firstmate's first mate, the Chief of Staff is read-only over the target project: every change goes through a harness session.
- **Two kinds of skills, kept apart.**
  - **Bot skills** (how to plan, prompt and judge) go to the bot runtime: `prompt-contract`, `harness-cli` and `model-selection` today.
  - **Harness skills** (how to do a step) get installed into the target repo: `at-check-intake`, `at-claim`, `at-wrap-up` and `at-clean-up` today.
  - `skills/` currently mixes them. Split them into `bot-skills/` and `harness-skills/` (or equivalent), and mark internal skills so third-party skill installers don't pick them up, as firstmate does with an internal metadata flag.
- **Missing harness skills:** handoff note, log entry, propose memory, escalate and verify.
- **Prompt contract.** Every prompt states the goal, where to look (pointers, not pasted content), constraints and permissions, what done means, and the reply format: short, structured, with evidence. Use the harness's structured-output mode where it has one. Prompts name harness skills instead of spelling out procedures.
- **Sessions.** Reuse one harness session across a playbook's steps, and start a fresh session per ticket. Put stable content first in prompts so it gets cached. Pick the model per step: mechanical steps use a cheap model or a script.
- **Personas must match this model.** Audit every `persona.md`: none should tell a bot to read code or run commands itself. They should say what to ask the harness for and how to judge the reply.

### Scale: hierarchy and pools
- **Goal → Workstream → Ticket**, mapping to Jira Initiative → Epic → Story. The active-bets list is the Goal level, owned by the Chief of Staff.
- **The Chief of Staff manages goals and workstreams, never individual tickets.** Each workstream gets its own Planner session.
- **Pools are parallel sessions** of the same role bot, which the per-session leases already support. Pool sizes come from the work, the budget and the trust level.
- **Each layer reads only summaries** from the layer below. Workstream summaries are computed from ticket events by a script, and the Planner adds only judgment: risks and what's next.

### Tickets
- **Tickets are the unit of work**: claimed, leased, built, reviewed, tested and shipped.
- **Two task shapes**, as in firstmate: **ship** tickets (feature, bug, chore, docs) deliver changes, and **scout** tickets (investigations) end at an evidenced report, with no review, QA or release. Lifecycles per type come from the definition of done.
- **Not everything is a ticket.** Playbook steps stay inside a ticket, and parallel fan-outs are short-lived sub-tasks inside one session.
- **Statuses:** Backlog, To do, In progress, Blocked, In review, In QA, Ready to release, Done. Only the Operations Manager sets Done, after release.
- **Retries:** send-backs from review or QA add one to Tries. Junior escalates to Senior after 2. Senior sets Blocked after 3 more. Failed automated checks don't count as tries.
- **Escalation chain:** session → ticket owner → Chief of Staff → user. Only the Chief of Staff talks to the user.

### Ticket engine: harden `at-ticket`
Option B2 is settled: SQLite in WAL mode, behind `ticket.Store`, with Postgres deferred until multi-host. Check each of the following against the current code, and add whatever's missing:
- **Heartbeats come from a background process** started by the claim, never from LLM turns. A single harness step can outlast a lease.
- **Fencing tokens.** Each claim gets a lease number that increases on every re-claim, and every write to a claimed ticket must include it, so late writes from a session that lost its lease are rejected. Version checks cover transitions, but not comments or notes.
- **Area locks.** Tickets declare the code areas they touch, and ClaimNext never runs overlapping tickets at the same time.
- **Dependencies.** ClaimNext only returns tickets whose dependencies are Done.
- **A lease reaper** that frees expired leases, logs an event, and counts how often each ticket's lease expires.
- **Workstreams and budgets** in the store, and **schema migrations**.
- **SQLite settings:** busy timeout, `BEGIN IMMEDIATE` for every write, tiny transactions, regular checkpoints, and the database's clock for lease expiry.
- **Backups:** an export and snapshot command, run on a schedule. The store is local-only.
- **Environment detection:** refuse or fall back on network filesystems, unreliable container file locking, submodules where `.git` is a file, and bare repos.
- **Render `Backlog.md` in one place only.** It must never be committed on agent branches, or hundreds of branches will conflict on it.
- **More race tests:** processes killed mid-transaction, a lease expiring while a heartbeat arrives, and writes after a lease was re-claimed.
- **External ticket tools are mirrors.** Jira, GitHub Issues, Linear or Backlog.md are synced views for humans, never where agents coordinate.

### Cost: zero-token checks only
- **Port `docs/design/maestro-usage.py` to Go** under `tools/`, behind the store interface. It's a tested reference: parsers for Claude Code JSON output (prefer `modelUsage`, which includes subagents) and Codex `exec --json` (sum every `turn.completed`, priced from a table). Add the Grok Build parser.
- **Wrap every harness call**, so usage gets recorded automatically. Keep it **one row per invocation, not per session**, since resumed sessions reuse their ID.
- **`check`** gates budgets (exit 0 ok, 1 warn, 2 over) per ticket, workstream, role and globally, in both dollars and tokens, since subscription plans don't bill per token. **`breaker`** pauses a role pool when its failure rate spikes. Pass the remaining budget as a hard cap where the harness supports one.

### The Chief of Staff's wake loop
- **Use a zero-token watcher**, like firstmate's: a small process that sleeps on `at-ticket events --since <seq>` and wakes a bot only when an event needs it.
- **Wake through Grok Bot's own mechanism.** Verify which ones exist, for example webhooks.
- **No turn ends blind.** While work is under way, a bot's turn must not end without supervision being live, as in firstmate's turn-end guard.
- **Restart-proof.** All state lives on disk and in the store, and after a restart the Chief of Staff reconciles from them.
- **User-facing commands for the Chief of Staff:** a short digest of goals, workstreams and spending, and a recap of what happened since the user last spoke, walking through open decisions one at a time.

### Shipping and safety
- **Ships autonomously within trust levels** by default. Trust levels, per repo and per risk class, are earned from evidence and lost automatically on a rollback or escaped bug:
  1. **Propose:** PRs only. No merging, deploying or deleting. Every install starts here.
  2. **Merge low-risk:** docs, tests and chores.
  3. **Merge most:** everything except high-risk classes.
  4. **Ship:** merge and deploy. Migrations, auth and payments still need a human unless the user explicitly allows them.
- **Only the user changes trust levels.** Concurrency limits are tied to trust level too: new repos start with few sessions and earn more.
- **Gates for every autonomous ship:** the project's definition of done, verification evidence from the real app, a multi-model review panel, a passing merge queue, and a rollback plan. Ship progressively where the project supports it.
- **Merge queue:** land one change at a time on the latest main, re-running checks each time. Each writer gets its own worktree.
- **Definition of done** is set per project at setup, drafted from the repo (CI config, PR template, CONTRIBUTING, branch protection, coverage thresholds) and confirmed by the user. It varies by ticket type, and every item says how it's checked: command, evidence or judgment. Maestro's floor for autonomous shipping is verification evidence, a passing review and a passing merge queue. Projects can raise the bar, not lower it. Bots can propose raising it, and only the user can lower it.
- **Code hosts:** build GitHub first, but keep it behind an interface. firstmate shows GitLab, Gerrit and Forgejo users exist.
- **Kill switch:** a stop file that pauses every bot at its next step.
- **Preflight** before every session: kill switch, health check (harness logged in, store reachable, clean git), environment and trust level.
- **Secrets** never appear in prompts, replies or logs.

### Memory and self-improvement
- **Four memory tiers:** working (scratchpad, deleted at clean-up), ticket (archived with the ticket), project (decisions, conventions, pitfalls, codebase map) and role (lessons). Bots propose memories into an inbox, and the Operations Manager curates them. Memories record which files they're about and get flagged when those change. They decay and are archived under a size budget, as firstmate's `/stow` does. Harness auto-memory is disabled or pointed at the curated memory.
- **The improvement loop:** signals (logs, one-line retros, user corrections, failure reasons) → patterns, never one-offs → one change → test on golden-task evals → adopt as a commit → measure against a baseline → keep or revert.
- **Approval by risk:** tuning (Operations Manager), skill or prompt (trial), playbook (plus Chief of Staff), persona or authority (user). The Operations Manager can never edit its own guardrails.
- **Self-repo boundary.** Lessons that would change Maestro itself are proposed only, never pushed or PRed upstream from a user's install, unless the maintainer opts in. firstmate has the same rule.

### Install: never overstep
- **Grok Bot drives the install, and a deterministic CLI does the file work.** Judgment steps (onboarding, drafting `project-setup.md` and the definition of done, setting up the seven bots) follow an install playbook. All file creation goes through an installer command that refuses to overwrite and records a manifest.
- **The footprint:** everything real lives in one namespaced folder: read-only rules, `project/` (setup and memory, committed), `local/` (environment, logs, scratchpads, worktrees, ignored by its own `.gitignore`), and `manifest.json` listing every file Maestro created, with checksums. Harness skills are generated per installed harness into that harness's skill folder (`.claude/skills/`, `.agents/skills/`, Grok's path once verified), hidden from `git status` via `.git/info/exclude`, set to trigger only when named, or shipped as a plugin where the harness has a plugin system.
- **Never edit** the user's `AGENTS.md`, `CLAUDE.md`, harness settings, root `.gitignore`, ticket workflow or branches. Pass rules, permissions and model per session via launch flags, so the user's own harness sessions never see Maestro's rules.
- **Backlog.md mirror:** choose Skip for its AI setup, since its CLI and MCP options write instruction files into the repo. Add its folder to the manifest.
- **Update** replaces rules and generated skills only. **Uninstall** removes exactly what the manifest lists, including the ticket store in the git directory, asks before deleting edited files, and offers to keep project memory.

### Naming
Rename `at-`, `.agent-team/`, `AT_TICKET_STORE` and the `agent-team` store path to Maestro names (`maestro-`, `.maestro/`, `MAESTRO_TICKET_STORE`, `…/maestro/tickets.db`) before anything is installed anywhere. Nothing is installed yet, so no compatibility layer is needed. **Wait for the user's approval** (Needs the user, item 2) before starting.

## Facts to verify before relying on them
- Grok Build's skill folder path, headless flags, permission flags and JSON output format. firstmate supports Grok as a primary harness and has Grok hooks and a `GROK_BOT.md`, which may answer some of this.
- Grok Bot's wake mechanisms.
- Each harness's manual-only skill setting, and its flags for passing instructions, permissions, model and budget caps per session.
- Whether Backlog.md can keep its folder inside the Maestro folder.

## Phases

Each phase ends with its done criteria met and verified.

**Phase 0: Sync and audit.**
- Once the user provides the token, bring the local checkout up to date with `main`.
- Run the ten-pass review on `main` that PR #3 skipped.
- Audit the repo against this brief, and log every conflict and gap.

*Done when:* the checkout matches `main`, the review findings are filed as tickets or fixed, and the gap list is in the decision log.

**Phase 1: Rename** (after approval).
*Done when:* nothing references the old names, and all tests pass.

**Phase 2: Harden `at-ticket`.**
*Done when:* every item under "Ticket engine" exists with tests, and the new race tests pass under `-race`.

**Phase 3: Cost.**
*Done when:* real headless runs on each available harness record correctly, and `check` and `breaker` gate a session in a test.

Phases 2 and 3 can run in parallel.

**Phase 4: Rules, skills and personas.**
- Split bot rules from harness rules, and bot skills from harness skills.
- Add the missing harness skills.
- Bring every persona in line with "bots prompt, harnesses work".
- Add memory, trust levels, the kill switch and preflight.

*Done when:* no persona tells a bot to touch files, and every rule lives in exactly one file.

**Phase 5: Playbooks.**
Audit the 15 existing playbooks, then add what's missing:
- **Engineering:** bug fix (reproduce first), refactor (capture existing behaviour first), performance (trace before and after), and scout investigation.
- **Onboarding:** setup, the definition of done, and a project-local verification skill.
- **Upkeep:** memory curation and housekeeping.
- **The Chief of Staff's** digest and recap.

Each playbook is followed step by step, with skipped steps kept on the list along with the reason.

*Done when:* every playbook has a golden task in `evals/` that passes.

**Phase 6: Wake loop.**
*Done when:* a ticket moving through every status wakes exactly the right bot each time with zero tokens spent in between, and a forced restart reconciles cleanly.

**Phase 7: Installer.**
*Done when:* installing into a sample repo that already has its own `AGENTS.md`, `CLAUDE.md` and harness settings leaves them byte-for-byte unchanged, and uninstalling restores the repo exactly, apart from anything the user chose to keep.

**Phase 8: Set up the seven bots and dogfood.**
- Set up the seven bots in Grok Bot from `bot-profiles/`.
- Fill the active-bets list with the user.
- Install Maestro into a sample repo at trust level 1, then run a real goal through it.

*Done when:* the goal produces reviewed PRs with evidence on every ticket.

**Phase 9: Trust levels, gates and merge queue.**
*Done when:* a change is blocked at each gate in a test, and a simulated rollback drops the trust level automatically.

**Phase 10: Self-improvement.**
*Done when:* a seeded pattern in the logs produces one proposed change, which is tested on evals and then adopted or reverted with a logged reason.

**Phase 11: Swarm proof.**
Raise concurrency step by step on the sample repo.
*Done when:* a goal ships at trust level 2 with no double claims, usage stays within budget, and the uninstall leaves no trace.

## Reporting

At the end of each phase, send one short message:
- **What was done:** a few lines.
- **Evidence:** the commands you ran, with their results.
- **Decisions logged.**
- **Anything blocked, or needing the user.**
- **Next phase.**
