# maestro

An automated agentic workflow that takes products from plan to ship, packaged so you can install it into any project.

maestro bundles a full suite of agent roles, skills, and tools. Install it, give it context about your project, and the workflow is ready to run. No per-project setup of agents, prompts, or tooling.

## What's included

| Part | What it is |
|---|---|
| Roles | Agent definitions. Each role owns one part of the delivery process. |
| Skills | Reusable instructions agents load for specific tasks. |
| Tools | Scripts and commands the agents call to do the work. |
| Bot profiles | Bot definitions in `bot-profiles/` that outside tools, such as Grok, read and copy to create bots. |

## How it works

1. **Install** maestro into your project.
2. **Add project context**: what you're building, the stack, build and test commands, conventions.
3. **Run the workflow.** The agents plan, build, check, and ship.

### The agent team

You talk to one agent: the Chief of Staff. It hands big projects to the Planner and writes tickets for small tasks itself. Engineers pick up tickets from `tickets/`, and their code passes automated checks, a code review, and QA before the Operations Manager releases it. Each role runs on the cheapest model tier that can do the job; color marks the tier.

<!-- Source: docs/agent-team.mermaid. Edit that file, then copy it here. -->
```mermaid
flowchart TD
    User([User])

    CoS["<b>Chief of Staff</b><br/>Frontier"]
    Planner["<b>Planner</b><br/>Mid-tier"]
    Tickets[("<b>tickets/</b><br/>Specs, tickets, status, blockers")]

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

    %% Status and blockers flow through the tickets folder
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

## Supported harnesses

maestro runs on the three main coding agents:

- Claude Code
- Codex
- Grok Build

`AGENTS.md` holds the shared instructions. Codex and Grok Build read it directly. `CLAUDE.md` imports it for Claude Code.

## Status

Early. The workflow exists and works; this repo is where it gets packaged for install. Install steps will land here once packaging is done.
