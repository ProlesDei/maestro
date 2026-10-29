# 0427 `evals/check.py` is the one docs check

- **Context:** Phase 4's doc check (links, backticked repo paths, README diagram embeds, skill flags) lived in `/tmp` because Phase 4 couldn't write to `tools/` or `evals/`. Phase 5's `evals/check.py` checked playbooks and tasks.
- **Choice:** Fold Phase 4's checks into `evals/check.py`: skill frontmatter (name, `metadata.internal`, harness flags, `agents/openai.yaml`, prefix, and descriptions YAML can read), links and repo paths in every Markdown file outside `tools/`, `docs/decisions/` and `HANDOFF.md`, and the README embeds. Harness steps must name a skill in `skills/harness/`, bot steps one in `skills/bot/`. README and AGENTS.md point to it.
- **Alternatives:** A second script under `docs/` (two commands to remember); a Go checker in `tools/` (a module for a Markdown check).
- **Why:** One command gates every Markdown change.
