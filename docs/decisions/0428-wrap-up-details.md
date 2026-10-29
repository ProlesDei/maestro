# 0428 Wrap-up details the playbooks need from the skills

- **Context:** The playbooks needed things the Phase 4 skills didn't do: add a try on a send-back, hand off with no status change (release at trust level 1, a Chief of Staff waiting on the user), answer with `Approved:`, and route both escalation prefixes.
- **Choice:** `maestro-handoff-note` takes `--add-try` and `keep` (release the lease, no transition), starts with `Approved:` / `Declined:` when it answers an escalation, and writes `Tries` and `Skipped:` lines. `maestro-escalate` takes the prefix from the bot. `maestro-check-intake` routes `Escalation (chief-of-staff):` to the Chief of Staff, gives QA its `todo` tickets, and lists Ops's curation and housekeeping duties. `model-selection` reads the **Model:** line.
- **Alternatives:** Extra wrap-up skills per case (more near-copies, against 0421).
- **Why:** The playbooks run as written, on the skills that exist.
