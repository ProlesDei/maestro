# Install Maestro

Run these steps from the project you want to use in T3.

## 1. Copy the project rules and skills

Copy [the Maestro principles](bots/root-principles.md) into the target project's root `AGENTS.md`. Create the file if needed. On updates, replace only the `## Maestro principles` section. For Claude Code, include `@AGENTS.md` in the root `CLAUDE.md` if it doesn't already import it.

Set `maestro` to your Maestro checkout. For Claude Code, use `.claude/skills` instead of `.agents/skills`.

```sh
maestro=/path/to/maestro
skills=.agents/skills
mkdir -p "$skills"
for src in "$maestro"/bots/*/ "$maestro"/bots/*/skills/*/; do
  [ -f "$src/SKILL.md" ] || continue
  dest="$skills/maestro-$(basename "$src")"
  mkdir "$dest" && cp -R "$src." "$dest/" && rm -rf "$dest/skills"
done
```

If already installed, keep the project's `memory.md` when updating a skill.

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
