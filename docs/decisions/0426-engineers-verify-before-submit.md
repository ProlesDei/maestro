# 0426 Engineers run `maestro-verify` before they submit

- **Context:** 0419 asked the engineers' submit steps to use `maestro-verify`. Phase 5's playbooks submitted once the checks passed.
- **Choice:** Every engineer playbook that submits has a Verify step after the checks. A failure goes back to the build step and isn't a try. The handoff lists the evidence paths, and QA still verifies on its own checkout.
- **Alternatives:** Leave verification to QA alone (more QA send-backs, each costing a try).
- **Why:** Most failures are caught before they cost a review and a QA session.
