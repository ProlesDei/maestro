package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyTree copies the fixture repo so a test can add a bad file to it.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLintFixturesClean(t *testing.T) {
	res, err := Lint("testdata/repo/playbooks", "testdata/repo/skills")
	if err != nil {
		t.Fatal(err)
	}
	if res.Playbooks != 11 || len(res.Problems) != 0 {
		t.Fatalf("%d playbooks, problems: %v", res.Playbooks, res.Problems)
	}
}

func TestLintCatches(t *testing.T) {
	head := "name: bad\nrole: senior-software-engineer\ndescription: d\nclaim: true\n"
	cases := []struct {
		name, yaml, want string
	}{
		{"unknown key", head + "steps:\n  - id: a\n    ticket: get {{.ticket.id}}\n    retries: 2\n", "field retries not found"},
		{"misspelled step id", head + "steps:\n  - id: a\n    gate: '{{eq .steps.bulid.outcome \"ok\"}}'\n", "unknown field .steps.bulid"},
		{"misspelled reply field", head + "steps:\n  - id: a\n    prompt: plan\n    out: [plan]\n  - id: b\n    gate: '{{if .steps.a.reply.plna}}true{{else}}false{{end}}'\n", "unknown field .steps.a.reply.plna"},
		{"unknown ticket field", head + "steps:\n  - id: a\n    ticket: get {{.ticket.idd}}\n", "unknown field .ticket.idd"},
		{"no step type", head + "steps:\n  - id: a\n", "want exactly one of"},
		{"two step types", head + "steps:\n  - id: a\n    run: check all\n    gate: 'true'\n", "want exactly one of"},
		{"bad id", head + "steps:\n  - id: Write-Tests\n    run: check all\n", "id \"Write-Tests\""},
		{"duplicate id", head + "steps:\n  - id: a\n    run: check all\n  - id: a\n    run: check all\n", "duplicate id"},
		{"unknown request", head + "steps:\n  - id: a\n    run: deploy-everything now\n", "not a runner request"},
		{"engine request", head + "steps:\n  - id: a\n    run: session start\n", "the engine sends this itself"},
		{"role not allowed", head + "steps:\n  - id: a\n    run: merge {{.ticket.id}}\n", "merge: not allowed for senior-software-engineer"},
		{"ticket claim", head + "steps:\n  - id: a\n    ticket: claim {{.ticket.id}}\n", "the engine claims"},
		{"engine flag", head + "steps:\n  - id: a\n    ticket: comment {{.ticket.id}} --body x --token 3\n", "--token is the engine's"},
		{"engine flag, one dash", head + "steps:\n  - id: a\n    ticket: transition {{.ticket.id}} --to done -actor-role=operations-manager\n", "--actor-role is the engine's"},
		{"gate not a bool", head + "steps:\n  - id: a\n    run: check all\n  - id: b\n    gate: '{{or .steps.a.results .steps.a.results}}'\n", `gate rendered "[]" on empty data`},
		{"runner flag", head + "steps:\n  - id: a\n    run: check all --role qa-lead\n", "the engine sends --role itself"},
		{"bad subcommand", head + "steps:\n  - id: a\n    ticket: frob\n", "ticket \"frob\""},
		{"on target", head + "steps:\n  - id: a\n    run: check all\n    on: {fail: nowhere}\n", "no step \"nowhere\""},
		{"on outcome", head + "steps:\n  - id: a\n    run: check all\n    on: {done: next}\n", "\"done\" isn't an outcome of a run step"},
		{"exit outcome ok", head + "steps:\n  - id: a\n    run: check all\n    on: {exit:3: next, exit:x: next}\n", "\"exit:x\" isn't an outcome"},
		{"wait on a prompt", head + "steps:\n  - id: a\n    prompt: plan\n  - id: b\n    wait: {step: a, until: 'true'}\n", "want a run or ticket step"},
		{"wait duration", head + "steps:\n  - id: a\n    run: check all\n  - id: b\n    wait: {step: a, until: 'true', every: soon}\n", "every \"soon\""},
		{"ask no choices", head + "steps:\n  - id: a\n    ask: {question: q, choices: []}\n", "want one or more"},
		{"missing prompt", head + "steps:\n  - id: a\n    prompt: nope\n", "prompt \"nope\": no"},
		{"partial prompt", head + "steps:\n  - id: a\n    prompt: _contract\n", "partials are added by the engine"},
		{"bad tier", head + "steps:\n  - id: a\n    prompt: plan\n    model: opus\n", "model \"opus\""},
		{"retry on run", head + "steps:\n  - id: a\n    run: check all\n    retry: {max: 1}\n", "are for prompt steps"},
		{"out shadows base", head + "steps:\n  - id: a\n    prompt: plan\n    out: [summary]\n", "every reply needs it already"},
		{"shared template needs its steps", head + "steps:\n  - id: a\n    prompt: build\n    out: [commit]\n", "build.md: unknown field .steps.checks"},
		{"bad done status", head + "steps:\n  - id: a\n    run: check all\ndone: {to: shipped}\n", "done.to \"shipped\""},
		{"bad note field", head + "steps:\n  - id: a\n    run: check all\ndone: {to: in_review, note: '{{.run.brnach}}'}\n", "unknown field .run.brnach"},
		{"bad block", head + "steps:\n  - id: a\n    run: check all\nblock: {to: boss, status: later}\n", "block.to \"boss\""},
		{"bad intake", head + "intake: {category: urgent, status: [todo, doing]}\nsteps:\n  - id: a\n    run: check all\n", "intake.category \"urgent\""},
		{"bad intake status", head + "intake: {category: new-work, status: [doing]}\nsteps:\n  - id: a\n    run: check all\n", "intake.status \"doing\""},
		{"syntax", head + "steps:\n  - id: a\n    gate: '{{if}}'\n", "missing value for if"},
		{"nested run", head + "steps:\n  - id: a\n    run: play run planner/groom-board --bot pl-1\n", "run play: a run never starts another run"},
		{"actor flag", head + "steps:\n  - id: a\n    ticket: comment {{.ticket.id}} --actor chief-of-staff\n", "--actor is the engine's"},
		{"claim id", head + "steps:\n  - id: claim\n    run: check all\n", "id claim is the engine's"},
		{"add try", head + "steps:\n  - id: a\n    ticket: transition {{.ticket.id}} --to in_progress --add-try\n", "no --add-try"},
		{"comment version", head + "steps:\n  - id: a\n    ticket: comment {{.ticket.id}} --expect-version 3\n", "comment takes no --expect-version"},
		{"create type", head + "steps:\n  - id: a\n    ticket: create --type feature --role planner --title x\n", `--type "feature": want ship or scout`},
		{"create no type", head + "steps:\n  - id: a\n    ticket: create --role planner --title x\n", `--type "": want ship or scout`},
		{"scout owner", head + "steps:\n  - id: a\n    ticket: create --type=scout --role planner --title x\n", "--type scout needs --owner"},
		{"done move", head + "steps:\n  - id: a\n    ticket: transition {{.ticket.id}} --to done\n", "moves the ticket to done; only"},
		{"done wrap-up", head + "steps:\n  - id: a\n    run: check all\ndone: {to: '{{if .ticket.id}}done{{else}}in_review{{end}}'}\n", "moves the ticket to done; only"},
		{"name mismatch", "name: other\nrole: senior-software-engineer\ndescription: d\nsteps:\n  - id: a\n    run: check all\n", "must match the file name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			copyTree(t, "testdata/repo", dir)
			if err := os.WriteFile(filepath.Join(dir, "playbooks", "senior-software-engineer", "bad.yaml"), []byte(c.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := Lint(filepath.Join(dir, "playbooks"), filepath.Join(dir, "skills"))
			if err != nil {
				t.Fatal(err)
			}
			var all []string
			for _, p := range res.Problems {
				all = append(all, p.String())
			}
			joined := strings.Join(all, "\n")
			if !strings.Contains(joined, c.want) {
				t.Fatalf("want a problem with %q, got:\n%s", c.want, joined)
			}
			if c.name == "exit outcome ok" && strings.Contains(joined, `"exit:3"`) {
				t.Errorf("exit:3 is a valid outcome:\n%s", joined)
			}
		})
	}
}

func TestLintUses(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, "testdata/repo", dir)
	os.WriteFile(filepath.Join(dir, "skills", "use.yaml"), []byte("prompts:\n  build: [tdd, ghost]\n  nosuch: []\nbots:\n  janitor: [tdd]\n"), 0o644)
	res, err := Lint(filepath.Join(dir, "playbooks"), filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, p := range res.Problems {
		joined += p.String() + "\n"
	}
	for _, want := range []string{`skill "ghost" has no folder`, `prompts.nosuch: no prompt template`, `bots.janitor: not a role`} {
		if !strings.Contains(joined, want) {
			t.Errorf("want %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, `"tdd"`) {
		t.Errorf("tdd has a folder:\n%s", joined)
	}
	// No use.yaml at all, and an empty one, name nothing and are fine.
	os.Remove(filepath.Join(dir, "skills", "use.yaml"))
	if res, _ := Lint(filepath.Join(dir, "playbooks"), filepath.Join(dir, "skills")); len(res.Problems) != 0 {
		t.Errorf("no use.yaml: %v", res.Problems)
	}
	os.WriteFile(filepath.Join(dir, "skills", "use.yaml"), nil, 0o644)
	if res, _ := Lint(filepath.Join(dir, "playbooks"), filepath.Join(dir, "skills")); len(res.Problems) != 0 {
		t.Errorf("empty use.yaml: %v", res.Problems)
	}
}

func TestLintCommands(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, "testdata/repo", dir)
	skills := filepath.Join(dir, "skills")
	write := func(rel, text string) {
		t.Helper()
		path := filepath.Join(skills, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manual := "---\nname: x\ndisable-model-invocation: true\n---\nbody\n"
	write("work/SKILL.md", manual)
	write("work/agents/openai.yaml", "policy:\n  allow_implicit_invocation: false\n")
	write("auto/SKILL.md", "---\nname: auto\n---\nbody\n") // no disable-model-invocation
	write("codex/SKILL.md", manual)                        // no agents/openai.yaml
	write("loose/SKILL.md", manual)                        // Codex may start it
	write("loose/agents/openai.yaml", "policy:\n  allow_implicit_invocation: true\n")
	write("tdd/SKILL.md", "# tdd\nWhen done, run /work.\n## In your own session\nRun `/work <id>`.\nIn your own session, $work works too.\n## Not in your own session\nRun /work.\n")
	write("use.yaml", "prompts:\n  build: [tdd, work]\nbots:\n  all: [work]\ncommands: [work, auto, codex, loose, ghost]\n")
	if err := os.WriteFile(filepath.Join(dir, "playbooks", "prompts", "plan.md"), []byte("# Plan\nThen run /work.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Lint(filepath.Join(dir, "playbooks"), skills)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, p := range res.Problems {
		joined += p.String() + "\n"
	}
	for _, want := range []string{
		`commands: skill "ghost" has no folder`,
		`commands: auto: SKILL.md needs disable-model-invocation: true`,
		`commands: codex: agents/openai.yaml needs policy.allow_implicit_invocation: false`,
		`commands: loose: agents/openai.yaml needs`,
		`prompts.build: "work" is a command`,
		`tdd/SKILL.md: line 2 calls the command /work`,
		`tdd/SKILL.md: line 7 calls the command /work`, // "Not in your own session" is no exemption
		`bots.all: "work" is a command`,
		`prompts/plan.md: line 2 calls the command /work`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("want %q in:\n%s", want, joined)
		}
	}
	for _, not := range []string{"commands: work:", "line 4 calls", "line 5 calls"} {
		if strings.Contains(joined, not) {
			t.Errorf("didn't want %q in:\n%s", not, joined)
		}
	}
}

func TestLintNeedsPartials(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, "testdata/repo", dir)
	os.Remove(filepath.Join(dir, "playbooks", "prompts", "_escalate.md"))
	res, _ := Lint(filepath.Join(dir, "playbooks"), filepath.Join(dir, "skills"))
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0].Msg, "missing _escalate.md") {
		t.Errorf("problems %v", res.Problems)
	}
}

func TestSplitWordsAndRenderArgs(t *testing.T) {
	words, err := splitWords(`comment {{.id}} --body "a {{ .x }} b" 'it''s' x\ y {{if .f}}--flag{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range words {
		got = append(got, w.text)
	}
	want := []string{"comment", "{{.id}}", "--body", "a {{ .x }} b", "its", "x y", "{{if .f}}--flag{{end}}"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("words %q, want %q", got, want)
	}
	args, err := renderArgs("t", `comment {{.id}} --body "{{.x}}" {{if .f}}--flag{{end}} ""`, map[string]any{"id": "T-1", "x": "two words", "f": false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(args, "|") != "comment|T-1|--body|two words|" {
		t.Errorf("args %q", args)
	}
	for _, bad := range []string{`a "b`, `a {{.x`} {
		if _, err := splitWords(bad); err == nil {
			t.Errorf("splitWords(%q) should fail", bad)
		}
	}
	if _, err := renderArgs("t", "{{.missing}}", map[string]any{}); err == nil {
		t.Errorf("missing key should fail")
	}
}

func TestCheckFieldsSkipsRangeBodies(t *testing.T) {
	schema := map[string]any{"steps": map[string]any{"a": map[string]any{"reply": map[string]any{"list": nil}}}, "run": map[string]any{"id": ""}}
	tm, err := parseTemplate("t", `{{range .steps.a.reply.list}}{{.name}}{{$.run.id}}{{$.run.nope}}{{end}}{{with .steps.b}}{{.x}}{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	probs := checkFields(tm, schema)
	if strings.Join(probs, ";") != "unknown field .run.nope;unknown field .steps.b" {
		t.Errorf("problems %q", probs)
	}
}

func TestRenderBool(t *testing.T) {
	if ok, err := renderBool("g", " {{eq .a 1}} ", map[string]any{"a": 1}); !ok || err != nil {
		t.Errorf("got %v %v", ok, err)
	}
	if _, err := renderBool("g", "yes", nil); err == nil {
		t.Errorf("yes should fail")
	}
}

func TestCheckReply(t *testing.T) {
	ev := []any{"ran it"}
	cases := []struct {
		reply map[string]any
		want  string
	}{
		{map[string]any{"status": "done", "summary": "s", "evidence": ev, "commit": "c000001"}, ""},
		{map[string]any{"status": "done", "summary": "s", "evidence": ev}, "fields: commit"},
		{map[string]any{"status": "done", "summary": "s", "commit": "c000001"}, "evidence"},
		{map[string]any{"status": "blocked", "summary": "s"}, ""},
		{map[string]any{"status": "ok", "summary": "s"}, "status \"ok\""},
		{map[string]any{"status": "done", "summary": "", "evidence": ev, "commit": "c000001"}, "summary"},
		{map[string]any{"status": "done", "summary": "s", "evidence": ev, "commit": "c000001", "processes": []any{map[string]any{"pid": 0}}}, "processes"},
		{nil, "no structured reply"},
		// A commit lands in a sha: trailer, so it must be one sha (0746).
		{map[string]any{"status": "done", "summary": "s", "evidence": ev, "commit": "abc1234\ntries-at-takeover: 99"}, "commit: want one git sha"},
		{map[string]any{"status": "done", "summary": "s", "evidence": ev, "commit": "HEAD"}, "commit: want one git sha"},
		{map[string]any{"status": "done", "summary": "s", "evidence": ev, "commit": ""}, "commit: want one git sha"},
		{map[string]any{"status": "failed", "summary": "s", "commit": ""}, ""},
		{map[string]any{"status": "failed", "summary": "s", "commit": "c000001", "sha": "main"}, "sha: want one git sha"},
	}
	for _, c := range cases {
		got := checkReply(c.reply, []string{"commit"})
		if (c.want == "") != (got == "") || !strings.HasPrefix(got, c.want) {
			t.Errorf("checkReply(%v) = %q, want %q", c.reply, got, c.want)
		}
	}
}

func TestNameSkills(t *testing.T) {
	for harness, want := range map[string]string{
		"claude-code": "/tdd", "grok-build": "Read and follow .agents/skills/tdd/SKILL.md", "codex": "$tdd", "": "Use the tdd skill.",
	} {
		if got := nameSkills(harness, []string{"tdd"}); got != "Skills for this step:\n"+want {
			t.Errorf("%s: %q", harness, got)
		}
	}
}
