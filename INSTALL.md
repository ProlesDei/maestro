# Install Maestro

An agent follows these steps at the root of the team's repo, with a Maestro checkout at hand. The same steps install and update.

## 1. Place Maestro

1. Before you write anything, check every path below. If one holds something Maestro didn't place, or `AGENTS.md` has a `## Maestro principles` section outside the markers, stop and report it.
2. Copy the checkout's `maestro/` folder to `.maestro/` in the repo. On an update, replace everything in `.maestro/` except `memory/` and `maestro-board/`.
3. For each folder in `.maestro/skills/`, make sure `.agents/skills/` and `.claude/skills/` each hold a relative symlink `maestro-<folder>` that points to it.
4. Put the text of the checkout's [principles.md](principles.md) at the end of the root `AGENTS.md`, between the lines `<!-- maestro:start -->` and `<!-- maestro:end -->`. Create the file if needed. On an update, replace only what sits between the markers.
5. Make sure the root `CLAUDE.md` imports `@AGENTS.md`. Create the file if needed.
6. Commit what you placed, so every worktree gets it. `.maestro/.gitignore` keeps the board's config and data out of Git.

## 2. Set up the ticket board

The board is Beads on one Dolt server on the owner's machine. Every session reaches it through `.maestro/maestro-bd`, which finds the board in the primary checkout's `.maestro/maestro-board/`. Put `bd` 1.3.4 and Dolt 2.2.0 on T3's `PATH`; verify each download against its release checksums.

Start the server from the primary checkout. Run this again after a reboot or stop; on an update, leave a running server alone. It binds to loopback only and logs to `.maestro/maestro-board/server.log`. If port 3307 is taken, pick a free one and use it in both blocks.

```bash
(
  set -eu
  umask 077
  board="$(pwd -P)/.maestro/maestro-board"
  mkdir -p "$board/sql"
  cd "$board"
  export DOLT_ROOT_PATH="$board" DOLT_DISABLE_EVENT_FLUSH=1
  dolt config --global --add metrics.disabled true
  dolt config --global --add versioncheck.disabled true
  bd --version | grep -q '^bd version 1\.3\.4 '
  [[ "$(dolt version)" == "dolt version 2.2.0" ]]
  nohup dolt sql-server --host 127.0.0.1 --port 3307 --data-dir "$board/sql" \
    </dev/null >>server.log 2>&1 &
  printf '%s\n' "$!" > server.pid
)
```

On a first install, once the log shows the server listening, create the board. Skip this on an update.

```bash
(
  set -eu
  export GIT_CEILING_DIRECTORIES="$(pwd -P)"
  cd .maestro/maestro-board
  export BEADS_DIR="$PWD/.beads" BD_DISABLE_METRICS=1 BD_DISABLE_EVENT_FLUSH=1
  export DOLT_ROOT_PATH="$PWD" DOLT_DISABLE_EVENT_FLUSH=1
  bd --sandbox init --server --external --server-host 127.0.0.1 --server-port 3307 \
    --server-user root --prefix maestro --skip-agents --skip-hooks --non-interactive
)
```

From a fresh linked worktree, run `.maestro/maestro-bd --json ready --unassigned`. Expect a JSON array, empty on a new board. Sessions on other machines need a private network to this one, such as Tailscale; never open the port to the internet.

## 3. Connect the code graph

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

## 4. Verify in T3

Open a fresh session in a new worktree of the repo. Invoke `$maestro-chief-of-staff` in Codex or `/maestro-chief-of-staff` in Claude Code. Replace `<symbol>` with a function or class you know.

> Index this checkout with `index_repository`. Use `search_graph` to find `<symbol>` and show its file path.

Check that the path matches the source file.
