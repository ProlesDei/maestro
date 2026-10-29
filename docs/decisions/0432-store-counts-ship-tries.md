# 0432 The store counts tries on ship tickets only

- **Context:** 0428 gave `maestro-handoff-note` an `--add-try` flag, and 0502 has the reviewer's or QA's wrap-up add the try. 0201 has the store count send-backs itself, and the CLI has no `--add-try`, so the wrap-up would exit 2. 0502 also says an owner sending a scout report back is not a try, but the store counted it.
- **Choice:** Drop `--add-try`. The store adds the try on `in_review → in_progress` and `in_qa → in_progress`, for ship tickets only. This replaces the `--add-try` part of 0428 and the `add_try` part of 0502.
- **Alternatives:** Count scout send-backs too (a Senior Engineer could be blocked by an owner's questions on a report, against 0502).
- **Why:** One counter, kept by the move itself, with the numbers 0502 asks for.
