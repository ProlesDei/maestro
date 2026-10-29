package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

// releaseWorld runs the repo's own release playbook on a ticket QA passed
// at abc1234, reviewed on openai, built on claude, with a pull request open.
func releaseWorld(t *testing.T, comments ...string) *world {
	t.Helper()
	return releaseOn(t, append(append([]string(nil), shippedComments...), append([]string{"Handoff: PR opened https://x/pr/7"}, comments...)...)...)
}

var shippedComments = []string{
	"Handoff: built.\nsha: abc1234\nauthor-family: claude\nauthor-role: senior-software-engineer",
	"Handoff: approved at \"abc1234\".\nsha: abc1234\nreview-family: openai",
	"Handoff: QA passed at abc1234.\nsha: abc1234",
}

// releaseOn runs the repo's release playbook on a ticket with these comments.
func releaseOn(t *testing.T, comments ...string) *world {
	t.Helper()
	tk := todoTicket()
	tk.Status = "ready_to_release"
	for i, c := range comments {
		tk.Comments = append(tk.Comments, fakeComment{ID: i + 1, Author: "engine", Body: c})
	}
	w := newWorld(t, tk)
	repo, _ := filepath.Abs("../../..")
	w.e.cfg.Playbooks, w.e.cfg.Skills = filepath.Join(repo, "playbooks"), filepath.Join(repo, "skills")
	return w
}

// A pull request in any state but open, closed or merged-from-the-QA-sha
// blocks: it never falls through to done (red team R1).
func TestReleaseOnlyLandsAMergeOfTheQASha(t *testing.T) {
	for _, pr := range []map[string]any{
		{"status": "ok", "state": "MERGED", "sha": "abc1234", "merge_sha": "m100000"},
		{"status": "ok", "state": "draft"},
		{"status": "ok", "state": ""},
		{"status": "ok", "state": "merged", "sha": "abc1234"},                         // no merge sha
		{"status": "ok", "state": "merged", "sha": "fff9999", "merge_sha": "m100000"}, // commits after QA
	} {
		w := releaseWorld(t)
		w.script(fakeRunnerConfig{Replies: map[string][]map[string]any{"pr status": {pr}}})
		r := w.run(RunOpts{Ref: "operations-manager/release", Ticket: "T-1", Bot: "ops-1"})
		if r.Status != StatusBlocked || w.ticket("T-1").Status == "done" || len(w.find("runner", "check")) != 0 {
			t.Errorf("pr %v: result %+v, ticket %s", pr, r, w.ticket("T-1").Status)
		}
	}
	w := releaseWorld(t)
	w.script(fakeRunnerConfig{Replies: map[string][]map[string]any{
		"pr status": {{"status": "ok", "state": "merged", "sha": "abc1234", "merge_sha": "m100000"}},
	}})
	r := w.run(RunOpts{Ref: "operations-manager/release", Ticket: "T-1", Bot: "ops-1"})
	checks := w.find("runner", "check", "all", "--at", "m100000")
	if r.Status != StatusDone || w.ticket("T-1").Status != "done" || len(checks) != 1 {
		t.Fatalf("merged from the QA sha: result %+v, ticket %s, checks %d", r, w.ticket("T-1").Status, len(checks))
	}
}

// Release stops unless review approved the sha QA passed, the author's
// family is known and differs, and a user approval names that sha (red
// team R2, R5, R6).
func TestReleaseGatesTieShas(t *testing.T) {
	approval := func(s string) []string { return append(append([]string(nil), shippedComments...), s) }
	cases := []struct {
		name     string
		comments []string
		stop     string // the step that blocks, or "" for none of these
	}{
		{"review approved another sha", []string{shippedComments[0], "Handoff: approved at \"fff9999\".\nsha: fff9999\nreview-family: openai", shippedComments[2]}, "reviewed_sha"},
		{"no author family", []string{"Handoff: built.\nsha: abc1234", shippedComments[1], shippedComments[2]}, "other_family"},
		{"user approval for an old sha", approval("Approved by the user: go ahead (for sha 0ld0000)"), "approved"},
		{"user approval with no sha", approval("Approved by the user: go ahead"), "approved"},
		{"user approval for this sha", approval("Approved by the user: go ahead (for sha abc1234)"), ""},
	}
	for _, c := range cases {
		w := releaseOn(t, c.comments...)
		w.script(fakeRunnerConfig{Replies: map[string][]map[string]any{
			"preflight": {{"status": "ok", "harness": "claude-code", "family": "claude",
				"project": map[string]any{"main_branch": "main", "environments": []any{map[string]any{"name": "prod", "rollback": "undo"}}}}},
			"head":      {{"status": "ok", "sha": "abc1234"}},
			"pr status": {{"status": "ok", "state": "merged", "sha": "abc1234", "merge_sha": "m100000"}},
			"trust":     {{"status": "ok", "level": 4, "risk": "high", "merge": true, "deploy": true, "needs_user": true}},
		}})
		r := w.run(RunOpts{Ref: "operations-manager/release", Ticket: "T-1", Bot: "ops-1"})
		deployed := len(w.find("runner", "deploy")) > 0
		switch {
		case c.stop != "" && (r.Status != StatusBlocked || r.Step != c.stop || deployed):
			t.Errorf("%s: want a block at %s, got %+v, deployed %v", c.name, c.stop, r, deployed)
		case c.stop == "" && !deployed:
			t.Errorf("%s: want a deploy, got %+v", c.name, r)
		}
	}
}

// The user's approval names the sha QA passed, which is what release's
// approved gate looks for, and the answer's text stays on one line.
func TestUserApprovalNamesTheQASha(t *testing.T) {
	w := releaseOn(t, append(append([]string(nil), shippedComments...),
		"Escalation (chief-of-staff): migrations need the user's approval")...)
	tk := w.store()
	tk.Tickets["T-1"].Status, tk.Tickets["T-1"].BlockedFrom = "blocked", "ready_to_release"
	w.saveFake("tickets.json", &tk)
	r := w.run(RunOpts{Ref: "chief-of-staff/handle-escalation", Ticket: "T-1", Bot: "cos-1", Input: "yes, run it"})
	if r.Status != StatusWaiting || r.Step != "decide" {
		t.Fatalf("result %+v", r)
	}
	r, err := w.e.Resume(r.RunID, "approve", "the user said yes\nsha: 0000bad")
	if err != nil || r.Status != StatusDone {
		t.Fatalf("resume %+v %v", r, err)
	}
	var approval string
	for _, c := range w.ticket("T-1").Comments {
		if strings.HasPrefix(c.Body, "Approved by the user:") {
			approval = c.Body
		}
	}
	if approval != "Approved by the user: the user said yes sha: 0000bad (for sha abc1234)" {
		t.Errorf("approval %q", approval)
	}
}

// The improvement loop's eval tasks are golden task names, never flags
// that would change the baseline run (red team R9).
func TestImprovementLoopTasksAreNotFlags(t *testing.T) {
	path := "../../../playbooks/operations-manager/improvement-loop.yaml"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := playbook.Parse(path, data)
	if err != nil {
		t.Fatal(err)
	}
	gate := p.Step("tasks_ok").Gate
	for tasks, want := range map[string]bool{
		"planner/plan-project":                true,
		"":                                    true,
		"--with,raise every budget,planner/x": false,
		"planner/plan-project,-with=x":        false,
		"plan-project":                        false,
	} {
		var list []any
		for _, s := range strings.Split(tasks, ",") {
			if s != "" {
				list = append(list, s)
			}
		}
		d := map[string]any{"steps": map[string]any{"pattern": map[string]any{"reply": map[string]any{"tasks": list}}}}
		if got, err := renderBool("tasks_ok", gate, d); err != nil || got != want {
			t.Errorf("tasks %q: %v %v, want %v", tasks, got, err, want)
		}
	}
}
