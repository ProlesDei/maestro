# Install Maestro

An agent follows these steps at the root of the team's primary checkout, with a Maestro checkout at hand. The same steps install and update.

## 1. Place Maestro

1. Before writing, resolve `.maestro`, `.agents`, `.claude`, `AGENTS.md`, `CLAUDE.md`, and every other path the install writes in the checkout, including their parents. The board under `~/.local/share/maestro-board/` is the one write outside it. If any resolve outside the checkout, stop and ask the owner. Check the files and skill links you would replace. Stop if Maestro did not place one. Also stop if the root `AGENTS.md` has a `## Maestro principles` section outside the markers.
2. Copy the checkout's `maestro/` files into `.maestro/`. On an update, keep existing memory and any old board.
3. For each folder in `.maestro/skills/`, make sure `.agents/skills/` and `.claude/skills/` each hold a relative symlink `maestro-<folder>` that points to it.
4. Put the text of the checkout's [principles.md](principles.md) at the end of the root `AGENTS.md`, between the lines `<!-- maestro:start -->` and `<!-- maestro:end -->`. Create the file if needed. On an update, replace only what sits between the markers.
5. Add `@AGENTS.md` to the root `CLAUDE.md` if missing. Create the file if needed.

## 2. Set up QA

If QA memory lacks a verified start, read the README, package scripts, Makefile, compose files and docs. Draft steps to start the app with fresh test data. Ask the owner only to confirm the steps or fill gaps.

Follow the test run rule in [principles.md](principles.md) and prove one start in a scratch worktree on a free port. Check that the app answers, then stop what you started. Record the run steps, readiness check and any secret names under “How to run the app” in `.maestro/memory/qa.md`. If the repo has no app, record that. If the start fails, report what blocks QA.

## 3. Set up the ticket board

Put `bd` 1.3.4, Dolt 2.2.0, and Linux `ss` and `setsid` on the install session's `PATH`. Verify the `bd` and Dolt downloads against their release checksums.

On an update, wait until no QA session is live before running setup, since setup adds the QA slot that later planners claim. Run `.maestro/maestro-bd maestro-setup`. On a fresh install, append a free port if 3307 is taken. The board binds to loopback, so sessions run on the board's machine. Its folder under `~/.local/share/maestro-board/` is named `<repo>-<hash>`, with the first 12 SHA-256 digits of the primary checkout's physical path.

To move an old board, stop its Dolt server and move `.maestro/maestro-board/` by hand to that folder, and if `.beads/dolt-server.port` is missing, write the old port there before running setup.

Run setup once at a time per board. If the first setup stops partway, stop any board server it left running, move the board folder aside and run setup again. Run setup again after a reboot or stop, or after `bd` moves. If its port is taken, pass a free one. To move the primary checkout, stop Dolt, move the checkout, repair its worktrees, then move the board folder to the new path's name before running setup.

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

Check that the install commit's tree contains every install file and skill link. If Git ignores any, stop and ask the owner. Open an install PR to the default branch. Post verdicts as plain PR comments, wait for required checks to pass, and merge with this session's account and the merge method QA will use. If the host refuses, stop and ask the owner. A successful merge proves QA's merge path.

## 5. Verify in T3

Create a fresh linked worktree from the remote default branch, as T3 does for tickets. Use `.maestro/maestro-bd` there to list ready, unassigned tickets as JSON. Expect an array, empty on a new board.

Use `.maestro/maestro-bd` to create a throwaway ticket on the board. Claim it atomically as one actor. Check that a second actor gets an already-claimed error. Close it with an update guarded by the first actor as assignee and `in_progress` status, then delete it. Stop if any check fails.

Open a fresh session there. Trust the project in Codex or approve the project MCP server in Claude Code if it is pending. Invoke `$maestro-chief-of-staff` in Codex or `/maestro-chief-of-staff` in Claude Code. Replace `<symbol>` with a function or class you know.

> Index this checkout with `index_repository`. Use `search_graph` to find `<symbol>` and show its file path.

Check that the path matches the source file.
