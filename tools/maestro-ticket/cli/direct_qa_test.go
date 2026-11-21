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
	if code, out, errs := h.note("verdict", "T-1", holder, tok, "needs-review", "Rereview: QA at "+vsha+": the action also sends mail"); code != 0 {
		t.Fatalf("rereview: %d %s %s", code, out, errs)
	}
	if got := sends(h.route(build)); !slices.Equal(got, []string{"send reviewer: review T-1"}) {
		t.Fatalf("replayed handoff after QA requested review: %v", got)
	}
}
