# Install Maestro

An agent follows these steps at the root of the team's repo, with a Maestro checkout at hand. The same steps install and update.

## 1. Place Maestro

1. Before you write anything, check every path below. If one holds something Maestro didn't place, or `AGENTS.md` has a `## Maestro principles` section outside the markers, stop and report it.
2. Copy the checkout's `maestro/` folder to `.maestro/` in the repo. On an update, replace everything in `.maestro/` except `memory.md`.
3. For each folder in `.maestro/skills/`, make sure `.agents/skills/` and `.claude/skills/` each hold a relative symlink `maestro-<folder>` that points to it.
4. Put the text of the checkout's [principles.md](principles.md) at the end of the root `AGENTS.md`, between the lines `<!-- maestro:start -->` and `<!-- maestro:end -->`. Create the file if needed. On an update, replace only what sits between the markers.
5. Make sure the root `CLAUDE.md` imports `@AGENTS.md`. Create the file if needed.
6. Commit what you placed, so every worktree gets it.

## 2. Connect the code graph

Put the `codebase-memory-mcp` executable on T3's `PATH`. If you need the binary, download the archive for your platform and verify it against the release's `checksums.txt` before extracting it. [Download v0.12.1](https://github.com/DeusData/codebase-memory-mcp/releases/tag/v0.12.1)

**Codex** — Add this entry to the trusted project's `.codex/config.toml`.

```toml
[mcp_servers.codebase-memory-mcp]
command = "codebase-memory-mcp"
```

**Claude Code** — Register the server in the project.

```sh
claude mcp add --scope project --transport stdio codebase-memory-mcp -- codebase-memory-mcp
```

## 3. Verify in T3

Open a fresh session in a new worktree of the repo. Invoke `$maestro-chief-of-staff` in Codex or `/maestro-chief-of-staff` in Claude Code. Replace `<symbol>` with a function or class you know.

> Index this checkout with `index_repository`. Use `search_graph` to find `<symbol>` and show its file path.

Check that the path matches the source file.
