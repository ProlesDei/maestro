# Memory for a swarm (draft, not built)

Two kinds of memory. **Short term** is what one piece of work needs to resume: it lives with the work and goes when the work merges. **Long term** is lessons: things a future session needs that the code, git and tickets can't show. Lessons are files in the repo, so git carries them to every machine, and they reach `main` only through a reviewed PR.

## 1. Where each kind of memory lives

Nothing new syncs between machines. A run's scratch never leaves the machine it ran on. The resume note rides on the branch. Lessons are committed. The one piece that doesn't travel today is the ticket store, which is a local SQLite file; going multi-machine means a shared ticket store first, through the Postgres driver its README describes.

```mermaid
flowchart TD
    subgraph Local["This machine only · git's shared folder"]
        S["<b>Run scratch</b><br/>plan step writes, build step reads<br/>gone when the run ends"]
        I["<b>Inbox</b><br/>lessons from runs that<br/>never reached a PR"]
    end
    subgraph Branch["The work's branch · travels when pushed"]
        W["<b>Resume note</b><br/>body of the newest wip: commit"]
        N["<b>New lesson files</b><br/>proposed in this PR"]
    end
    subgraph Main["main · every session, every machine"]
        L["<b>Lessons</b><br/>.maestro/project/memory/"]
        A["<b>Archive</b><br/>kept and searchable,<br/>never loaded"]
    end
    T[("<b>Ticket store</b><br/>claims · status · comments<br/>local today")]
    D["<b>Desk</b><br/>what one bot is driving now<br/>built on demand, never stored"]
    N -->|"review merges"| L
    I -->|"chief of staff opens a PR"| N
    L -->|"over the cap"| A
    T --> D
    W --> D
```

## 2. Which lessons a session loads

Lessons have a scope: everyone (`shared`), one role (`roles/<role>`), or one part of the code (`areas/<path>`). A session loads only the scopes that match its role and the paths it touches, so hundreds of agents each read a short list instead of one giant file. In a playbook step the engine picks the scopes from the ticket's `areas` and the run's role; in your own session you run the same command.

```mermaid
flowchart TD
    Q["<b>Session on T-12</b><br/>role: engineer<br/>touches tools/maestro-ticket/cli"]
    Q --> R["maestro-play memory read<br/>--role engineer<br/>--paths tools/maestro-ticket/cli"]
    R --> S1["<b>shared/</b><br/>everyone · 10 max"]
    R --> S2["<b>roles/engineer/</b><br/>15 max"]
    R --> S3["<b>areas/tools/maestro-ticket/</b><br/>matches the path · 15 max"]
    R -. "not loaded" .-> X["roles/reviewer/ · areas/skills/ · archive/"]
    S1 --> P["<b>Into the prompt</b><br/>40 lessons at most<br/>stale ones marked stale"]
    S2 --> P
    S3 --> P
```

## 3. How a lesson gets in

No curator. A lesson rides in the same PR as the work that taught it, and the reviewer, from another model family, judges it with the code. A bad lesson gets dropped; it never blocks good code. A run that stops before it reaches a PR (often the one that learned the most) leaves its lesson in the inbox, and the chief of staff turns the inbox into a normal PR while it handles the escalation.

```mermaid
sequenceDiagram
    participant S as Session on T-12
    participant E as Engine
    participant B as Branch maestro/T-12
    participant R as Reviewer
    participant M as main
    participant F as A later session
    S->>S: learns: store tests miss lease bugs without -race
    alt your own session
        S->>B: adds memory/areas/tools/maestro-ticket/race.md
    else a playbook step
        S->>E: reply field memory
        E->>B: writes the lesson file before it opens the PR
    end
    B->>R: one PR: the code and the lesson
    R->>R: evidence real? duplicate? right scope? under the cap?
    alt lesson holds
        R->>M: GO merges both
    else lesson fails
        R->>B: auto-fix finding: drop the lesson, code goes on
    end
    M->>F: memory read loads it from then on
```

## 4. How the cap works without losing anything

The cap limits what loads, never what's kept. Lint fails the PR that would push a scope over its cap, and that PR makes room: merge two lessons, promote one into a rule, or archive one with a reason. Promotion is the best way out for an important lesson: a rule in a lint, a check or `AGENTS.md` can't be forgotten. Archived lessons stay in git and `memory search` still finds them. A `keep: true` lesson only the owner can move.

```mermaid
flowchart TD
    A["PR adds a lesson"] --> C{"lint: is the scope<br/>over its cap?"}
    C -->|no| OK["PR passes"]
    C -->|yes| F["lint fails:<br/>make room in this PR"]
    F --> M1["Merge two lessons<br/>into one"]
    F --> M2["Promote one into a rule<br/>lint · check · AGENTS.md · Conventions"]
    F --> M3["Archive one,<br/>with the reason"]
    M1 --> OK
    M2 --> AR
    M3 --> AR["<b>archive/</b><br/>still in git<br/>memory search finds it"]
    AR --> OK
    K["<b>keep: true</b><br/>the owner pinned it"] -. "an agent can't merge or archive it<br/>review asks the owner" .-> F
```

## 5. One Grok Bot, many sessions, two machines

Grok Bot holds none of this in its head. Before it starts anything it reads its desk, which is built from claims, runs and worktrees, so it can't drift. It names the work in every prompt, runs at most one live session per work, and starts each one fresh: the session picks up from the resume note and the lessons, not from old chat. Moving work to another machine means pushing the branch.

```mermaid
sequenceDiagram
    participant G as Grok Bot · engineer
    participant D as Desk
    participant A as Session · T-12
    participant B as Session · T-15
    participant M2 as Machine 2
    G->>D: maestro-play memory desk --bot eng-1
    D-->>G: T-12 in progress, paused at 3f2a · T-15 todo
    par two works at once, one session each
        G->>A: work: T-12 (fresh session)
        A->>A: read the wip note and lessons, go on
    and
        G->>B: work: T-15 (fresh session)
        B->>B: make the worktree, read lessons
    end
    A-->>G: paused: wip commit holds the resume note
    Note over A,M2: to move T-12, push maestro/T-12
    M2->>M2: fetch the branch, read the wip note, go on
```

## Still open

1. Runs that never reach a PR: keep the inbox for them, drained by the chief of staff? (Proposed: yes.)
2. A goal scope, so sibling tickets from one planner goal share findings while they run. Needs a `--goal` ticket field. (Proposed: wait for a real need.)
3. Cap sizes: 15 per area or role, 10 shared, 8 lines per lesson?
4. Only the owner sets `keep: true`?
