package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

func TestPlannerDirectQARouteAndRereview(t *testing.T) {
	h := newRouteHarness(t)
	h.env["MAESTRO_TICKET_ACTOR"] = "junior-engineer"
	if code, out, errs := h.run("", "create", "--title", "Copy change", "--role", "engineer", "--direct-qa"); code == 0 || !strings.Contains(out+errs, "planner") {
		t.Fatalf("engineer chose direct QA: exit %d: %s %s", code, out, errs)
	}
	h.env["MAESTRO_TICKET_ACTOR"] = "planner"
	h.ok(nil, "create", "--title", "Copy change", "--role", "engineer", "--direct-qa")
	for _, reserved := range []string{" Handoff: PR u at ", "Review: GO at ", "QA: pass at "} {
		if code, _, _ := h.run("", "comment", "T-1", "--body", reserved+vsha); code == 0 {
			t.Fatalf("ordinary comment accepted as proof: %q", reserved)
		}
	}
	h.move("T-1", "in_progress")
	h.env["MAESTRO_TICKET_ACTOR"] = "senior-engineer"
	holder := "senior-engineer/build"
	tok := h.claim("T-1", holder)
	if code, out, errs := h.note("handoff", "T-1", holder, tok, "built", "Handoff: PR u at "+vsha); code != 0 {
		t.Fatalf("handoff: %d %s %s", code, out, errs)
	}
	build := runs.Record{Job: "build", Ticket: "T-1", Command: opusCmd()}
	if got := sends(h.route(build)); !slices.Equal(got, []string{"send qa-lead: qa T-1"}) {
		t.Fatalf("direct handoff: %v", got)
	}
	h.env["MAESTRO_TICKET_ACTOR"] = "qa-lead"
	holder = "qa-lead/check"
	tok = h.claim("T-1", holder)
	base := strings.Repeat("b", 40)
	pass := "QA: pass at " + vsha + " (PR u), tier 0, on main at " + base
	if code, out, errs := h.note("verdict", "T-1", holder, tok, "pass", pass); code != 0 {
		t.Fatalf("QA pass: %d %s %s", code, out, errs)
	}
	var basis struct {
		TestedBase string `json:"tested_base"`
	}
	h.ok(&basis, "qa-basis", "T-1")
	if basis.TestedBase != base {
		t.Fatalf("QA pass tested base %q, want %s", basis.TestedBase, base)
	}
	if code, out, errs := h.note("verdict", "T-1", holder, tok, "needs-review", "Rereview: QA at "+vsha+": the action also sends mail"); code != 0 {
		t.Fatalf("rereview: %d %s %s", code, out, errs)
	}
	_, _, count := h.status("T-1")
	if code, out, errs := h.note("verdict", "T-1", holder, tok, "pass", pass); code != 0 {
		t.Fatalf("original receipt: %d %s %s", code, out, errs)
	}
	if _, _, after := h.status("T-1"); after != count {
		t.Fatal("receipt retry wrote another verdict")
	}
	if code, _, _ := h.note("verdict", "T-1", holder, tok, "later-pass", pass); code == 0 {
		t.Fatal("a withdrawn QA basis authorized a new pass")
	}
	if got := sends(h.route(build)); !slices.Equal(got, []string{"send reviewer: review T-1"}) {
		t.Fatalf("replayed handoff after QA requested review: %v", got)
	}
}
