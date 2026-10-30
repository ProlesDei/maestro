---
name: maestro-recall
description: "Use for /maestro-recall, 'recall my work on X', 'catch me up on X', 'where did I leave off', or before you start or resume work on a topic. Rebuilds your recent context from chat history, tickets, git and PRs, checks it against the repo as it is now, and returns a short brief. Read only."
argument-hint: "<topic> [--since 7d]"
disable-model-invocation: true
---

# Recall

Rebuild what you knew about a topic, check it against the repo as it is now, and hand back a brief. Read only: change nothing, write no ticket, save no memory.

## 1. Set the scope

The window is the last 7 days unless `--since` says otherwise. The workspace is the current repo; never read another project's history unless asked. Search for the topic's words and its likely names: ticket ids, branch names, file names, people.

## 2. Search, newest first

1. **Your chat history.** Search the harness's session logs and read only the matching exchanges, not whole transcripts.
   - Claude Code: `~/.claude/projects/<cwd with each / as ->/*.jsonl`, plus that project's `memory/` folder.
   - Codex: `~/.codex/sessions/`.
   - Other harnesses: their own session store, if you know where it is. If you don't, skip it and say so.
2. **Tickets.** `maestro-ticket list --json`, then `maestro-ticket get <id> --json` and `maestro-ticket events --ticket <id> --json` for each match.
3. **Git.** `git log --since=<window> --all --oneline -- <paths>`, `git branch -a`, `git worktree list`.
4. **PRs.** `gh pr list --search <topic>`.
5. **Decisions.** The repo's decision log or ADR folder, if it has one.

## 3. Check before you trust

Chat history and memory say what was true then. For each fact you will repeat, check that the file, flag, branch or ticket still exists and still says that. When two sources disagree, the newer one wins: cite both and mark it `changed`. A claim with no source you can open goes under Unverified, not Decided.

## 4. Write the brief

Keep it under 30 lines:

```
Recall: <topic> (as of <date>, window <n>d)
Where it stands: <2 to 4 sentences>
Decided:      - <decision>: <source>
In flight:    - <branch / ticket / PR>: <state, sha, who holds it>
Open:         - <question>: <who can answer>
Unverified:   - <claim>: <why it couldn't be checked>
Next step:    <the one thing to do first>
Sources:      <paths, ticket ids, shas, session files>
```

Never paste transcripts. Never repeat a secret you find; say which file held it.

Reply: the brief above, filled in, and nothing else.
