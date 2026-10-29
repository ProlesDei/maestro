# 0401 Bot and harness skills in separate folders, all marked internal

- **Context:** G4.3: bot and harness skills shared `skills/`, and none had an internal or manual-only flag. The brief asks for `bot-skills/` and `harness-skills/` "or equivalent". This run may only edit inside `skills/`.
- **Choice:** `skills/bot/<name>/` and `skills/harness/<name>/`, each with its own `_template/`. Every skill sets `metadata.internal: true`, the flag third-party installers such as `npx skills` use to skip a skill. Harness skills also set `disable-model-invocation: true` (Claude Code and Grok Build) and ship `agents/openai.yaml` with `policy.allow_implicit_invocation: false` (Codex), as 0140 and 0143 found. Keep the `maestro-` prefix on harness skills.
- **Alternatives:** Top-level `bot-skills/` and `harness-skills/` (outside this run's paths, and it splits one format README in two); prefix only, one folder (the brief asks for a split); let the installer add the Codex file (then the source doesn't show what gets installed).
- **Why:** The folder says who reads a skill, the prefix still says it at a glance in a harness's skill list, and the flags travel with the folder installers copy.
