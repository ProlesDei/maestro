# maestro

An automated agentic workflow that takes products from plan to ship, packaged so you can install it into any project.

maestro bundles a full suite of agent roles, skills, and tools. Install it, give it context about your project, and the workflow is ready to run. No per-project setup of agents, prompts, or tooling.

## What's included

| Part | What it is |
|---|---|
| Roles | Agent definitions. Each role owns one part of the delivery process. |
| Skills | Reusable instructions. Bot skills (`skills/bot/`) teach the bots how to prompt and judge, and stay with the bot runtime. Harness skills (`skills/harness/`, `maestro-*`) teach a harness session one step; they install into your harness's skill folder and run only when a prompt names them. See `skills/README.md`. |
| Playbooks | Step-by-step plans for each role's jobs, in `playbooks/<role>/`. The bot runtime picks one per ticket and runs its steps in one harness session. See `playbooks/README.md`. |
| Tools | Scripts and commands the agents call to do the work. `tools/maestro-ticket/` is the ticket system: a CLI over a SQLite store shared by every worktree, with leases, versioned transitions, an event log and a generated `Backlog.md`. See `tools/maestro-ticket/README.md`. |
| Evals | One golden task per playbook in `evals/<role>/`, the situation and the result it must produce, for catching regressions once the eval runner exists. `evals/check.py` checks the format of the playbooks, tasks, skills and docs. See `evals/README.md`. |
| Bot profiles | One folder per bot at `bot-profiles/<name>/persona.md`, plus any supporting files in that folder, and `bot-profiles/team-rules.md` shared by all of them. Outside tools, such as Grok, read a folder and copy it to create the bot. |

## How it works

1. **Install** maestro into your project.
2. **Add project context**: what you're building, the stack, build and test commands, conventions.
3. **Run the workflow.** The agents plan, build, check, and ship.

### The agent team

You talk to one agent: the Chief of Staff. It turns your goals into workstreams, each run by its own Planner session, which writes the tickets. Engineers pick up tickets from the ticket system (`maestro-ticket`). Ship tickets pass automated checks, a code review and QA before the Operations Manager releases them, within the trust level you set; scout tickets end at an evidenced report. Each role runs on the cheapest model tier that can do the job; color marks the tier. The rules every bot follows are in `bot-profiles/team-rules.md`.

<!-- Source: docs/team.mermaid. Edit that file, then copy it here. -->
```mermaid
flowchart TD
    User([User])

    CoS["<b>Chief of Staff</b><br/>Frontier<br/>Goals and workstreams, never tickets"]
    Planner["<b>Planner sessions</b><br/>Mid-tier<br/>One per workstream, owns its tickets"]
    Tickets[("<b>Ticket system</b><br/>maestro-ticket<br/>Ship and scout tickets, status, blockers<br/>Backlog.md and Jira are mirrors")]

    subgraph Build ["Build, review and test: pools of parallel sessions"]
        subgraph Engineers ["Engineers"]
            SSE["<b>Senior Engineer</b><br/>Frontier"]
            JSE["<b>Junior Engineer</b><br/>Mid-tier"]
        end
        Checks["<b>Automated checks</b><br/>Runner, no tokens"]
        CR["<b>Code Reviewer</b><br/>Mid-tier, another model<br/>family from the author"]
        QA["<b>QA Lead</b><br/>Mid-tier<br/>Evidence from the running app"]
    end

    Ops["<b>Operations Manager</b><br/>Mid-tier"]
    Logs[("<b>Logs, usage and memory inbox</b><br/>Retros, failures, token cost,<br/>memory proposals")]
    Done([Released])

    %% User and Chief of Staff
    User <-->|"goals, decisions, approvals,<br/>digest and recap"| CoS

    %% Planning
    CoS -->|"workstream brief"| Planner
    Planner -->|"writes specs and tickets"| Tickets
    Planner -.->|"workstream summary,<br/>escalates above its authority"| CoS

    %% Status and blockers flow through the ticket system
    Tickets -.->|"status, blockers, escalations,<br/>scout reports"| Planner

    %% Engineers pick up work
    Tickets -->|"complex or risky"| SSE
    Tickets -->|"routine"| JSE
    JSE -.->|"escalates after 2 tries"| SSE
    Engineers -.->|"updates status, escalates,<br/>scout reports"| Tickets

    %% Build, review, test
    Engineers -->|"submits ship ticket"| Checks
    Checks -->|"passed"| CR
    CR -->|"approved"| QA
    Checks -.->|"failed"| Engineers
    CR -.->|"changes requested: +1 try"| Engineers
    QA -.->|"failed: +1 try"| Engineers

    %% Release
    QA -->|"passed: ready to release"| Ops
    Ops -.->|"deploy needs approval"| CoS
    Ops -->|"opens PR; merges and deploys<br/>only as the trust level allows"| Done
    Ops -->|"marks ship ticket done after release"| Tickets

    %% Operations
    CoS -->|"infrastructure requests"| Ops
    Build -.->|"logs"| Logs
    Logs -->|"curates memory, reviews costs and failures"| Ops
    Ops -.->|"suggests one tested workflow change"| CoS

    %% Legend
    subgraph Legend ["Legend"]
        direction LR
        L1["Frontier model"] ~~~ L2["Mid-tier model"] ~~~ L3["No tokens"] ~~~ L4[("Shared storage")] ~~~ L5["Solid line: work<br/>Dotted line: feedback,<br/>status or escalation"]
    end

    %% Styles by cost tier
    classDef frontier fill:#f8c4b4,stroke:#b5472b,color:#1a1a1a
    classDef mid fill:#bcd6f5,stroke:#2f5f9e,color:#1a1a1a
    classDef free fill:#c8e6c9,stroke:#2e7d32,color:#1a1a1a
    classDef storage fill:#e6dcf5,stroke:#6a4c9c,color:#1a1a1a
    classDef person fill:#f5f5f5,stroke:#555,color:#1a1a1a
    classDef note fill:#ffffff,stroke:#999,color:#1a1a1a,stroke-dasharray:3 3

    class CoS,SSE,L1 frontier
    class Planner,JSE,CR,QA,Ops,L2 mid
    class Checks,L3 free
    class Tickets,Logs,L4 storage
    class User,Done person
    class L5 note
```

### Bot anatomy

An outside bot runtime, for example Grok Bot, creates a bot for each persona in `bot-profiles/`. Every persona operates under this same anatomy. The bot runtime loads the role's profile (`persona.md`, the team rules, playbooks and bot skills) outside the user's repo. It runs the standard session loop: preflight, starting a headless harness session with launch flags, checking intake, claiming work, prompting steps under the prompt contract, checking structured replies with evidence, batched wrap-up, and cleanup. The bot never touches the repo: harness sessions do the hands-on work, and the runner (`docs/runner.md`) runs every no-token step within the role's permissions and the trust level.

<!-- Source: docs/bot-anatomy.mermaid. Edit that file, then copy it here. -->
```mermaid
flowchart TD
    Runtime["<b>Bot runtime</b> · outside their repo<br/>Any agent, e.g. a Grok or Claude bot<br/>Thinks, decides and prompts. Never touches files or runs commands<br/><i>How it's started is out of scope</i>"]

    subgraph Profile ["Bot profile: loaded by the bot runtime, never installed into their repo"]
        direction LR
        P1["<b>persona.md</b><br/>Persona and<br/>decision authority"] ~~~ P2["<b>team-rules.md</b><br/>Rules every bot<br/>follows, in one place"] ~~~ P3["<b>playbooks/</b><br/>Step-by-step plans<br/>for the role's jobs"] ~~~ P5["<b>Bot skills</b><br/>runner · prompt contract ·<br/>choosing a model per step"]
    end

    Runner["<b>Runner</b> · no tokens<br/>The bot's only way into their repo<br/>Named requests, allowed by role and trust level<br/>Stop file checked first · usage recorded"]

    subgraph Loop ["Session loop: identical for every role"]
        B0["<b>Preflight</b>, through the runner<br/>Stop file set? Stop<br/>Health check: harness logged in, maestro-ticket doctor passes, git clean<br/>Environment, trust level, budgets"]
        B1["<b>Start a harness session with launch flags</b><br/>.maestro/rules.md · role permissions · model<br/>Their config files stay untouched<br/>Stable content first, so it gets cached"]
        B2["<b>Prompt: check intake</b> · maestro-check-intake<br/>Requests → work sent back → new work → scheduled duties<br/>Resume the scratchpad if one exists"]
        B3["<b>Pick the playbook</b><br/>Prompt: claim the work · maestro-claim<br/>The claim keeps the lease alive in the background"]
        B4["<b>Next step</b><br/>Pick the model tier for this step<br/>No tokens: a runner request<br/>Otherwise a prompt naming the harness skill"]
        B5["<b>Check the structured reply</b><br/>Goal met? Evidence included?"]
        B6{"Result?"}
        B7["<b>Prompt: wrap up</b>, batched<br/>maestro-propose-memory, to the inbox ·<br/>maestro-handoff-note or maestro-escalate ·<br/>maestro-log-entry: retro, what slowed me down?"]
        B8["<b>Clean up</b> · hook if available, else maestro-clean-up<br/>Scratchpad · temp files · processes · worktree · claim<br/>Then close the harness session"]
        B9{"More in intake?"}
        B10(["Session ends"])
    end

    Contract["<b>Prompt contract</b><br/>1 the goal<br/>2 pointers, not pasted content<br/>3 constraints and permissions<br/>4 what done means<br/>5 reply: short, structured, with evidence<br/><br/>Same session across a playbook's steps ·<br/>fresh session per ticket"]

    subgraph HarnessSide ["Their repo · the harness does all the work"]
        H["<b>Harness session</b>, headless<br/>Claude Code · Codex · Grok Build"]
        Loads["<b>Uses our footprint only</b><br/>.maestro/rules.md · project/ setup and memory<br/>maestro-* skills, loaded only when named"]
        Work["<b>Does the hands-on work</b><br/>Ticket system CLI: maestro-ticket · files and code ·<br/>commits on maestro/ branches · tests and the running app"]
    end

    Guard["<b>Guardrails</b><br/>Trust levels: propose-only at first · stop file ·<br/>role permissions · budgets and retry limits ·<br/>approval gates · secrets never in prompts, replies or logs"]

    subgraph Roles ["Same anatomy, different profiles: what each role lets the harness do"]
        direction LR
        E1["<b>Chief of Staff</b><br/>Read summaries ·<br/>goals and workstreams"] ~~~ E2["<b>Planner</b><br/>One workstream's tickets ·<br/>read-only code"] ~~~ E3["<b>Engineers</b><br/>Edit and commit on<br/>maestro/ branches"] ~~~ E4["<b>Code Reviewer</b><br/>Read-only code<br/>and diffs"] ~~~ E5["<b>QA Lead</b><br/>Run tests and the app ·<br/>read-only code"] ~~~ E6["<b>Operations</b><br/>PRs, merges, deploys by trust level ·<br/>memory · improvement loop"]
    end

    Runtime -->|"loads the profile it's configured for"| Profile
    Profile --> B0
    Guard -.-> B0
    B0 --> B1 --> B2 --> B3 --> B4
    Contract -.->|"shapes every prompt"| B4
    B0 -.->|"requests"| Runner
    B4 -.->|"no-token steps"| Runner
    Runner ==>|"launches with flags"| H
    B4 ==>|"prompt"| H
    H ==>|"structured reply"| B5
    B5 --> B6
    B6 -->|"step done, more to do"| B4
    B6 -->|"not right, within budget:<br/>sharper prompt"| B4
    B6 -->|"stuck or over budget:<br/>escalate"| B7
    B6 -->|"playbook done"| B7
    B7 --> B8 --> B9
    B9 -->|"yes: new session"| B0
    B9 -->|"no"| B10
    Loads --> H
    H --> Work
    B10 ~~~ Roles

    classDef runtime fill:#f5f5f5,stroke:#555,color:#1a1a1a
    classDef profile fill:#e6dcf5,stroke:#6a4c9c,color:#1a1a1a
    classDef shared fill:#fff3c4,stroke:#b38f00,color:#1a1a1a
    classDef step fill:#f8c4b4,stroke:#b5472b,color:#1a1a1a
    classDef harness fill:#bcd6f5,stroke:#2f5f9e,color:#1a1a1a
    classDef ours fill:#c8e6c9,stroke:#2e7d32,color:#1a1a1a
    classDef role fill:#ffffff,stroke:#6a4c9c,color:#1a1a1a
    classDef note fill:#ffe0b2,stroke:#e65100,color:#1a1a1a

    class Runtime,B10 runtime
    class P1,P2,P3 profile
    class P5,Contract shared
    class B0,B1,B2,B3,B4,B5,B6,B7,B8,B9 step
    class H,Work harness
    class Loads,Runner ours
    class E1,E2,E3,E4,E5,E6 role
    class Guard note
```

## Supported harnesses

maestro runs on the three main coding agents:

- Claude Code
- Codex
- Grok Build

Installed sessions get Maestro's rules through launch flags and never rely on, or edit, your `AGENTS.md` or `CLAUDE.md`. In this repo, `AGENTS.md` holds the instructions for developing Maestro itself; `CLAUDE.md` imports it.

## Status

Early. The roles, skills, playbooks and the ticket store are written; the runner, the wake loop and the installer aren't built yet, so nothing installs today. Install steps will land here once they are.
