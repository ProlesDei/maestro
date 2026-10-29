# 0433 Owners are roles; no-code tickets are scouts

- **Context:** 0202 stores an owner, and only it may set a scout ticket done, as `--actor`. The playbooks wrote `Owner: <role>` in the body, and had the Operations Manager set an infrastructure request done straight from `in_progress`, which the store refuses for ship tickets.
- **Choice:** `create --owner <role>` replaces the body line, and the owner closes a scout with `--actor <role>`. Investigations, `Plan:` tickets and workflow proposals are scouts owned by their reader. An infrastructure request ships no code, so it's a scout owned by `operations-manager`: `in_progress` → `in_review` → `done`. When it turns into repo changes, the engineer ticket is a ship ticket owned by `planner`.
- **Alternatives:** Owner as a bot id (the creator rarely knows which bot will read it); keep infrastructure requests as ship tickets (they'd need a fake review, QA and release to reach done).
- **Why:** The rules in 0009 and 0423 hold with no change to the store.
