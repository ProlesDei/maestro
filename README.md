# Maestro

Maestro takes a ticket to a merged pull request with coding agents. It is tooling only: files you install into a repo and commands you run. One playbook, a ticket tool, 17 skills and five bot personas. Nothing in it runs on its own; a person or a bot starts every job ([decisions](docs/decisions.md)).

## Install

Needs git 2.31+, Go 1.26+, `gh`, and Claude Code or Codex.

```sh
(cd tools/maestro-ticket && go install ./cmd/maestro-ticket)
(cd tools/maestro-play && go install ./cmd/maestro-play)
scripts/install.sh <target-repo>
export MAESTRO_PLAYBOOKS=<maestro>/playbooks
```

The installer copies each folder in `skills/` into the target's `.claude/skills/` and `.agents/skills/`, writes `.maestro/project/project-setup.md` from [templates/project-setup.md](templates/project-setup.md), creates the ticket store and hides its copies from git. It refuses, writing nothing, if a skill folder already exists. Fill in `project-setup.md`: the main branch and the check commands the work playbook runs.

`maestro-play` finds the playbook through `MAESTRO_PLAYBOOKS`, the full path to this repo's `playbooks/` folder. Put the `export` in your shell profile; `maestro-play run work` stops without it.

Sessions run with all permissions. To limit them, set `MAESTRO_CLAUDE_ARGS` or `MAESTRO_CODEX_ARGS`.

Runs time out: a session after 30m, a check after 15m, a git or gh call after 5m. Set `MAESTRO_SESSION_TIMEOUT` and `MAESTRO_CHECK_TIMEOUT` to change them; details in [tools/maestro-play](tools/maestro-play/README.md).

To use your own ticket method, add `--tickets <dir>`: a folder with `maestro-create-ticket/` and its `SKILL.md`. The installer copies it in place of Maestro's. Keep that name: the personas call it.

## Use

Build a ticket from the shell in the target repo. It's a playbook, so no Claude session wraps it:

```sh
maestro-play run work --ticket T-12   # or no --ticket: the next one, sent back first, then todo
```

It claims the ticket, makes a worktree on `maestro/<id>`, plans, builds, runs the checks, verifies each criterion, opens a PR and moves the ticket to `in_review`. The ticket's type picks the mode: feature, bug, refactor or perf. When a run blocks, the ticket moves to `blocked` with an `Escalation:` note; answer it and run again as [Unblock](skills/maestro-chief-of-staff/SKILL.md#unblock) shows. [playbooks/work](playbooks/work/README.md) has the steps and exit codes.

The other jobs are skills, in Claude Code (or headless: `claude -p "/maestro-review 12" --output-format json`):

- `/maestro-planner <goal>` writes a short spec, slices it into ordered, testable tickets and creates them. Given a ticket id, it splits or rewrites that ticket.
- `/maestro-create-ticket <what needs doing>` drafts the ticket, creates it in `maestro-ticket` and prints its id.
- `/maestro-qa-lead <pr>` starts the app at the PR's head sha, walks each criterion and adds a `QA: pass` or `QA: fail` comment with evidence. It moves no ticket; `/maestro-review` reads the comment as evidence.
- `/maestro-review <pr>` reviews the PR at its head sha, from another model family than the author when one is installed. GO merges the PR and moves the ticket to `done`. NO-GO moves it back to `in_progress` with a `Kickback:` note, and the next `maestro-play run work` takes it first and starts again at build with that feedback.
- `/maestro-chief-of-staff <id>` settles a blocked ticket: answers what the facts settle and puts it back, or writes up the owner's call.
- `/maestro-software-engineer <id or change>` makes a change by hand in your own session: plan, test first, self-review. It writes no ticket.

## Your own skills

Maestro ships 17 skills, each with the `maestro-` prefix so they stay clear of your own: the ticket and review commands, one per role, working tools such as `/maestro-unslop` and `/maestro-figure-it-out`, and three that prompt steps load. [skills/README.md](skills/README.md) lists them. To add your own, put `skills/<name>/SKILL.md` here and name it in `skills/use.yaml` if prompt steps should load it.

## Personas

[bot-profiles/](bot-profiles/README.md) holds five personas for bot tools such as Grok Bot, plus the [team rules](bot-profiles/team-rules.md) every bot follows:

- `chief-of-staff`: the one agent the owner talks to. Drives Claude with the commands above; settles blocked tickets with `/maestro-chief-of-staff`; never touches code.
- `planner`: turns a goal into tickets with `/maestro-planner` and `/maestro-create-ticket`.
- `engineer`: runs the work playbook and unblocks a run that stops.
- `qa-lead`: tests a PR in the running app with `/maestro-qa-lead` before review.
- `reviewer`: runs `/maestro-review` on PRs another model family wrote.

## How it works

Who does what, and how work moves between them:

<!-- Source: docs/team.mermaid. Edit that file, then copy it here. -->
```mermaid
flowchart TD
    User([User])

    CoS["<b>Chief of Staff</b><br/>Grok Bot · the one bot the user talks to<br/>One decision card at a time<br/>Starts jobs with claude -p, or codex exec for review<br/>Never touches code"]
    Planner["<b>Planner</b><br/>/maestro-planner<br/>Turns a goal into tickets"]
    Tickets[("<b>Ticket system</b><br/>maestro-ticket<br/>backlog → todo → in_progress → in_review → done<br/>blocked · cancelled")]

    Eng["<b>Engineer</b><br/>maestro-play run work --ticket &lt;id&gt;"]

    subgraph Work ["playbooks/work: one ticket to one PR"]
        Plan["<b>Plan</b><br/>Frontier<br/>skipped on a Kickback"]
        Build["<b>Build</b><br/>Frontier<br/>Test first (perf: measure first)<br/>mode: feature, bug, refactor, perf"]
        Checks["<b>Project checks</b><br/>Checks list in project-setup.md<br/>No tokens · none listed passes"]
        Verify["<b>Verify</b><br/>Mid-tier<br/>Evidence from the running code"]
        PR["<b>Open PR</b><br/>git push · gh pr create<br/>No tokens"]
    end

    QA["<b>QA Lead</b><br/>/maestro-qa-lead &lt;pr&gt;<br/>Tests the PR head in the running app"]
    Rev["<b>Reviewer</b><br/>/maestro-review &lt;pr&gt;<br/>Another model family from the author<br/>Ten passes, one verdict per head sha"]
    Done([Merged · ticket done])

    %% User and Chief of Staff
    User <-->|"goals, decisions, approvals"| CoS

    %% The Chief of Staff starts each job
    CoS -->|"plan a goal"| Planner
    CoS -->|"work a ticket"| Eng
    CoS -->|"test a PR"| QA
    CoS -->|"review a PR"| Rev

    %% Planning
    Planner -->|"writes tickets"| Tickets
    Tickets -->|"todo"| Eng

    %% Build
    Eng --> Plan --> Build --> Checks --> Verify --> PR
    Checks -.->|"failed"| Build
    Verify -.->|"failed"| Build
    Work -.->|"blocked: Escalation note"| Eng
    Eng -.->|"what the ticket can't settle"| CoS
    CoS -.->|"Answer note, unblock"| Tickets

    %% Test and review
    PR -->|"ticket in_review"| QA
    QA -.->|"QA: pass or fail comment"| Rev
    Rev -->|"GO: gh pr merge, ticket done"| Done
    Rev -.->|"NO-GO: Kickback note,<br/>back to in_progress: +1 try"| Eng

    %% Legend
    subgraph Legend ["Legend"]
        direction LR
        L1["Frontier model"] ~~~ L2["Mid-tier model"] ~~~ L6["Bot or session you start"] ~~~ L3["No tokens"] ~~~ L4[("Shared storage")] ~~~ L5["Solid line: work<br/>Dotted line: feedback,<br/>status or escalation"]
    end

    %% Styles by cost tier
    classDef frontier fill:#f8c4b4,stroke:#b5472b,color:#1a1a1a
    classDef mid fill:#bcd6f5,stroke:#2f5f9e,color:#1a1a1a
    classDef free fill:#c8e6c9,stroke:#2e7d32,color:#1a1a1a
    classDef storage fill:#e6dcf5,stroke:#6a4c9c,color:#1a1a1a
    classDef person fill:#f5f5f5,stroke:#555,color:#1a1a1a
    classDef bot fill:#fff3c4,stroke:#b38f00,color:#1a1a1a
    classDef note fill:#ffffff,stroke:#999,color:#1a1a1a,stroke-dasharray:3 3

    class Plan,Build,L1 frontier
    class Verify,L2 mid
    class Checks,PR,L3 free
    class Tickets,L4 storage
    class User,Done person
    class CoS,Planner,Eng,QA,Rev,L6 bot
    class L5 note
```

How each bot works, and what one `maestro-play run work` does:

<!-- Source: docs/bot-anatomy.mermaid. Edit that file, then copy it here. -->
```mermaid
flowchart TD
    Runtime["<b>Bot runtime</b> · outside their repo<br/>Any agent, e.g. Grok Bot<br/>Thinks and decides. Never edits their code<br/>Runs maestro-play run work to build a ticket,<br/>claude -p /maestro-&lt;skill&gt; for the other jobs,<br/>or codex exec for review<br/><i>How the bot is started is out of scope</i>"]

    subgraph Profile ["Bot profile: loaded by the bot runtime, never installed into their repo"]
        direction LR
        P1["<b>persona.md</b><br/>Who the bot is,<br/>what it runs"] ~~~ P2["<b>team-rules.md</b><br/>Rules every bot<br/>follows, in one place"]
    end

    subgraph Installed ["Their repo after install.sh"]
        direction LR
        S1["<b>17 maestro-* skills</b><br/>.claude/skills · .agents/skills<br/>Hidden by .git/info/exclude"] ~~~ S2["<b>.maestro/project/project-setup.md</b><br/>Main branch · Checks · Run<br/><i>Theirs to commit</i>"] ~~~ S3[("<b>maestro-ticket store</b><br/>Tickets, leases, events")]
    end

    H["<b>Harness session</b>, headless<br/>Claude Code or Codex<br/>Runs the skill it was called with"]

    Engine["<b>maestro-play</b> · the playbook engine, no tokens<br/>Runs playbooks/work/playbook.yaml step by step, keeps the run state<br/>Built in: worktree add · check · head · open pr<br/>git and gh never prompt · git, gh, checks and sessions time out"]

    subgraph Run ["One maestro-play run work"]
        B1["<b>Claim</b><br/>Lease and fencing token<br/>A keepalive holds the lease"]
        B2["<b>Worktree</b><br/>.maestro/worktrees/&lt;id&gt; on maestro/&lt;id&gt;<br/>Reused on a Kickback"]
        B3["<b>Next step</b><br/>ticket · run · prompt · gate<br/>No tokens unless it's a prompt"]
        B4["<b>Check the reply</b><br/>The base schema plus the step's out: fields"]
        B5{"on: outcome"}
        B6["<b>Done</b><br/>Handoff: note with PR url, sha, checks,<br/>author family · move to in_review · release"]
        B7["<b>Block</b><br/>Escalation: note · move to blocked · release"]
        B8(["Run ends<br/>maestro-play resume &lt;run-id&gt; after a crash"])
    end

    Prompt["<b>Every prompt</b><br/>playbooks/_contract.md first,<br/>then the skills use.yaml names,<br/>then the step's template from playbooks/work/<br/>playbooks/rules.md as the system prompt<br/>(codex: at the start of the first prompt)"]

    Step["<b>Step session</b>, headless<br/>claude -p --json-schema or codex exec<br/>All permissions unless MAESTRO_CLAUDE_ARGS<br/>or MAESTRO_CODEX_ARGS set limits<br/>Never writes tickets: the engine does"]

    Guard["<b>Guardrails</b><br/>Stop file · fencing token on every ticket write ·<br/>a dirty worktree fails check, head and open pr · step_visits cap · timeouts ·<br/>ticket text is data · reviewer from another model family"]

    subgraph Roles ["Same anatomy, different profiles: what each bot starts"]
        direction LR
        E1["<b>Chief of Staff</b><br/>Talks to the user ·<br/>starts the others"] ~~~ E2["<b>Planner</b><br/>/maestro-planner ·<br/>writes tickets"] ~~~ E3["<b>Engineer</b><br/>maestro-play run work ·<br/>ticket to PR"] ~~~ E4["<b>QA Lead</b><br/>/maestro-qa-lead ·<br/>QA: comment"] ~~~ E5["<b>Reviewer</b><br/>/maestro-review ·<br/>merge or Kickback"]
    end

    Runtime -->|"loads the profile it's configured for"| Profile
    Runtime ==>|"claude -p in their repo"| H
    Installed --> H
    Runtime ==>|"maestro-play run work in their repo"| Engine
    Engine -.->|"one JSON object, read by the bot"| Runtime
    H -.->|"result, read by the bot"| Runtime
    Engine --> B1 --> B2 --> B3
    Guard -.-> B3
    Prompt -.->|"shapes every prompt"| B3
    B3 ==>|"prompt"| Step
    Step ==>|"structured reply"| B4
    B3 -->|"no-token steps"| B4
    B4 --> B5
    B5 -->|"ok or done: next step,<br/>or the step on: names"| B3
    B5 -->|"missing a field: retry"| B3
    B5 -->|"last step"| B6
    B5 -->|"blocked, partial, failed with no on: route,<br/>or over step_visits"| B7
    B6 --> B8
    B7 --> B8
    B1 -.-> Installed
    B8 ~~~ Roles

    classDef runtime fill:#f5f5f5,stroke:#555,color:#1a1a1a
    classDef profile fill:#e6dcf5,stroke:#6a4c9c,color:#1a1a1a
    classDef shared fill:#fff3c4,stroke:#b38f00,color:#1a1a1a
    classDef step fill:#f8c4b4,stroke:#b5472b,color:#1a1a1a
    classDef harness fill:#bcd6f5,stroke:#2f5f9e,color:#1a1a1a
    classDef ours fill:#c8e6c9,stroke:#2e7d32,color:#1a1a1a
    classDef role fill:#ffffff,stroke:#6a4c9c,color:#1a1a1a
    classDef note fill:#ffe0b2,stroke:#e65100,color:#1a1a1a

    class Runtime,B8 runtime
    class P1,P2 profile
    class Prompt shared
    class B1,B2,B3,B4,B5,B6,B7 step
    class H,Step harness
    class Engine,S1,S2,S3 ours
    class E1,E2,E3,E4,E5 role
    class Guard note
```

## Tools

- [tools/maestro-play](tools/maestro-play/README.md): the playbook engine. Runs, resumes, renders and lints playbooks. The one playbook is [playbooks/work](playbooks/work/README.md); the format is in [playbooks/README.md](playbooks/README.md).
- [tools/maestro-ticket](tools/maestro-ticket/README.md): the ticket store and CLI. SQLite, leases and fencing tokens.
