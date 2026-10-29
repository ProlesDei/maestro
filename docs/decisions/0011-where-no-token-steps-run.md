# 0011 No-token steps run in a runner, not the bot

- **Context:** The brief says bots never touch files and mechanical steps use "a cheap model or a script". It doesn't say who runs the script. Today `skills/model-selection/SKILL.md:18` has the bot runtime run commands in the repo, which is a bot touching the repo.
- **Choice (default):** A deterministic runner runs every no-token step: the same wrapper that launches harness sessions and records usage (Phase 3). It takes a named command from an allowlist per role and trust level, runs it in the ticket's worktree, and returns the exit code and trimmed output. The bot only picks the step and judges the result. Phase 4 rewrites `model-selection` and the 28 playbook steps to match.
- **Alternatives:** Send every mechanical step to a harness session on a cheap model (costs tokens); keep the bot runtime running commands (breaks a settled decision).
- **Why:** Zero tokens, the same allowlist and logs as harness calls, and the bot still touches nothing.
