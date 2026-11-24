# Install Maestro

Maestro adds 17 `maestro-*` skills to your repo for Claude Code and Codex: a planner that turns a goal into tickets, engineers that take a ticket to a PR, a reviewer, and QA that tests the PR in your running app and merges it. One script installs them into `.maestro/` and links them for both tools. You commit about 80 files to your default branch. Sessions run with full permissions, and QA merges to your default branch with your `gh` account.

```mermaid
flowchart LR
  G[Your goal] --> P[maestro-planner] --> T[Tickets]
  T --> E[maestro-software-engineer] --> PR[PR]
  PR --> R[maestro-code-review] --> Q[maestro-qa-lead] --> M[Merged]
```

`.maestro/` is the only copy. `.claude/skills/` and `.agents/skills/` hold symlinks into it:

```
your-repo/
├── .maestro/
│   ├── install.sh               # update and uninstall from here
│   ├── manifest                 # every path Maestro placed, with its hash
│   ├── VERSION                  # Maestro commit installed
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
| Node 18+ and `playwright-cli` with Playwright's Chromium | checking anything a user sees |
| A ticket board the agents can reach | planning, claiming and tracking work |
| Linux, macOS or WSL (Windows needs WSL for the symlinks) | the install |

**The board** can be GitHub Issues, Beads, Linear or any tracker the agents can read and write. It needs these statuses: `backlog`, `todo`, `in_progress`, `in_review`, `blocked`, `done`, `cancelled`. It also needs ordered notes on each ticket, and a claim only one agent can hold at a time. [maestro-create-ticket](skills/maestro-create-ticket/SKILL.md) sets the ticket's shape; [maestro-working-principles](skills/maestro-working-principles/SKILL.md) sets claims and notes.

## Install

From anywhere inside your repo:

```bash
(src=$(mktemp -d) && trap 'rm -rf "$src"' EXIT && git clone -q --depth 1 https://github.com/ProlesDei/maestro.git "$src" && bash "$src/install.sh")
```

To pin a version, add `--ref <commit or tag>` after `install.sh`. It fetches that commit and installs it.

The script places only `maestro-*` skills, their links and the files in `.maestro/`, and stages exactly those, even if `.gitignore` ignores `.claude/` or `.agents/`. It changes nothing and exits 1 if:
- a path it would place already exists and it didn't place it;
- `.maestro/`, `.claude/` or `.agents/` (or a `skills/` folder in them) is a symlink;
- a file it placed has changed since the last install, or has staged changes;
- git is busy (`index.lock` exists).

Then add your agent notes (next section) and commit both to your default branch together. Each ticket runs in its own git worktree, which holds only committed files.

## Tell the agents about your project

The skills read your repo's `AGENTS.md` or `CLAUDE.md` for what's specific to your project. Add:

- **The board**: which one, and how agents reach it.
- **Start the app**: the command, its port, and how to run several copies at once without a clash.
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

## Update

On a new branch, run:

```bash
bash .maestro/install.sh
```

It fetches Maestro's latest `main` (or `--ref <commit or tag>`), replaces only what it placed, removes skills Maestro dropped, and stages the change. Open a PR for it like any other change, so sessions working elsewhere never see files mid-swap.

Don't edit files in `.maestro/`: the update stops until you undo the edit. Put your own skills straight in `.claude/skills/` and `.agents/skills/`, named without `maestro-`.

## Uninstall

```bash
bash .maestro/install.sh --uninstall
```

It removes only what it placed, stages the removal, and leaves your own skills alone. Commit to finish.
