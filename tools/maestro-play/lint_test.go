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
	if _, err := playbook.Load(playbooks, "engineer/work"); err != nil {
		t.Fatalf("engineer/work, the playbook /maestro-work runs: %v", err)
	}
	res, err := engine.Lint(playbooks, skills)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Problems {
		t.Error(p.String())
	}
	t.Logf("%d playbooks linted", res.Playbooks)
}

// TestRepoPromptsRender renders every prompt step, the done note and the
// escalation of every playbook on a tiny-cli ticket, once as new work and
// once sent back with a Kickback note.
func TestRepoPromptsRender(t *testing.T) {
	e, err := engine.New(engine.Config{Playbooks: playbooks, Skills: skills, Common: t.TempDir(), Dir: t.TempDir()})
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
			for _, ticket := range []string{"testdata/tiny-cli-ticket.json", "testdata/tiny-cli-kickback.json"} {
				for _, step := range steps {
					n++
					_, err := e.Render(engine.RenderOpts{Ref: p.Ref(), Step: step, TicketFile: ticket, EnvFile: "testdata/tiny-cli-env.json"})
					if err != nil {
						t.Errorf("render %s --step %s on %s: %v", p.Ref(), step, ticket, err)
					}
				}
			}
		}
	}
	t.Logf("%d renders", n)
}
