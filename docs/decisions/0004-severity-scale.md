# 0004 Severity scale for audits

- **Context:** The review procedure uses blocker / major / minor / nit. The Phase 0 request asked for must-fix / should / later.
- **Choice:** **must-fix** = breaks a settled decision or a guarantee later phases rely on (gate bypass, double work, silent lost leases), or blocks a phase's done criteria; maps to blocker/major. **should** = real defect with a workaround or small blast radius; maps to minor. **later** = belongs to a future phase by design, or a nit.
- **Alternatives:** Keep the four-level scale.
- **Why:** Three levels are enough to order the work. Owning phase carries the "when".
