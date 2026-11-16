package cli

import (
	"strconv"
	"strings"
	"testing"
)

const vsha = "0123456789abcdef0123456789abcdef01234567"

// claim takes the ticket as holder and returns the lease token.
func (h *routeHarness) claim(id, holder string) string {
	h.t.Helper()
	var cl struct{ Claim struct{ Token int64 } }
	h.ok(&cl, "claim", id, "--holder", holder, "--status", "todo,in_progress,in_review")
	return strconv.FormatInt(cl.Claim.Token, 10)
}

// note runs verdict or handoff with the body on stdin.
func (h *routeHarness) note(cmd, id, holder, token, key, body string) (int, string, string) {
	return h.run(body, cmd, id, "--holder", holder, "--token", token, "--body-file", "-", "--idempotency-key", key, "--json")
}

func (h *routeHarness) status(id string) (string, string, int) {
	h.t.Helper()
	var d struct {
		Status   string
		Claim    *struct{ Holder string }
		Comments []struct{}
	}
	h.ok(&d, "get", id, "--full")
	holder := ""
	if d.Claim != nil {
		holder = d.Claim.Holder
	}
	return d.Status, holder, len(d.Comments)
}

// A review's GO posts and moves nothing; a Kickback: posts, sends the
// ticket back and lets it go, in one call; a retry replays both.
func TestVerdictPostsAndMoves(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	tok := h.claim("T-1", "reviewer/r1")

	if code, out, errs := h.note("verdict", "T-1", "reviewer/r1", tok, "k1", "Review: GO at "+vsha+" (PR u), tier 1, gated on g\nVerified: criteria 1\nFor QA: none\n"); code != 0 || !strings.Contains(out, `"kind": "review-go"`) {
		t.Fatalf("go: %d %s %s", code, out, errs)
	}
	if st, holder, n := h.status("T-1"); st != "in_review" || holder != "reviewer/r1" || n != 1 {
		t.Fatalf("after a GO: %s %q %d comments", st, holder, n)
	}

	kick := "Kickback: 2 blockers at " + vsha + "\nVerified: criteria 1\nGates: green on g\n- F1 [blocker, auto-fix] a.go:3: x. Fix: y\n2. **F2** [Blocker, ask-user] b.go:9: which one?\n"
	code, out, errs := h.note("verdict", "T-1", "reviewer/r1", tok, "k2", kick)
	if code != 0 || !strings.Contains(out, `"moved": "in_review -> in_progress"`) {
		t.Fatalf("kickback: %d %s %s", code, out, errs)
	}
	if st, holder, n := h.status("T-1"); st != "in_progress" || holder != "" || n != 2 {
		t.Fatalf("after a kickback: %s %q %d comments", st, holder, n)
	}
	// The same command again: a replay, with no second note or move.
	if code, _, errs := h.note("verdict", "T-1", "reviewer/r1", tok, "k2", kick); code != 0 {
		t.Fatalf("replay: %d %s", code, errs)
	}
	if st, _, n := h.status("T-1"); st != "in_progress" || n != 2 {
		t.Fatalf("after a replay: %s, %d comments", st, n)
	}
	var evs struct {
		Events []struct {
			Type string
			Data struct{ Note string }
		}
	}
	h.ok(&evs, "events", "--ticket", "T-1", "--type", "transitioned")
	if last := evs.Events[len(evs.Events)-1]; last.Data.Note != "Kickback at "+vsha {
		t.Fatalf("move note %q", last.Data.Note)
	}
}

// Only a blocker sends a change back: a Kickback: with a major on it, a
// wrong count or no finding is refused before anything is written, and so
// is a note with no head, an Escalation: with no Return-role:, or a note
// on a ticket that moved on.
func TestVerdictRefuses(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress", "in_review")
	tok := h.claim("T-1", "reviewer/r1")
	for name, c := range map[string]struct{ body, want string }{
		"a major":         {"Kickback: 2 blockers at " + vsha + "\n- F1 [blocker, auto-fix] a.go:1: x\n- F2 [major, auto-fix] a.go:2: y", "take F2 (major) off the Kickback"},
		"the old header":  {"Kickback: 1 blockers, 1 majors at " + vsha + "\n- F1 [blocker, auto-fix] a.go:1: x", "majors and minors go under ## Follow-ups"},
		"a wrong count":   {"Kickback: 3 blockers at " + vsha + "\n- F1 [blocker, auto-fix] a.go:1: x", "counts 3 blockers, and 1 finding lines follow"},
		"no finding":      {"Kickback: 0 blockers at " + vsha, "a Kickback with no blocker is a GO"},
		"no head":         {"Review: GO at HEAD (PR u)", "wants the head's full sha right after"},
		"a short head":    {"QA: pass at abc1234 (PR u), tier 1, on main at " + vsha, "wants the head's full sha right after"},
		"a bold major":    {"Kickback: 1 blockers at " + vsha + "\n- **F1** [blocker, auto-fix] a.go:1: x\n* **F2** [Major, auto-fix] a.go:2: y", "take F2 (major) off the Kickback"},
		"a reason unread": {"Kickback: 1 blockers at " + vsha + "\nGates: red: make test", "no finding line follows"},
		"a zero count":    {"Kickback: 0 blockers at " + vsha + "\nGates: red: make test", "no finding line follows"},
		"no bracket":      {"Kickback: 1 blockers at " + vsha + "\n- F1 [blocker, auto-fix] a.go:1: x\n- F2 major, auto-fix: y", "can't read the severity of F2"},
		"no return role":  {"Escalation: review at " + vsha + "\nWhat happened", "an Escalation: ends with a line"},
		"an unknown note": {"Looks good to me", "want Review: GO at, Kickback:"},
		"a build's note":  {"Handoff: PR u at " + vsha, "the note opens"},
	} {
		code, out, _ := h.note("verdict", "T-1", "reviewer/r1", tok, "k-"+name, c.body)
		if code != 2 || !strings.Contains(out, c.want) {
			t.Errorf("%s: exit %d, want 2 with %q:\n%s", name, code, c.want, out)
		}
	}
	if st, holder, n := h.status("T-1"); st != "in_review" || holder != "reviewer/r1" || n != 0 {
		t.Fatalf("a refusal wrote: %s %q %d comments", st, holder, n)
	}
	// A refusal never hints at a GO while the first line counts blockers.
	if _, out, _ := h.note("verdict", "T-1", "reviewer/r1", tok, "k-hint", "Kickback: 1 blockers at "+vsha+"\nGates: red: make test"); strings.Contains(out, "Review: GO") {
		t.Errorf("a kickback with a reason no line names was told to post a GO: %s", out)
	}
	// No key, no claim.
	if code, _, _ := h.run("Review: GO at "+vsha, "verdict", "T-1", "--holder", "reviewer/r1", "--token", tok, "--body-file", "-"); code != 2 {
		t.Errorf("no key: exit %d", code)
	}
	if code, _, _ := h.run("Review: GO at "+vsha, "verdict", "T-1", "--body-file", "-", "--idempotency-key", "k"); code != 2 {
		t.Errorf("no claim: exit %d", code)
	}
	// Another run's token: the fence refuses, and nothing is posted.
	if code, out, _ := h.note("verdict", "T-1", "reviewer/r2", tok, "k-other", "Review: GO at "+vsha); code != 4 || !strings.Contains(out, "lease_lost") && !strings.Contains(out, "claimed") {
		t.Errorf("another holder: exit %d %s", code, out)
	}
	// A ticket that moved on takes no verdict.
	h.ok(nil, "release", "T-1", "--holder", "reviewer/r1", "--token", tok)
	h.move("T-1", "in_progress")
	tok = h.claim("T-1", "reviewer/r1")
	if code, out, _ := h.note("verdict", "T-1", "reviewer/r1", tok, "k-late", "Review: GO at "+vsha); code != 4 || !strings.Contains(out, "status_conflict") {
		t.Errorf("moved on: exit %d %s", code, out)
	}
	if _, _, n := h.status("T-1"); n != 0 {
		t.Fatalf("%d comments on a ticket that took no verdict", n)
	}
}

// QA's notes: a pass posts only, a fail goes back, an escalation blocks,
// and a kickback at none (no PR) still goes back.
func TestVerdictQAAndEscalation(t *testing.T) {
	h := newRouteHarness(t)
	for i := 0; i < 4; i++ {
		h.ok(nil, "create", "--title", "t"+strconv.Itoa(i))
	}
	for id, c := range map[string]struct{ body, status, holder string }{
		"T-1": {"QA: pass at " + vsha + " (PR u), tier 1, on main at g\nSetup: prod build", "in_review", "qa-lead/q"},
		"T-2": {"Kickback: QA fail at " + vsha + " (PR u), tier 1, on main at g\n- 1 fail: steps", "in_progress", ""},
		"T-3": {"Escalation: QA at " + vsha + ": merge: denied\nQuestion\nReturn-role: engineer\n", "blocked", ""},
		"T-4": {"Kickback: 1 blockers at none\nVerified: none\nGates: not run\n- F1 [blocker, auto-fix] none:0: maestro/T-4 has no PR", "in_progress", ""},
	} {
		h.move(id, "in_progress", "in_review")
		tok := h.claim(id, "qa-lead/q")
		if code, out, errs := h.note("verdict", id, "qa-lead/q", tok, "run-"+id, c.body); code != 0 {
			t.Fatalf("%s: %d %s %s", id, code, out, errs)
		}
		if st, holder, n := h.status(id); st != c.status || holder != c.holder || n != 1 {
			t.Errorf("%s: %s %q %d comments; want %s %q", id, st, holder, n, c.status, c.holder)
		}
	}
}

// handoff posts the engineer's note, sends the ticket to review and lets
// it go; it wants a Handoff: that names a head.
func TestHandoff(t *testing.T) {
	h := newRouteHarness(t)
	h.ok(nil, "create", "--title", "a")
	h.move("T-1", "in_progress")
	tok := h.claim("T-1", "senior-engineer/e1")
	for body, want := range map[string]string{
		"Handoff: PR u, head abc1234":          "names no full head sha",
		"Handoff: PR u\nMerged main " + vsha:   "names no full head sha",
		"Review: GO at " + vsha:                "want Handoff:",
		"Kickback: 1 blockers at " + vsha + "": "want Handoff:",
	} {
		if code, out, _ := h.note("handoff", "T-1", "senior-engineer/e1", tok, "h-bad", body); code != 2 || !strings.Contains(out, want) {
			t.Errorf("%q: exit %d, want 2 with %q: %s", body, code, want, out)
		}
	}
	body := "\n  Handoff: PR https://github.com/o/r/pull/3 at " + vsha + ", branch maestro/T-1\nChecks: test 0\n1. criterion: proof"
	if code, out, errs := h.note("handoff", "T-1", "senior-engineer/e1", tok, "h1", body); code != 0 || !strings.Contains(out, `"kind": "handoff"`) {
		t.Fatalf("handoff: %d %s %s", code, out, errs)
	}
	if st, holder, n := h.status("T-1"); st != "in_review" || holder != "" || n != 1 {
		t.Fatalf("after a handoff: %s %q %d comments", st, holder, n)
	}
	// The note is stored from its first word, as routing reads it.
	var d struct{ Comments []struct{ Body string } }
	h.ok(&d, "get", "T-1", "--full")
	if !strings.HasPrefix(d.Comments[0].Body, "Handoff: PR ") {
		t.Fatalf("stored with its leading blanks: %q", d.Comments[0].Body)
	}
	// The same command again replays, even once the reviewer has taken the
	// ticket and written on it.
	rtok := h.claim("T-1", "reviewer/r1")
	h.run("looking", "comment", "T-1", "--body-file", "-", "--holder", "reviewer/r1", "--token", rtok)
	if code, _, errs := h.note("handoff", "T-1", "senior-engineer/e1", tok, "h1", body); code != 0 {
		t.Fatalf("replay: %d %s", code, errs)
	}
	if _, _, n := h.status("T-1"); n != 2 {
		t.Fatalf("a replay posted again: %d comments", n)
	}
}
