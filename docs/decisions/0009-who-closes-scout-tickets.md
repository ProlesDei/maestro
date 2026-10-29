# 0009 Who sets Done on scout tickets

- **Context:** The brief says only the Operations Manager sets Done, after release. It also says scout tickets end at an evidenced report with no review, QA or release. Both can't hold as written.
- **Choice (proposed):** Scouts go In progress → In review (the ticket owner reads the report) → Done, set by the Operations Manager like every other ticket. The store drops the `in_progress → done` shortcut, and lets a scout move In review → Done without QA or release.
- **Alternatives:** Let the ticket owner set Done on scouts; let the store allow `in_progress → done` for scouts.
- **Why:** One rule for Done is simpler to enforce in the store than an exception. **Status: needs Paul** (refines a settled decision).
