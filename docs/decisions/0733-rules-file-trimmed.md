# 0733 The session rules move to playbooks/rules.md and lose the skill and reply rules

- **Context:** 0700 B moves `skills/harness/rules.md` to `playbooks/rules.md`, passed by the engine, never installed, and drops its skill rules.
- **Choice:** Dropped: "when the prompt names a skill, follow it", "write memory only through `maestro-propose-memory`" (now the reply's `memory` field), the ticket-write rules (0732 covers heartbeat and lease loss, since sessions write nothing), and the Replies section, which `prompts/_contract.md` now says once. The `.maestro/rules.md` path is gone from the edit ban, since the file no longer exists in the repo.
- **Alternatives:** Keep the reply rules in both files (two copies of one rule).
- **Why:** One rule, one file. The contract renders first in every prompt, and the rules file holds only scope, memory, secrets and the stop file.
