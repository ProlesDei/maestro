---
name: maestro-recall
description: Use when you're starting or resuming work and want your recent context on a topic rebuilt from your own chat history and the shared record (tickets, decisions, git, memory). You get back a short brief of where things stand now. Read only.
argument-hint: "<topic> [--since 7d]"
disable-model-invocation: true
---
<!-- Claude draft for Paul to edit. Harness skill, not a bot persona; nothing in Maestro depends on it. -->

# Recall

Rebuild what you knew about a topic, check it against how things are now, and hand back a brief. Read only: change nothing, write no ticket, save no memory.

## 1. Search, newest first

The default window is 7 days (`--since` changes it). Search for the topic's words and its likely names: ticket ids, branch names, file names, people.

1. **Your own chat history.** Search the harness's session logs for the topic. Read only the matching exchanges, not whole transcripts.
   - Claude Code: `~/.claude/projects/<cwd with / as ->/*.jsonl`, plus that project's `memory/` folder.
   - Codex: `~/.codex/sessions/`.
   - Other harnesses: their own session store, if you know where it is. If you don't, skip it and say that you did.
2. **The shared record.**
   - Tickets: `maestro-ticket list --json`, then `get` and `events --ticket <id> --json` for the ones that match.
   - Decisions: the repo's decision log, if it has one (in Maestro, the `docs/` decisions folder).
   - Memory: `.maestro/project/memory/`.
   - Git: `git log --since=<window> --all --oneline -- <paths>`, `git branch -a`, `git worktree list`.
   - PRs: `gh pr list --search <topic>`.

## 2. Check before you trust

- Memory and chat history say what was true then. For each fact you'll repeat, check that the file, flag, branch or ticket still exists and still says that.
- When two sources disagree, the newer one wins. Cite both and mark it `changed`.
- A claim with no source you can open goes under "unverified", not under "decided".

## 3. Brief (under about 30 lines)

```
Recall: <topic> (as of <date>, window <n>d)
Where it stands: <2–4 sentences>
Decided:      - <decision>: <source>
In flight:    - <branch / ticket / PR>: <state, sha, who holds it>
Open:         - <question>: <who can answer>
Unverified:   - <claim>: <why it couldn't be checked>
Next step:    <the one thing to do first>
Sources:      <paths, ticket ids, shas, session files>
```

Never paste transcripts, and never repeat a secret you find. Say which file held it.
