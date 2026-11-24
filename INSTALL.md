# Install Maestro

An agent follows these steps at the root of the team's primary checkout, with a Maestro checkout at hand. The same steps install and update.

## 1. Place Maestro

1. Before writing, find a remote that can take PRs and its default branch. If you can't find both, stop and ask the owner. Resolve `.maestro`, `.agents`, `.claude`, `AGENTS.md`, `CLAUDE.md`, and every other path the install writes or removes in the checkout, including their parents. If any resolve outside the checkout, stop and ask the owner. The board and QA run folder can live outside it. Check the files and skill links you would replace or remove. Stop if Maestro did not place one. Also stop if the root `AGENTS.md` has a `## Maestro principles` section outside the markers.
2. On an update, end every Maestro session first, and clean each ended run with the old `.maestro/maestro-tmp` if it is there. Copy the checkout's `maestro/` files into `.maestro/`. Keep existing memory and any old board. Remove files Maestro placed and no longer ships.
3. For each folder in `.maestro/skills/`, make sure `.agents/skills/` and `.claude/skills/` each hold a relative symlink `maestro-<folder>` that points to it.
4. Put one line at the end of the root `AGENTS.md` between `<!-- maestro:start -->` and `<!-- maestro:end -->`: “Maestro roles read `.maestro/principles.md` when they start.” Create the file if needed. On an update, replace only what sits between the markers.
5. Add `@AGENTS.md` to the root `CLAUDE.md` if missing. Create the file if needed.

## 2. Set up QA

If QA memory lacks a verified start, read the README, package scripts, Makefile, compose files and docs. Draft steps to start the app with fresh test data. Ask the owner only to confirm the steps or fill gaps.

Put `bash`, `git`, `t3`, `ss`, `setsid`, `flock` and `jq` on the install session's `PATH`. Follow the test run and [Worktrees](maestro/principles.md#worktrees) rules. Prove one start in a worktree inside the run folder on a free port. Check that the app answers. If the start fails, report what blocks QA.

If users see the app in a browser, open it headless while it runs, using the tool and session setup from [QA's browser guide](maestro/skills/qa/browser.md). Take one screenshot and look at it, then close only your browser session. If no browser works, report what's missing and that QA will block screen checks until it's fixed.

Record the run steps, readiness check, browser tool that worked and any secret names under “How to run the app” in `.maestro/memory/qa.md`. If the repo has no app, record that.

## 3. Set up the ticket board

Put `bd` 1.3.4 and Dolt 2.2.0 on the install session's `PATH`. Verify the `bd` and Dolt downloads against their release checksums.

The chief of staff restarts the planners after the update merges. Setup adds a second QA slot and keeps any existing hold. Run `.maestro/maestro-bd maestro-setup`. On a fresh install, append a free port if 3307 is taken. The board binds to loopback, so sessions run on the board's machine. Its folder under `~/.local/share/maestro-board/` is named `<repo>-<hash>`, with the first 12 SHA-256 digits of the primary checkout's physical path.

To move an old board, stop its Dolt server and move `.maestro/maestro-board/` by hand to that folder, and if `.beads/dolt-server.port` is missing, write the old port there before running setup.

If the first setup stops partway, stop any board server it left running, move the board folder aside and run setup again. After a reboot or stop, the launcher starts Dolt on the saved port. If the port is taken, run setup with a free one. Rerun setup if `bd` moves. To move the primary checkout, stop Dolt, move the checkout, repair its worktrees, then move the board folder to the new path's name before running setup.

## 4. Connect the code graph and commit

Put `codebase-memory-mcp` 0.12.1 or later on T3's `PATH`. If you need the binary, download the archive for your platform and verify it against the release's `checksums.txt` before extracting it. [Download v0.12.1](https://github.com/DeusData/codebase-memory-mcp/releases/tag/v0.12.1)

**Codex** — Add this entry to the project's `.codex/config.toml`.

```toml
[mcp_servers.codebase-memory-mcp]
command = "codebase-memory-mcp"
```

**Claude Code** — Register the server in the project (`.mcp.json`).

```sh
claude mcp add --scope project --transport stdio codebase-memory-mcp -- codebase-memory-mcp
```

Keep the rest of each config. Keep a matching entry. If an entry uses the same name with different settings, ask the owner before changing it. Commit the Maestro install files, QA memory and MCP config so new worktrees get them.

Check that the install commit's tree contains every install file and skill link. If Git ignores any, stop and ask the owner. Open an install PR to the remote's default branch from step 1. Post verdicts as plain PR comments and wait for required checks to pass. Use this session's account and QA's merge method. Merge directly with the install PR's head SHA as the guard. If the host refuses or only offers a merge queue, stop and ask the owner.

## 5. Verify in T3

Create a fresh linked worktree from the remote's default branch from step 1, as T3 does for tickets. Use `.maestro/maestro-bd` there to list ready, unassigned tickets as JSON. Expect an array, empty on a new board.

Use `.maestro/maestro-bd` to create a throwaway ticket on the board. Claim it atomically as one actor. Check that a second actor gets an already-claimed error. Close it with an update guarded by the first actor as assignee and `in_progress` status, then delete it. Stop if any check fails.

Open a fresh session there. Trust the project in Codex or approve the project MCP server in Claude Code if it is pending. Invoke `$maestro-chief-of-staff` in Codex or `/maestro-chief-of-staff` in Claude Code. Replace `<symbol>` with a function or class you know.

> Index this checkout with `index_repository`. Use `search_graph` to find `<symbol>` and show its file path.

Check that the path matches the source file.
