# Install Maestro

Maestro adds 17 `maestro-*` skills to your repo for Claude Code and Codex: a planner that turns a goal into tickets, engineers that take a ticket to a PR, a reviewer, and QA that tests the PR in your running app and merges it. One script installs them into `.maestro/` and links them for both tools. You commit about 80 files to your default branch. Sessions run with full permissions, and QA merges to your default branch with your `gh` account.

```mermaid
flowchart LR
  G[Your goal] --> P[maestro-planner] --> T[Tickets]
  T --> E[maestro-software-engineer] --> PR[PR]
  PR --> R[maestro-code-review] --> Q[maestro-qa-lead] --> M[Merged]
```

`.maestro/` is the only copy. `.claude/skills/` and `.agents/skills/` hold symlinks into it. Your own files in `.maestro/`, such as mobile.dev Maestro flows, stay yours (if your mobile.dev config picks up flows from every subfolder, leave out `skills/`: its YAML files aren't flows):

```
your-repo/
├── .maestro/
│   ├── install.sh               # update and uninstall from here
│   ├── manifest                 # every path Maestro placed, with its hash
│   ├── VERSION                  # Maestro commit installed
│   ├── .gitattributes           # keeps line endings as shipped
│   └── skills/maestro-*/        # SKILL.md, references/, scripts/
├── .claude/skills/maestro-*  -> ../../.maestro/skills/maestro-*   (Claude Code)
└── .agents/skills/maestro-*  -> ../../.maestro/skills/maestro-*   (Codex)
```

## What you need

| Need | For |
|---|---|
| Bash 4.4+ (macOS ships 3.2: `brew install bash`), Git 2.38+, jq 1.5+ | the skills' scripts |
| `gh`, signed in to an account that can push, comment on PRs and merge | engineers open PRs; review and QA comment; QA merges |
| A GitHub `origin`; PRs from branches in the same repo into its default branch; branch rules that allow a merge commit without a merge queue | review and QA refuse anything else |
| Branch rules that don't require an approving review or an up-to-date branch | verdicts are PR comments, and every session posts as one account |
| Required status checks green on the PR's head before QA merges | QA merges without admin rights, so a red check stops the merge |
| Node 18+ and `playwright-cli` with Playwright's Chromium | checking anything a user sees |
| A ticket board the agents can reach | planning, claiming and tracking work |
| Linux, macOS or WSL (Windows needs WSL for the symlinks), and git's `core.symlinks` not set to false | the install, and the links in every worktree |

**The board** can be GitHub Issues, Beads, Linear or any tracker the agents can read and write. It needs these statuses: `backlog`, `todo`, `in_progress`, `in_review`, `blocked`, `done`, `cancelled`. It also needs ordered notes on each ticket, a claim only one agent can hold at a time, and these fields: deps, areas, priority, goal and a parked reason for `backlog`, plus type (`ship` or `scout`), tier and route (`review` or `qa`), which the planner sets. GitHub Issues and Linear have no claim only one agent can hold, and every session posts as one account, so your agent notes must name one: a dispatcher that hands each ticket to one session, say, or a claim tool. A board kept in the repo's tracked files can't hold a claim across worktrees. [maestro-create-ticket](skills/maestro-create-ticket/SKILL.md) sets the ticket's shape; [maestro-working-principles](skills/maestro-working-principles/SKILL.md) sets claims and notes.

**Running the sessions** is yours: a dispatcher, a scheduler or a person. Apart from `maestro-swarm` for its own workers, Maestro's skills never start a session or free another's claim. Your outer layer:

- frees a claim once the session that held it ended, and at once a claim on a `blocked` ticket whose latest comment is an `Escalation:`, unless a `planner/` holder holds it;
- runs each `Next:` line a reply gives, as written, as a new session's first message (`/<line>` in Claude Code, `$<line>` in Codex), since some carry flags such as `--approved <H>` (`after <time>` gives a UTC time such as `2026-11-25T16:20Z`: start it no sooner, without that part); a `maestro-chief-of-staff <id>` line runs naming each card still waiting on the owner, as the `blocked` row says;
- runs one `maestro-chief-of-staff` at a time: ids and `up:` lines that come up while one runs wait, then go to the next run together, naming each card still waiting, so one cause gets one card;
- sends a reply's `up:` lines together, with the call that gave them, to one `maestro-chief-of-staff` run, starting one once none is going and naming each card still waiting, then again with each next run, starting one for them as soon as no card waits on the owner, until a card, a `joins` line or a Send line carries that call, and each `Send:` line of a chief-of-staff run you started to whom it names;
- shows each owner's card to the owner. When the owner picks an option, runs its decision for each id on the card's `Tickets` (the owner's own words as `maestro-planner rule <id>: <answer>` if no option fits). For a plan that created no ticket, runs the picked option's plan call, or reruns the latest call for that goal with `; owner ruled: <answer>` added. A `joins <What>` reply adds its tickets and plan calls to that card: on the pick, the decision runs for each ticket, with its own picks added, and each plan call reruns with `; owner ruled: <answer>`. Drops a card once the owner's pick has run for it, or, for a card that lists tickets, once every one has left `blocked`;
- reruns a run after 15 minutes when it left its ticket's status and notes as they were, with no reply, no `Next:` line or `Next: none`, and the ticket is still unclaimed (a full disk, GitHub down, a stopped ruling), and tells the owner after 3 tries. A chief-of-staff run, a run whose reply has an `up:` line, and a planner job that only reads (a draft, Check, Board or Answer) never count: they change nothing by design or wait on the owner. Nor does a run with no ticket, such as a goal's plan;
- after each run ends, and every 15 minutes, starts from this table each unclaimed ticket that no run you started is still working on and no `Next:` line holds for later, such as one a `Next: none`, a ruling or a `Start now:` line leaves, unless its own last run left it as it was (the rule above applies):

| Ticket | Start |
|---|---|
| `todo` with every dep `done` and no area shared with a claimed ticket, or `in_progress` with no claim | `maestro-software-engineer <id>` on the model its `tier` names, or `maestro-designer design <id>` for a `Design:` scout |
| `in_review` with route `qa`, or whose latest `Review: GO at` names the PR's head with no `Rereview:` after it | `maestro-qa-lead <id>` |
| any other `in_review` | `maestro-code-review <id>` |
| `blocked`, once per `Escalation:` (a run counts once a card, a `joins` line, a Send line or an Answer names the ticket), unless a card waiting on the owner names it | `maestro-chief-of-staff <id>`, naming each card still waiting on the owner, so one cause joins its card |

## Install

From anywhere inside your repo:

```bash
(src=$(mktemp -d) && trap 'rm -rf "$src"' EXIT && git clone -q --depth 1 https://github.com/ProlesDei/maestro.git "$src" && bash "$src/install.sh")
```

To pin a version, add `--ref <tag, branch or full 40-character sha>` after `install.sh`.

The script places only `maestro-*` skills, their links and its own files in `.maestro/`, and stages exactly those, even if `.gitignore` ignores `.claude/` or `.agents/`. It changes nothing and exits 1 if:
- a path it would place already exists and it didn't place it;
- `.maestro/`, `.claude/` or `.agents/` (or a `skills/` folder in them) is a symlink, except one `skills/` folder linked to the other;
- a file it placed has changed since the last install, or has staged changes;
- `.maestro/manifest` comes from a newer Maestro, or names a path outside `.maestro/`, `.claude/skills/maestro-*` and `.agents/skills/maestro-*`;
- git's `core.symlinks` is false, or a sparse checkout leaves out `.maestro/`, `.claude/` or `.agents/`;
- git is busy (`index.lock` exists).

If it fails or you stop it (Ctrl-C) partway, it puts every file back as it was.

Then add your agent notes (next section) and commit both to your default branch together. First exclude `.maestro/`, `.claude/skills/maestro-*` and `.agents/skills/maestro-*` from formatters and pre-commit hooks: prettier and the like rewrite the skills' tables, and every later update then refuses. Each ticket runs in its own git worktree, which holds only committed files.

## Tell the agents about your project

The skills read your repo's `AGENTS.md` or `CLAUDE.md` for what's specific to your project. Add:

- **The board**: which one, how agents reach it, and how they claim and release a ticket, and where each status and field lives (say, a `status:in_review` label or a `Route:` line in the body).
- **Start the app**: the command, its port, where a worktree gets its env file and its own database, and how to run several copies at once without a clash; or that there is no app, and which commands show a change works.
- **Regenerable files**: ignored files a finished worktree may lose, such as build outputs and copied env files.
- **Harness branch prefixes**: the branch prefixes your tools give the worktrees they make, such as `claude/`, `codex/` or `t3/`, so `maestro-prune` cleans merged ones.
- **Outside steps**: anything a person must do first, such as a VPN, a login or a feature flag.
- **Sensitive paths**: code that needs a closer look, such as auth, billing or migrations.
- **Critical flows**: the user journeys QA always checks.
- **Browser tool**, if not `playwright-cli`.
- **Who proves the work**, if engineers prove their own instead of QA.

## Give sessions full permissions

Maestro's sessions never stop to ask. In T3, set each session to Full access. Outside T3, each person sets it once:

| Tool | Setting |
|---|---|
| Claude Code | `"permissions": {"defaultMode": "bypassPermissions"}` in `~/.claude/settings.json`, or `--dangerously-skip-permissions`. A repo's `.claude/settings.json` can't turn it on. |
| Codex | `approval_policy = "never"` and `sandbox_mode = "danger-full-access"` in `~/.codex/config.toml`. Both are needed. |

## Run it

Call each skill by name: `/maestro-planner` in Claude Code, `$maestro-planner` in Codex.

1. `maestro-planner <goal>`: writes ordered tickets on the board and names the ones that can start now.
2. `maestro-software-engineer <ticket id>`: claims the ticket, builds it in its own worktree, opens a PR and moves the ticket to `in_review`. `maestro-swarm` runs several at once.
3. `maestro-code-review <ticket id>`: reviews the PR in a fresh session and posts its verdict.
4. `maestro-qa-lead <ticket id>`: tests the PR in the running app, merges it if it passes and marks the ticket `done`.

`maestro-chief-of-staff` checks facts and weighs calls for you along the way; it doesn't start work. [skills/README.md](skills/README.md) lists every skill.

Merged worktrees, branches and QA evidence pile up across a swarm. Run `maestro-prune --safe-only` on a schedule, daily say, from your own scheduler or by hand: it removes only what is merged and idle. Run `maestro-planner board` on the same schedule: it lifts holds whose fact came true and finishes splits and drops that waited on another session.

## Update

On a new branch, run:

```bash
bash .maestro/install.sh
```

It fetches Maestro's latest `main` (or `--ref`), replaces only what it placed, removes what Maestro dropped, and stages the change. Open a PR for it like any other change, so sessions working elsewhere never see files mid-swap. If `.maestro/install.sh` ever breaks, the install one-liner does the same update with the newest installer.

Updating past the version that made the planner a product owner? Your outer layer now runs the sessions as **Running the sessions** says: it frees claims, including the ones older engineers kept on escalated tickets, and schedules `maestro-prune --safe-only` and `maestro-planner board`.

Don't edit the files Maestro placed (`.maestro/manifest` lists them): the update stops until you undo the edit. Keep them out of formatters and pre-commit hooks, as Install says. Put your own skills straight in `.claude/skills/` and `.agents/skills/`, named without `maestro-`.

## Uninstall

```bash
bash .maestro/install.sh --uninstall
```

It removes only what it placed, stages the removal, and leaves your own skills and files alone. Commit to finish.
