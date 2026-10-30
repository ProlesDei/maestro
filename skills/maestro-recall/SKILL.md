---
name: maestro-recall
description: "Rebuild your recent context on a topic from chat history, tickets, git and PRs, check it against the repo as it is now, and return a short brief. Use for /maestro-recall, 'recall my work on X', 'catch me up on X', 'where did I leave off', or before you start or resume work on a topic."
argument-hint: "<topic> [--since <days>]"
disable-model-invocation: true
---

# Recall

You hand back a brief of where a topic stands now. Read only: change nothing, write no ticket, save no memory. Follow the **maestro-working-principles** skill.

## Start

Open a todo list with one item per phase below.

## Phase A: Set the scope

1. Set the window: the last 7 days, or the number `--since` gives.
2. Search the current repo only. Never read another project's history unless asked.
3. List the search words: the topic, ticket ids, branch names, file names, people.

## Phase B: Search, newest first

1. **Chat history.** Search the harness's session logs, newest file first (`ls -t`). Read only the matching exchanges, never whole transcripts. Skip the current session.
   - Claude Code: `~/.claude/projects/<cwd with each / as ->/*.jsonl`, plus that project's `memory/` folder.
   - Codex: `~/.codex/sessions/`.
   - Other harnesses: their own session store, if you know where it is. If you don't, skip it and say so.
2. **Tickets.** `maestro-ticket list --json`, then `maestro-ticket get <id> --json` and `maestro-ticket events --ticket <id> --json` for each match.
3. **Git.** `git log --since="<n> days ago" --all --oneline -- <paths>`, `git branch -a`, `git worktree list`.
4. **PRs.** `gh pr list --search <topic>`.
5. **Decisions.** The repo's decision log or ADR folder, if it has one.

## Phase C: Check before you trust

1. Chat history and memory say what was true then. For each fact you will repeat, check that the file, flag, branch or ticket still exists and still says that.
2. When two sources disagree, the newer one wins. Cite both and mark the fact `changed`.
3. Put a claim with no source you can open under Unverified, not Decided.

## Phase D: Write the brief

Keep it under 30 lines. Never paste transcripts. If you find a secret, name the file that held it, never the value.

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

**Reply:** the brief above, filled in, and nothing else.
