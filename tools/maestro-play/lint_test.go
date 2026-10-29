// Package maestroplay_test checks the repo's own playbooks, prompt
// templates and skills/use.yaml, so go test here is the playbook check.
package maestroplay_test

import (
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-play/engine"
	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

const playbooks, skills = "../../playbooks", "../../skills"

func TestRepoPlaybooksLint(t *testing.T) {
	res, err := engine.Lint(playbooks, skills)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Problems {
		t.Error(p.String())
	}
	if res.Playbooks == 0 {
		// Say so rather than pass on nothing: the YAML playbooks come with
		// slices 4 and 5 of 0700.
		t.Skip("no playbooks/<role>/*.yaml to lint yet")
	}
	t.Logf("%d playbooks linted", res.Playbooks)
}

// TestRepoPromptsRender renders every prompt step, the done note and the
// escalation of every playbook on a tiny-cli ticket (0700 C's merge gate).
func TestRepoPromptsRender(t *testing.T) {
	e, err := engine.New(engine.Config{Playbooks: playbooks, Skills: skills, Common: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, role := range playbook.Roles {
		pbs, err := playbook.LoadRole(playbooks, role)
		if err != nil {
			if strings.Contains(err.Error(), "no playbooks") {
				continue
			}
			t.Fatal(err)
		}
		for _, p := range pbs {
			steps := []string{"done", "escalate"}
			for _, s := range p.Steps {
				if s.Kind() == playbook.KindPrompt {
					steps = append(steps, s.ID)
				}
			}
			for _, step := range steps {
				n++
				_, err := e.Render(engine.RenderOpts{Ref: p.Ref(), Step: step,
					TicketFile: "testdata/tiny-cli-ticket.json", EnvFile: "testdata/tiny-cli-env.json"})
				if err != nil {
					t.Errorf("render %s --step %s: %v", p.Ref(), step, err)
				}
			}
		}
	}
	if n == 0 {
		t.Skip("no playbooks/<role>/*.yaml to render yet")
	}
	t.Logf("%d renders", n)
}
