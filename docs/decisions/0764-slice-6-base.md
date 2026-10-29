# 0764 Slice 6 sits on ship/wave2

- **Context:** Slice 6 started from `int/p2a-p45` plus 0700, but the merge target also holds track B, the wake hook and `maestro-usage` (`ship/wave2`), which edit `evals/check.py`, `AGENTS.md` and `playbooks/README.md`, all slice 6 files.
- **Choice:** Merge `ship/wave2` into `pb/s6` before editing, as `pb/s2` did, so slice 6's rewrites of those files already include track B (`--deps`, `--areas`, `waiting_on_deps`, `area_busy`, keepalive claims, token-fenced release).
- **Alternatives:** Edit on the old base and resolve the three conflicts at merge.
- **Why:** The rewrites replace most of those files, so a conflict there would be resolved by hand anyway; doing it once, now, is safer.
