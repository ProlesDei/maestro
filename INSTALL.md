# Install Maestro

Run these steps from the project you want to use in T3.

## 1. Copy the project rules and chief-of-staff skill

Copy [the Maestro principles](bots/root-principles.md) into the target project's root `AGENTS.md`. Create the file if needed. On updates, replace only the `## Maestro principles` section. For Claude Code, include `@AGENTS.md` in the root `CLAUDE.md` if it doesn't already import it.

Set `bot` to the bot directory in your Maestro checkout. For Claude Code, use `.claude/skills` instead of `.agents/skills`.

```sh
bot=/path/to/maestro/bots/chief-of-staff
skills=.agents/skills
dest="$skills/maestro-chief-of-staff"
mkdir -p "$skills" && mkdir "$dest" && cp "$bot/SKILL.md" "$bot/memory.md" "$dest/"
```

If already installed, keep the project's `memory.md` when updating the skill.

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

Open a fresh project session. Invoke `$maestro-chief-of-staff` in Codex or `/maestro-chief-of-staff` in Claude Code. Replace `<symbol>` with a function or class you know.

> Index this checkout with `index_repository`. Use `search_graph` to find `<symbol>` and show its file path.

Check that the path matches the source file.
