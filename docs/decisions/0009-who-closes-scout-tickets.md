# 0009 Who sets Done on scout tickets

- **Context:** The brief says only the Operations Manager sets Done, after release. It also says scout tickets end at an evidenced report with no review, QA or release. Both can't hold as written.
- **Choice:** The rule that only the Operations Manager sets Done applies only to tickets that ship code. A scout or research ticket with no QA goes In progress → In review, and its owner marks it Done once they have read the report.
- **Alternatives:** Have the Operations Manager set Done on scouts too, after the owner reads the report (the earlier proposal); let the store allow `in_progress → done` for scouts.
- **Why:** Scouts ship nothing, so there is no release for the Operations Manager to check. **Status: resolved by Paul** (2026-10-28).
