---
name: maestro-draft-setup
description: Draft a project file from the repo itself, with a source for every value and an ask list, for project-setup.md, its definition of done, or the verification guide verify.md; use it at setup and when either is redone.
disable-model-invocation: true
metadata:
  internal: true
---

# Draft a project file

You fill in one of Maestro's project files from what the repo already says, so the user only answers what the repo can't.

## Inputs

- Which draft: `project-setup` (`.maestro/project/project-setup.md`), `definition-of-done` (its "Definition of done" section), or `verify` (`.maestro/project/verify.md`).
- The draft's path in `$(git rev-parse --git-common-dir)/maestro/scratch/<bot-id>/`, and the user's answers when applying them.

## Steps

1. **Read** only the repo's own sources: README, build files, CI config, pull request template, CONTRIBUTING, docs, and branch protection or coverage settings when the prompt gives them.
2. **project-setup:** name, summary, main branch, code host; automated checks (build, lint, test commands from CI or the build files); how to run the app; release (environments, deploy, smoke check, rollback); board settings.
3. **definition-of-done:** a table per ticket type (feature, bug, chore, docs, investigation): each item, how it's checked (a command, evidence or judgment), and its source.
4. **verify:** how to build, start and stop the app; what it needs (services, seed data, environment variables by name); how to drive it (browser, CLI, HTTP); how to observe results (logs, screenshots, output); that evidence goes in `$(git rev-parse --git-common-dir)/maestro/evidence/<ticket-id>/`; how to stop everything it starts. Something it needs isn't available: reply `blocked`.
5. **Every value** cites its source path. Anything unknown is marked `ask`, with a recommended answer.
6. **Applying answers:** put each one in and remove its `ask`. Change nothing else, and reply with the diff against the previous draft.

## Reply

```yaml
status: done
summary: project-setup draft; 2 asks
evidence:
  - read package.json, .github/workflows/ci.yml, README.md
draft: scratch/cos-1/project-setup.md
ask: ["Production deploy needs your approval? (recommend: yes)"]
next: confirm with the user
questions: []
```

## Rules

- Write only the draft in the scratch dir. The runner's installer writes the real file.
- Never edit the user's files.
