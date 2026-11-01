# Maestro

Maestro is infrastructure for a fully agentic workflow: a swarm of up to hundreds of agents working at once. The goal is to give one person the power of a whole company.

A team installs it into its own repo and gets:

- `maestro-play`, a playbook engine that takes one ticket to an open PR.
- `maestro-ticket`, a ticket store and CLI.
- 18 `maestro-*` skills, and six bot profiles.

Nothing in Maestro runs on its own: no loop, scheduler, dispatcher, watcher or webhook. A person or Grok Bot starts every job ([decisions](docs/decisions.md)).

## Who drives it

Grok Bot takes the place of the person who prompts the harness. It runs outside your repo, takes on one of the six profiles in [bot-profiles/](bot-profiles/README.md), comes into the repo, and drives Claude Code or Codex with that role's skills and commands. The owner sets goals and makes the calls only a person should make; Grok Bot does the rest.

## How work moves

1. The **chief of staff** talks to the owner, one decision at a time, sets direction and hands goals to the planner. It never touches code or tickets and never starts runs.
2. The **planner** runs the team day to day, as product owner and project manager in one. It turns goals into tickets with `/maestro-planner`, hands each ticket's build, QA and review to the right bot, and reports status and the blocks it can't settle to the chief of staff.
3. The **senior engineer** (top model, high effort) takes risky or broad tickets; the **junior engineer** (a cheaper model) takes routine ones. Either runs `maestro-play run work`, which takes one ticket to an open PR: worktree, plan, build, your project's checks, verify, PR. Only prompt steps spend tokens.
4. The **QA lead** tests the PR in the running app with `/maestro-qa-lead` and comments `QA: pass` or `QA: fail`.
5. The **reviewer**, from another model family than the author when one is installed, runs `/maestro-code-review`. GO merges the PR and closes the ticket; NO-GO sends it back with a `Kickback:` note.
6. A blocked run leaves an `Escalation:` note. It climbs one rung at a time (junior engineer, senior engineer, planner, chief of staff, owner) until someone can answer it; the planner posts the answer and the ticket runs again.

Every role follows the [team rules](bot-profiles/team-rules.md).

<!-- Source: docs/team.mermaid. Edit that file, then copy it here. -->
```mermaid
flowchart TD
    User([User])

    subgraph Grok ["Grok Bot · outside the repo · plays one role at a time"]
        CoS["<b>Chief of Staff</b><br/>The one role the user talks to<br/>One decision card at a time<br/>Sets direction, settles escalations<br/>Never touches code or tickets"]
        Planner["<b>Planner</b><br/>/maestro-planner<br/>Product owner and project manager<br/>Turns goals into tickets<br/>Hands out builds, QA and review"]
        Sr["<b>Senior Engineer</b><br/>maestro-play run work --ticket &lt;id&gt;<br/>Top model, high effort<br/>Risky or broad tickets, what the junior hands up"]
        Jr["<b>Junior Engineer</b><br/>maestro-play run work --ticket &lt;id&gt;<br/>Cheaper model<br/>Routine tickets"]
        QA["<b>QA Lead</b><br/>/maestro-qa-lead &lt;pr&gt;<br/>Tests the PR head in the running app"]
        Rev["<b>Reviewer</b><br/>/maestro-code-review &lt;pr&gt;<br/>Another model family from the author<br/>Passes scaled to risk, one verdict per head sha"]
    end

    Tickets[("<b>Ticket system</b><br/>maestro-ticket<br/>backlog → todo → in_progress → in_review → done<br/>blocked · cancelled")]

    subgraph Work ["playbooks/work: one ticket to one PR"]
        Plan["<b>Plan</b><br/>Frontier<br/>skipped on a Kickback"]
        Build["<b>Build</b><br/>Frontier<br/>Test first (perf: measure first)<br/>mode: feature, bug, refactor, perf"]
        Checks["<b>Project checks</b><br/>Checks list in project-setup.md<br/>No tokens · none listed passes"]
        Verify["<b>Verify</b><br/>Mid-tier<br/>Evidence from the running code"]
        PR["<b>Open PR</b><br/>git push · gh pr create<br/>No tokens"]
    end

    Done([Merged · ticket done])

    %% User and Chief of Staff
    User <-->|"goals, decisions, approvals"| CoS

    %% The Chief of Staff leads through the Planner
    CoS -->|"goals, answers"| Planner
    Planner -.->|"board status, blocks it can't settle"| CoS

    %% The Planner hands out each job
    Planner -->|"risky ticket"| Sr
    Planner -->|"routine ticket"| Jr
    Planner -->|"test a PR"| QA
    Planner -->|"review a PR"| Rev

    %% Planning
    Planner -->|"writes tickets"| Tickets
    Tickets -->|"todo"| Sr
    Tickets -->|"routine todo"| Jr

    %% Build
    Sr --> Plan
    Jr --> Plan
    Plan --> Build --> Checks --> Verify --> PR
    Checks -.->|"failed"| Build
    Verify -.->|"failed"| Build
    Work -.->|"blocked: Escalation note"| Sr
    Work -.->|"blocked: Escalation note"| Jr
    Jr -.->|"a block it can't settle,<br/>a second Kickback"| Sr
    Sr -.->|"what the ticket can't settle"| Planner
    Planner -.->|"Answer note, unblock"| Tickets

    %% Test and review
    PR -->|"ticket in_review"| QA
    QA -.->|"QA: pass or fail comment"| Rev
    Rev -->|"GO: gh pr merge, ticket done"| Done
    Rev -.->|"NO-GO: Kickback note,<br/>back to in_progress: +1 try"| Sr
    Rev -.->|"NO-GO"| Jr

    %% Legend
    subgraph Legend ["Legend"]
        direction LR
        L1["Frontier model"] ~~~ L2["Mid-tier model"] ~~~ L6["Role Grok Bot plays"] ~~~ L3["No tokens"] ~~~ L4[("Shared storage")] ~~~ L5["Solid line: work<br/>Dotted line: feedback,<br/>status or escalation"]
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
    class CoS,Planner,Sr,Jr,QA,Rev,L6 bot
    class L5 note
    style Grok fill:none,stroke:#b38f00,stroke-dasharray:4 4
```

## What we hold to

- **Least friction for your team.** Step sessions run with all permissions; you set limits if you want them ([Install](#install)).
- **The engine decides what runs next, not a prompt.** A rule goes in code, a schema or a lint before it goes in prose.
- **Proof over claims.** A review verdict covers one head sha. A worker's report is a claim until a run shows it.
- **Never clash with your setup.** Every shipped skill is named `maestro-*`. The installer never writes over a file it didn't place, and you pick where the skills live.
- **Read your setup, don't assume it.** Your main branch, checks and ticket flow come from `.maestro/project/project-setup.md`. You can track tickets in your own tool.
- **Small and plain.** Cut what only exists to look impressive.

## Install

Tell your agent (Claude Code, Codex or Grok), in your repo: *"Follow INSTALL.md in github.com/ProlesDei/maestro."* [INSTALL.md](INSTALL.md) has it ask you each call, one at a time: which harnesses, where the skills live, your ticket tool, your checks, each bot's starting memories. It writes your answers to `.maestro/project/install.yaml` and `project-setup.md`, and leaves the exact copying to [scripts/install.sh](scripts/install.sh), which never writes over a file of yours.

It needs git 2.31+, Go 1.26+, bash 4+, `gh` with access to this repo, and Claude Code or Codex.

What you get:

- Maestro pinned to one commit in `.maestro/src/`, hidden from git. `maestro-play` reads its playbooks from there.
- `maestro-ticket` and `maestro-play` on your `PATH`, one copy for every repo on the machine.
- The skills in `.claude/skills/` and `.agents/skills/`: committed, or hidden from git.
- `.maestro/project/project-setup.md`: your main branch, checks, how to run the app, your ticket flow.
- One paste-ready block per Grok bot in `.maestro/bots/`.

The same guide updates Maestro to a newer commit and uninstalls it.

Sessions run with all permissions. To limit them, set `MAESTRO_CLAUDE_ARGS` or `MAESTRO_CODEX_ARGS`.

Runs time out: a session after 30m, a check after 15m, a git or gh call after 5m. Set `MAESTRO_SESSION_TIMEOUT` and `MAESTRO_CHECK_TIMEOUT` to change them; details in [tools/maestro-play](tools/maestro-play/README.md).

Optional: a code graph. With [codebase-memory-mcp](https://github.com/DeusData/codebase-memory-mcp) as an MCP server, step sessions find symbols and callers before they grep, which costs fewer reads. INSTALL.md offers it.

## Use

Build a ticket from the shell in the target repo. It's a playbook, so no Claude session wraps it:

```sh
maestro-play run work --ticket T-12   # or no --ticket: the next one, sent back first, then todo
```

It claims the ticket, makes a worktree on `maestro/<id>`, plans, builds, runs the checks, verifies each criterion, opens a PR and moves the ticket to `in_review`. The ticket's type picks the mode: feature, bug, refactor or perf. When a run blocks, the ticket moves to `blocked` with an `Escalation:` note; answer it and run again as [Unblock](playbooks/work/README.md#unblock) shows. [playbooks/work](playbooks/work/README.md) has the steps and exit codes.

The other jobs are skills, in Claude Code (or headless: `claude -p "/maestro-code-review 12" --output-format json`):

- `/maestro-planner <goal>` writes a short spec, slices it into ordered, testable tickets and creates them. Given a ticket id, it splits or rewrites that ticket. It also checks a plan, finds what keeps the board from running in parallel, or answers a code question a plan turns on, for the planner bot, which can't read the repo.
- `/maestro-create-ticket <what needs doing>` drafts the ticket, creates it in `maestro-ticket` and prints its id.
- `/maestro-qa-lead <pr>` is the last check before production. It builds the PR's head sha the way production does, walks each criterion and the critical flows in a browser, tries harder angles as the risk goes up (upgrade from old data, rollback, a second user, a dependency down), and adds a `QA: pass` or `QA: fail` comment with evidence. It moves no ticket; `/maestro-code-review` reads the comment as evidence. `/maestro-qa-lead <ask>` does the QA lead's other jobs: a second look at a verdict, a bug reproduced, a ticket checked before it's built, a test plan.
- `/maestro-code-review <pr>` reviews the PR at its head sha, from another model family than the author when one is installed. GO merges the PR and moves the ticket to `done`. NO-GO moves it back to `in_progress` with a `Kickback:` note, and the next `maestro-play run work` takes it first and starts again at build with that feedback. ESCALATE blocks the ticket with an `Escalation:` note when the change needs the owner's yes (a dependency, CI or hook change, or one a revert can't undo) or a third kickback would repeat the last two. The review's depth follows the PR's risk: a docs fix gets one pass, an auth or migration change all ten. Add `--report-only` for the report alone.
- `/maestro-chief-of-staff <question or id>` is the chief of staff thinking inside the repo while the swarm runs. The chief of staff runs it to check a bot's report or get a second opinion on a call; the planner runs it to group blocked tickets by cause. It drafts the `Answer:` texts, the owner's next card and the messages to send. It changes nothing and hands back no commands.
- `/maestro-software-engineer <id or change>` makes a change by hand in your own session, always in its own worktree kept up to date with main: plan, test first, self-review. It writes no ticket. Any session that writes code loads it, and so do the plan and build steps.

## Your own skills

Maestro ships 18 skills, each with the `maestro-` prefix so they stay clear of your own: the ticket and review commands, one per role, working tools such as `/maestro-unslop` and `/maestro-figure-it-out`, and four that prompt steps load. [skills/README.md](skills/README.md) lists them. To add your own, put `skills/<name>/SKILL.md` here and name it in `skills/use.yaml` if prompt steps should load it.

## How a bot works

What a bot loads, what the installer leaves in your repo, and what one `maestro-play run work` does:

<!-- Source: docs/bot-anatomy.mermaid. Edit that file, then copy it here. -->
```mermaid
flowchart TD
    Runtime["<b>Grok Bot</b> · outside their repo<br/>Takes the place of the person at the keyboard<br/>Thinks and decides. Never edits their code<br/>Runs maestro-play run work to build a ticket,<br/>claude -p /maestro-&lt;skill&gt; for the other jobs,<br/>or codex exec for review<br/><i>How Grok Bot is started is out of scope</i>"]

    subgraph Profile ["Role profile: loaded by Grok Bot, never installed into their repo"]
        direction LR
        P1["<b>persona.md</b><br/>Who the bot is,<br/>what it runs"] ~~~ P2["<b>team-rules.md</b><br/>Rules every bot<br/>follows, in one place"]
    end

    subgraph Installed ["Their repo after INSTALL.md"]
        direction LR
        S1["<b>18 maestro-* skills</b><br/>.claude/skills · .agents/skills<br/>Committed or hidden from git: their pick"] ~~~ S2["<b>.maestro/project/project-setup.md</b><br/>Main branch · Checks · Run<br/><i>Theirs to commit</i>"] ~~~ S3[("<b>maestro-ticket store</b><br/>Tickets, leases, events")]
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

    subgraph Roles ["One Grok Bot, six profiles: what each role does"]
        direction LR
        E1["<b>Chief of Staff</b><br/>Talks to the user ·<br/>hands goals to the planner"] ~~~ E2["<b>Planner</b><br/>/maestro-planner ·<br/>writes tickets, runs the board"] ~~~ E3["<b>Senior Engineer</b><br/>maestro-play run work ·<br/>risky ticket to PR"] ~~~ E6["<b>Junior Engineer</b><br/>maestro-play run work ·<br/>routine ticket to PR"] ~~~ E4["<b>QA Lead</b><br/>/maestro-qa-lead ·<br/>QA: comment"] ~~~ E5["<b>Reviewer</b><br/>/maestro-code-review ·<br/>merge or Kickback"]
    end

    Runtime -->|"loads the profile for the role it plays"| Profile
    Runtime ==>|"claude -p in their repo"| H
    Installed --> H
    Runtime ==>|"maestro-play run work in their repo"| Engine
    Engine -.->|"one JSON object, read by Grok Bot"| Runtime
    H -.->|"result, read by Grok Bot"| Runtime
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
    class E1,E2,E3,E6,E4,E5 role
    class Guard note
```

## Tools

- [tools/maestro-play](tools/maestro-play/README.md): the playbook engine. Runs, resumes, renders and lints playbooks. The one playbook is [playbooks/work](playbooks/work/README.md); the format is in [playbooks/README.md](playbooks/README.md).
- [tools/maestro-ticket](tools/maestro-ticket/README.md): the ticket store and CLI. SQLite, leases and fencing tokens.

## Credits

Two builders shape how Maestro works. Kun Chen's [firstmate](https://github.com/kunchenguid/firstmate), [no-mistakes](https://github.com/kunchenguid/no-mistakes) and [treehouse](https://github.com/kunchenguid/treehouse) gave us the chief of staff, the checks and review every PR passes, and a worktree per change. Lauren Tan's [pstack](https://github.com/cursor/plugins/tree/main/pstack) gave our skills their form and many of their names.
