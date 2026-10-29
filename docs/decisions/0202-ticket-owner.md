# 0202 Tickets record an owner

- **Context:** 0009 says a scout ticket's owner marks it done after reading the report. The store had no owner, only `created_by`, and the Planner often creates tickets for someone else.
- **Choice:** Add `owner`, set at create with `--owner`, defaulting to the actor. A scout ticket must have a real owner (not `unknown`), or create fails with exit 2. Owner can't be changed yet. v1 tickets get `owner = created_by`.
- **Alternatives:** Use `created_by` as the owner (wrong when the Planner files a scout for the Chief of Staff); let anyone close scouts.
- **Why:** One explicit field, cheap now, and it makes the 0009 gate checkable.
