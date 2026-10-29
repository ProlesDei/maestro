# maestro

An automated agentic workflow that takes products from plan to ship, packaged so you can install it into any project.

maestro bundles a full suite of agent roles, skills, and tools. Install it, give it context about your project, and the workflow is ready to run. No per-project setup of agents, prompts, or tooling.

## What's included

| Part | What it is |
|---|---|
| Roles | Agent definitions. Each role owns one part of the delivery process. |
| Skills | Reusable instructions in `skills/<name>/SKILL.md`. `at-*` harness skills install into your repo under `.agent-team/` and the harness loads one only when a prompt names it. Bot skills (prompt contract, harness CLI, model selection) stay with the bot runtime. See `skills/README.md`. |
| Playbooks | Step-by-step plans for each role's jobs, in `playbooks/<role>/`. The bot runtime picks one per ticket and runs its steps in one harness session. See `playbooks/README.md`. |
| Tools | Scripts and commands the agents call to do the work. `tools/at-ticket/` is the ticket system: a CLI over a SQLite store shared by every worktree, with leases, versioned transitions, an event log and a generated `Backlog.md`. See `tools/at-ticket/README.md`. |
| Bot profiles | One folder per bot at `bot-profiles/<name>/persona.md`, plus any supporting files in that folder. Outside tools, such as Grok, read a folder and copy it to create the bot. |

## How it works

1. **Install** maestro into your project.
2. **Add project context**: what you're building, the stack, build and test commands, conventions.
3. **Run the workflow.** The agents plan, build, check, and ship.

### The agent team

You talk to one agent: the Chief of Staff. It hands big projects to the Planner and writes tickets for small tasks itself. Engineers pick up tickets from the ticket system (`at-ticket`), and their code passes automated checks, a code review, and QA before the Operations Manager releases it. Each role runs on the cheapest model tier that can do the job; color marks the tier.

<!-- Source: docs/agent-team.mermaid. Edit that file, then copy it here. -->
```mermaid
flowchart TD
    User([User])

    CoS["<b>Chief of Staff</b><br/>Frontier"]
    Planner["<b>Planner</b><br/>Mid-tier"]
    Tickets[("<b>Ticket system</b><br/>at-ticket · Backlog.md view<br/>Specs, tickets, status, blockers")]

    subgraph Build ["Build, review and test"]
        subgraph Engineers ["Engineers"]
            SSE["<b>Senior Engineer</b><br/>Frontier"]
            JSE["<b>Junior Engineer</b><br/>Mid-tier"]
        end
        Checks["<b>Automated checks</b><br/>No tokens"]
        CR["<b>Code Reviewer</b><br/>Mid-tier, opposite model<br/>family to the author"]
        QA["<b>QA Lead</b><br/>Mid-tier"]
    end

    Ops["<b>Operations Manager</b><br/>Mid-tier"]
    Logs[("<b>Logs</b><br/>Every agent's handoffs<br/>and token cost")]
    Done([Released])

    %% User and Chief of Staff
    User <-->|"goals, decisions, approvals"| CoS

    %% Planning
    CoS -->|"big project"| Planner
    CoS -->|"small task: writes ticket"| Tickets
    Planner -->|"writes specs and tickets"| Tickets
    Planner -.->|"escalates above its authority"| CoS

    %% Status and blockers flow through the ticket system
    Tickets -.->|"status and blockers:<br/>project tickets"| Planner
    Tickets -.->|"status and blockers:<br/>small tasks, escalations"| CoS

    %% Engineers pick up work
    Tickets -->|"complex or risky"| SSE
    Tickets -->|"routine"| JSE
    JSE -.->|"escalates after 2 failed tries"| SSE
    Engineers -.->|"updates status, flags blockers"| Tickets

    %% Build, review, test
    Engineers -->|"submits code"| Checks
    Checks -->|"passed"| CR
    CR -->|"approved"| QA
    Checks -.->|"failed"| Engineers
    CR -.->|"changes requested"| Engineers
    QA -.->|"failed"| Engineers

    %% Exception path
    CoS -.->|"exception: steps in directly"| Engineers

    %% Release
    QA -->|"passed: ready to release"| Ops
    Ops -.->|"production deploy needs approval"| CoS
    Ops -->|"merges to main and deploys"| Done
    Ops -->|"marks ticket done after release"| Tickets

    %% Operations
    CoS -->|"infrastructure requests"| Ops
    Build -.->|"logs"| Logs
    Logs -->|"reviews costs and failures"| Ops
    Ops -.->|"suggests workflow improvements"| CoS

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

An outside bot runtime, for example Grok Bot, creates a bot for each persona in `bot-profiles/`. Every persona operates under this same anatomy. The bot runtime loads the role's profile (`persona.md`, playbooks, bot skills, optimizations, and shared skills) outside the user's repo. It runs the standard session loop: preflight, starting a headless harness session with launch flags, checking intake, claiming work, prompting steps under the prompt contract, checking structured replies with evidence, batched wrap-up, and cleanup. It enforces guardrails and role-specific permissions while the coding harness does the hands-on work inside the repo.

<!-- Source: docs/bot-anatomy.mermaid. Edit that file, then copy it here. -->
```mermaid
flowchart TD
    Runtime["<b>Bot runtime</b> · outside their repo<br/>Any agent, e.g. a Grok or Claude bot<br/>Thinks, decides and prompts. Never touches files itself<br/><i>How it's started is out of scope</i>"]

    subgraph Profile ["Bot profile: loaded by the bot runtime, never installed into their repo"]
        direction LR
        P1["<b>persona.md</b><br/>Persona and<br/>decision authority"] ~~~ P2["<b>playbooks/</b><br/>Step-by-step plans<br/>for the role's jobs"] ~~~ P3["<b>Bot skills</b><br/>How to prompt and<br/>judge each step"] ~~~ P4["<b>optimizations.md</b><br/>General role tuning,<br/>improved upstream"] ~~~ P5["<b>Shared bot skills</b><br/>harness CLI · prompt contract ·<br/>choosing a model per step"]
    end

    subgraph Loop ["Session loop: identical for every role"]
        B0["<b>Preflight</b><br/>Kill switch set? Stop<br/>Health check: harness logged in, at-ticket doctor passes, git clean<br/>Read .agent-team/environment.md and the trust level"]
        B1["<b>Start a harness session with launch flags</b><br/>Team rules · role permissions · model<br/>Their config files stay untouched<br/>Stable content first, so it gets cached"]
        B2["<b>Prompt: check intake</b> · at-check-intake<br/>Requests → work sent back → new work → scheduled duties<br/>Resume the scratchpad if one exists"]
        B3["<b>Pick the playbook</b><br/>Prompt: claim the work · at-claim"]
        B4["<b>Next step</b><br/>Pick the model tier for this step<br/>Write the prompt, naming the harness skill"]
        B5["<b>Check the structured reply</b><br/>Goal met? Evidence included?"]
        B6{"Result?"}
        B7["<b>Prompt: wrap up</b>, batched · at-wrap-up<br/>Status · handoff note · memory proposals ·<br/>one-line retro: what slowed me down? · log"]
        B8["<b>Clean up</b> · hook if available, else at-clean-up<br/>Scratchpad · temp files · processes · worktree · claim<br/>Then close the harness session"]
        B9{"More in intake?"}
        B10(["Session ends"])
    end

    Contract["<b>Prompt contract</b><br/>1 the goal<br/>2 pointers, not pasted content<br/>3 constraints and permissions<br/>4 what done means<br/>5 reply: short, structured, with evidence<br/><br/>Same session across a playbook's steps ·<br/>fresh session per ticket"]

    subgraph HarnessSide ["Their repo · the harness does all the work"]
        H["<b>Harness session</b>, headless<br/>Claude Code · Codex · Grok Build"]
        Loads["<b>Uses our footprint only</b><br/>.agent-team/ rules, project-setup.md and memory<br/>at-* skills, loaded only when named"]
        Work["<b>Does the hands-on work</b><br/>Ticket system CLI: at-ticket · files and code ·<br/>git on at/ branches · tests and commands"]
    end

    Guard["<b>Guardrails</b><br/>Trust levels: propose-only at first · kill switch ·<br/>role permissions · budgets and retry limits ·<br/>approval gates · secrets never in prompts, replies or logs"]

    subgraph Roles ["Same anatomy, different profiles: what each role lets the harness do"]
        direction LR
        E1["<b>Chief of Staff</b><br/>Read summaries ·<br/>write its own tickets"] ~~~ E2["<b>Planner</b><br/>Full ticket system ·<br/>read-only code"] ~~~ E3["<b>Engineers</b><br/>Edit code on<br/>at/ branches"] ~~~ E4["<b>Code Reviewer</b><br/>Read-only code<br/>and diffs"] ~~~ E5["<b>QA Lead</b><br/>Run tests and the app ·<br/>read-only code"] ~~~ E6["<b>Operations</b><br/>Merge · deploy ·<br/>run the improvement loop"]
    end

    Runtime -->|"loads the profile it's configured for"| Profile
    Profile --> B0
    Guard -.-> B0
    B0 --> B1 --> B2 --> B3 --> B4
    Contract -.->|"shapes every prompt"| B4
    B4 ==>|"prompt"| H
    H ==>|"structured reply"| B5
    B5 --> B6
    B6 -->|"step done, more to do"| B4
    B6 -->|"not right, within budget:<br/>sharper prompt"| B4
    B6 -->|"stuck or over budget:<br/>set Blocked"| B7
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
    class P1,P2,P3,P4 profile
    class P5,Contract shared
    class B0,B1,B2,B3,B4,B5,B6,B7,B8,B9 step
    class H,Work harness
    class Loads ours
    class E1,E2,E3,E4,E5,E6 role
    class Guard note
```

## Supported harnesses

maestro runs on the three main coding agents:

- Claude Code
- Codex
- Grok Build

`AGENTS.md` holds the shared instructions. Codex and Grok Build read it directly. `CLAUDE.md` imports it for Claude Code.

## Status

Early. The workflow exists and works; this repo is where it gets packaged for install. Install steps will land here once packaging is done.
