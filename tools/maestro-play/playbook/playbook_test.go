package playbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	src := `name: x
role: chief-of-staff
description: d
intake: {category: request, status: "todo, blocked"}
steps:
  - id: a
    when: '{{true}}'
    run: summary
  - id: b
    when: {if: '{{false}}', reason: why}
    ask: {question: q, choices: [y, n]}
`
	p, err := Parse("x.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Intake[0].Status, ",") != "todo,blocked" {
		t.Errorf("status %q", p.Intake[0].Status)
	}
	if p.Steps[0].When.If != "{{true}}" || p.Steps[1].When.Reason != "why" {
		t.Errorf("when %+v %+v", p.Steps[0].When, p.Steps[1].When)
	}
	if p.Steps[0].Kind() != KindRun || p.Steps[1].Kind() != KindAsk || p.Ref() != "chief-of-staff/x" || p.Index("b") != 1 {
		t.Errorf("kinds %q %q", p.Steps[0].Kind(), p.Steps[1].Kind())
	}
	for _, bad := range []string{
		"name: x\nsteps:\n  - id: a\n    when: {if: x, unless: y}\n",
		"name: x\nnope: 1\n",
		"name: x\nintake: {status: {a: b}}\n",
	} {
		if _, err := Parse("bad.yaml", []byte(bad)); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestFindRequest(t *testing.T) {
	for words, want := range map[string]string{
		"check all": "check", "worktree add T-1": "worktree add", "pr status T-1": "pr status",
		"session start --x": "session start", "open pr --title x": "open pr", "head": "head", "resume": "", "merge": "", "frob": "",
	} {
		r := FindRequest(strings.Fields(words))
		got := ""
		if r != nil {
			got = r.Name
		}
		if got != want {
			t.Errorf("%q: %q, want %q", words, got, want)
		}
	}
}

func TestDefaults(t *testing.T) {
	if Prefix("owner") != "Escalation:" || Prefix("chief-of-staff") != "Escalation (chief-of-staff):" {
		t.Errorf("prefixes")
	}
	for to, want := range map[string]string{"": "blocked", "owner": "blocked", "chief-of-staff": "blocked", "engineer": "todo"} {
		if got := (Block{}).MoveTo(to); got != want {
			t.Errorf("MoveTo(%q) = %q, want %q", to, got, want)
		}
	}
	if (Block{Status: "keep"}).MoveTo("reviewer") != "keep" {
		t.Errorf("status overrides")
	}
	if e, to, err := (&Wait{}).Durations(); err != nil || e.String() != "1m0s" || to.String() != "30m0s" {
		t.Errorf("wait defaults %v %v %v", e, to, err)
	}
	if _, err := (&Ask{Timeout: "48h"}).LeaseTTL(); err == nil {
		t.Errorf("48h lease should fail")
	}
	if (&Intake{Role: "any"}).TicketRole("reviewer") != "" || (&Intake{}).TicketRole("reviewer") != "reviewer" {
		t.Errorf("intake role")
	}
}

func TestUses(t *testing.T) {
	dir := t.TempDir()
	u, err := LoadUses(dir)
	if err != nil || len(u.PromptSkills("build")) != 0 {
		t.Fatalf("missing use.yaml: %v %v", u, err)
	}
	os.WriteFile(filepath.Join(dir, "use.yaml"), []byte("prompts:\n  all: [a]\n  build: [b, a]\n"), 0o644)
	u, err = LoadUses(dir)
	if err != nil || strings.Join(u.PromptSkills("build"), ",") != "a,b" || strings.Join(u.PromptSkills("plan"), ",") != "a" {
		t.Fatalf("uses %+v %v", u, err)
	}
	os.WriteFile(filepath.Join(dir, "use.yaml"), []byte("prompts:\n  all: [a, work]\n  build: [b]\ncommands: [work]\n"), 0o644)
	u, err = LoadUses(dir)
	if err != nil || strings.Join(u.PromptSkills("build"), ",") != "a,b" || strings.Join(u.Commands, ",") != "work" {
		t.Fatalf("a command is never named in a prompt: %+v %v", u, err)
	}
	os.WriteFile(filepath.Join(dir, "use.yaml"), []byte("prompt: {}\n"), 0o644)
	if _, err := LoadUses(dir); err == nil {
		t.Errorf("unknown key should fail")
	}
}
