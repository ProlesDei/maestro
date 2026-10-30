package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
	"gopkg.in/yaml.v3"
)

// Problem is one lint finding.
type Problem struct {
	Path string // file
	Step string // step id, or ""
	Msg  string
}

func (p Problem) String() string {
	if p.Step != "" {
		return fmt.Sprintf("%s: step %s: %s", p.Path, p.Step, p.Msg)
	}
	return fmt.Sprintf("%s: %s", p.Path, p.Msg)
}

// LintResult is what Lint found.
type LintResult struct {
	Playbooks int
	Problems  []Problem
}

// Lint checks every playbooks/<role>/<job>.yaml under dir, the prompt
// templates in dir/prompts and skillsDir/use.yaml: the schema, step types,
// built-in request names and flags, ticket subcommands, outcome targets,
// template syntax and variables, and that every listed skill has a folder.
func Lint(dir, skillsDir string) (LintResult, error) {
	var res LintResult
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*.yaml"))
	if err != nil {
		return res, err
	}
	sort.Strings(paths)
	l := &linter{dir: dir, prompts: map[string]string{}}
	if err := l.loadPrompts(); err != nil {
		return res, err
	}
	for _, path := range paths {
		folder := filepath.Base(filepath.Dir(path))
		if strings.HasPrefix(folder, "_") || folder == "prompts" {
			continue
		}
		res.Playbooks++
		data, err := os.ReadFile(path)
		if err != nil {
			return res, err
		}
		p, err := playbook.Parse(path, data)
		if err != nil {
			l.add(path, "", err.Error())
			continue
		}
		l.playbook(p, folder)
	}
	if res.Playbooks > 0 {
		for _, name := range []string{"_contract", "_escalate"} {
			if _, ok := l.prompts[name]; !ok {
				l.add(filepath.Join(dir, "prompts"), "", "missing "+name+".md")
			}
		}
	}
	l.uses(skillsDir)
	res.Problems = l.probs
	return res, nil
}

type linter struct {
	dir     string
	prompts map[string]string // template name -> text
	probs   []Problem
}

func (l *linter) add(path, step, msg string) {
	l.probs = append(l.probs, Problem{path, step, msg})
}

func (l *linter) loadPrompts() error {
	paths, err := filepath.Glob(filepath.Join(l.dir, "prompts", "*.md"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(filepath.Base(path), ".md")
		l.prompts[name] = string(b)
		if _, err := parseTemplate(name, string(b)); err != nil {
			l.add(path, "", err.Error())
		}
	}
	return nil
}

func (l *linter) playbook(p *playbook.Playbook, folder string) {
	path := p.Path
	if job := strings.TrimSuffix(filepath.Base(path), ".yaml"); p.Name != job {
		l.add(path, "", fmt.Sprintf("name %q must match the file name %q", p.Name, job))
	}
	if p.Role != folder {
		l.add(path, "", fmt.Sprintf("role %q must match the folder %q", p.Role, folder))
	}
	if !playbook.Contains(playbook.Roles, p.Role) {
		l.add(path, "", fmt.Sprintf("unknown role %q", p.Role))
	}
	if strings.TrimSpace(p.Description) == "" {
		l.add(path, "", "description is empty")
	}
	if p.Session != "" && p.Session != "ticket" && p.Session != "run" && p.Session != "step" {
		l.add(path, "", fmt.Sprintf("session %q: want ticket, run or step", p.Session))
	}
	if p.Input != "" && p.Input != "required" && p.Input != "optional" {
		l.add(path, "", fmt.Sprintf("input %q: want required or optional", p.Input))
	}
	if c := p.Cursor; c != nil {
		if !playbook.IdentRE.MatchString(strings.ReplaceAll(c.Name, "-", "_")) {
			l.add(path, "", fmt.Sprintf("cursor.name %q: want lower case letters, digits, - and _", c.Name))
		}
		l.template(p, nil, "cursor.save", c.Save, lintSchema(p, nil))
	}
	l.intake(p)
	if len(p.Steps) == 0 {
		l.add(path, "", "no steps")
	}
	schema := lintSchema(p, nil)
	seen := map[string]bool{}
	for i := range p.Steps {
		s := &p.Steps[i]
		if !playbook.IdentRE.MatchString(s.ID) {
			l.add(path, s.ID, fmt.Sprintf("id %q: want lower case letters, digits and _", s.ID))
		}
		if seen[s.ID] {
			l.add(path, s.ID, "duplicate id")
		}
		seen[s.ID] = true
		if playbook.Contains(playbook.Targets, s.ID) {
			l.add(path, s.ID, "id is a reserved on: target")
		}
		if s.ID == "claim" {
			l.add(path, s.ID, "id claim is the engine's: its idempotency key is <run>:claim")
		}
	}
	for i := range p.Steps {
		l.step(p, &p.Steps[i], lintSchema(p, &p.Steps[i]))
	}
	if to := p.Done.To; to != "" && !strings.Contains(to, "{{") && to != "keep" && !playbook.Contains(playbook.Statuses, to) {
		l.add(path, "", fmt.Sprintf("done.to %q: unknown status", to))
	}
	if doneWord.MatchString(p.Done.To) {
		l.endsDone(p, "")
	}
	for _, f := range [][2]string{{"done.to", p.Done.To}, {"done.note", p.Done.Note}, {"done.role", p.Done.Role}, {"done.actor", p.Done.Actor}, {"done.log", p.Done.Log}} {
		if f[1] != "" {
			l.template(p, nil, f[0], f[1], schema)
		}
	}
	l.block(p, "", p.Block, schema)
	if text, ok := l.prompts["_escalate"]; ok {
		l.template(p, nil, "_escalate.md", text, schema)
	}
}

// block checks a block: (the playbook's, or a step's).
func (l *linter) block(p *playbook.Playbook, step string, b playbook.Block, schema map[string]any) {
	switch to := b.To; {
	case strings.Contains(to, "{{"):
		l.template(p, p.Step(step), "block.to", to, schema)
	case to == "", to == "owner", playbook.Contains(playbook.Roles, to):
	default:
		l.add(p.Path, step, fmt.Sprintf("block.to %q: want owner, chief-of-staff or a role", to))
	}
	if st := b.Status; st != "" && st != "blocked" && st != "todo" && st != "keep" {
		l.add(p.Path, step, fmt.Sprintf("block.status %q: want blocked, todo or keep", st))
	}
}

func (l *linter) intake(p *playbook.Playbook) {
	for i := range p.Intake {
		l.intakeEntry(p, &p.Intake[i])
	}
}

func (l *linter) intakeEntry(p *playbook.Playbook, in *playbook.Intake) {
	if !playbook.Contains(playbook.Categories, in.Category) {
		l.add(p.Path, "", fmt.Sprintf("intake.category %q: want one of %s", in.Category, strings.Join(playbook.Categories, ", ")))
	}
	if in.Category == "scheduled" && len(in.Status) > 0 {
		l.add(p.Path, "", "intake: a scheduled playbook takes no ticket status")
	}
	if (in.Category == "sent-back" || in.Category == "new-work") && len(in.Status) == 0 {
		l.add(p.Path, "", "intake.status is empty")
	}
	for _, st := range in.Status {
		if !playbook.Contains(playbook.Statuses, st) {
			l.add(p.Path, "", fmt.Sprintf("intake.status %q: unknown status", st))
		}
	}
	if in.Role != "" && in.Role != "any" && !playbook.Contains(playbook.Roles, in.Role) {
		l.add(p.Path, "", fmt.Sprintf("intake.role %q: want any or a role", in.Role))
	}
	if in.Type != "" && in.Type != "ship" && in.Type != "scout" {
		l.add(p.Path, "", fmt.Sprintf("intake.type %q: want ship or scout", in.Type))
	}
	if in.Owner != "" && !playbook.Contains(playbook.Roles, in.Owner) {
		l.add(p.Path, "", fmt.Sprintf("intake.owner %q: not a role", in.Owner))
	}
	if in.Category == "scheduled" && p.Claim {
		l.add(p.Path, "", "claim: a scheduled playbook has no ticket to claim")
	}
}

// outcomes is what on: may map for a step.
func outcomes(s *playbook.Step) (fixed []string, exitN bool) {
	if s.Each != "" {
		return []string{"ok", "fail"}, false
	}
	switch s.Kind() {
	case playbook.KindTicket, playbook.KindRun:
		return []string{"ok", "fail"}, true
	case playbook.KindPrompt:
		return playbook.ReplyStatuses, false
	case playbook.KindGate:
		return []string{"ok", "fail"}, false
	case playbook.KindAsk:
		return s.Ask.Choices, false
	}
	return nil, false
}

func (l *linter) step(p *playbook.Playbook, s *playbook.Step, schema map[string]any) {
	path := p.Path
	kind := s.Kind()
	if kind == "" {
		l.add(path, s.ID, "want exactly one of ticket, run, prompt, gate, ask")
		return
	}
	if kind != playbook.KindPrompt {
		if s.Retry != nil || len(s.Out) > 0 || s.Model != "" || s.Family != "" || s.Mode != "" || s.Session != "" || s.With != nil || s.Vars != nil {
			l.add(path, s.ID, "retry, out, model, family, mode, session, with and vars are for prompt steps")
		}
	}
	if s.With != nil && s.Vars != nil {
		l.add(path, s.ID, "with and vars are one thing: use one")
	}
	for _, k := range sortedKeys(toLists(s.Params())) {
		if !playbook.IdentRE.MatchString(k) {
			l.add(path, s.ID, fmt.Sprintf("with %q: want lower case letters, digits and _", k))
		}
		l.template(p, s, "with."+k, s.Params()[k], schema)
	}
	if s.Session != "" && s.Session != "fresh" {
		l.add(path, s.ID, fmt.Sprintf("session %q: want fresh", s.Session))
	}
	if s.Reason != "" && s.When == nil {
		l.add(path, s.ID, "reason without when")
	}
	if s.Reason != "" {
		l.template(p, s, "reason", s.Reason, schema)
	}
	if s.Body != "" {
		words, _ := splitWords(s.Ticket)
		if kind != playbook.KindTicket || len(words) == 0 ||
			!strings.Contains(words[0].text, "{{") && !playbook.Contains([]string{"comment", "create", "transition"}, words[0].text) {
			l.add(path, s.ID, "body is for ticket comment, create and transition steps")
		}
		l.template(p, s, "body", s.Body, schema)
	}
	if s.Each != "" {
		l.template(p, s, "each", "{{"+s.Each+"}}", schema)
	}
	if s.Block != nil {
		l.block(p, s.ID, *s.Block, schema)
	}
	if s.When != nil {
		if strings.TrimSpace(s.When.If) == "" {
			l.add(path, s.ID, "when is empty")
		}
		l.boolTemplate(p, s, "when", s.When.If, schema)
		if s.When.Reason != "" {
			l.template(p, s, "when.reason", s.When.Reason, schema)
		}
	}
	fixed, exitN := outcomes(s)
	ons := make([]string, 0, len(s.On))
	for out := range s.On {
		ons = append(ons, out)
	}
	sort.Strings(ons)
	for _, out := range ons {
		target := s.On[out]
		valid := playbook.Contains(fixed, out)
		if !valid && exitN && strings.HasPrefix(out, "exit:") {
			var n int
			_, err := fmt.Sscanf(out, "exit:%d", &n)
			valid = err == nil && fmt.Sprintf("exit:%d", n) == out && n > 0
		}
		if !valid {
			l.add(path, s.ID, fmt.Sprintf("on: %q isn't an outcome of a %s step", out, kind))
		}
		if !playbook.Contains(playbook.Targets, target) && p.Step(target) == nil {
			l.add(path, s.ID, fmt.Sprintf("on: %s: no step %q", out, target))
		}
	}
	switch kind {
	case playbook.KindTicket:
		l.ticketStep(p, s, schema)
	case playbook.KindRun:
		l.runStep(p, s, schema)
	case playbook.KindPrompt:
		l.promptStep(p, s, schema)
	case playbook.KindGate:
		l.boolTemplate(p, s, "gate", s.Gate, schema)
	case playbook.KindAsk:
		a := s.Ask
		if strings.TrimSpace(a.Question) == "" {
			l.add(path, s.ID, "ask.question is empty")
		}
		l.template(p, s, "ask.question", a.Question, schema)
		if len(a.Choices) == 0 {
			l.add(path, s.ID, "ask.choices: want one or more")
		}
		for _, c := range a.Choices {
			if !playbook.IdentRE.MatchString(c) {
				l.add(path, s.ID, fmt.Sprintf("ask.choices %q: want lower case letters, digits and _", c))
			}
		}
		if _, err := a.LeaseTTL(); err != nil {
			l.add(path, s.ID, "ask: "+err.Error())
		}
	}
}

func (l *linter) ticketStep(p *playbook.Playbook, s *playbook.Step, schema map[string]any) {
	words, err := splitWords(s.Ticket)
	if err != nil || len(words) == 0 {
		l.add(p.Path, s.ID, fmt.Sprintf("ticket: can't split %q", s.Ticket))
		return
	}
	sub := words[0].text
	if strings.Contains(sub, "{{") {
		// chosen at run time (a drafted fix); the engine checks it then
	} else if _, ok := playbook.TicketSubs[sub]; !ok {
		switch sub {
		case "claim", "release", "heartbeat", "keepalive":
			l.add(p.Path, s.ID, fmt.Sprintf("ticket %s: the engine claims, heartbeats and releases", sub))
		default:
			l.add(p.Path, s.ID, fmt.Sprintf("ticket %q: want one of get, list, events, doctor, create, update, comment, transition", sub))
		}
	}
	flags := map[string]string{}
	for i, w := range words[1:] {
		for _, f := range playbook.EngineTicketFlags {
			if isFlag(w.text, f) {
				l.add(p.Path, s.ID, fmt.Sprintf("ticket: %s is the engine's", f))
			}
		}
		if name, val, ok := strings.Cut(w.text, "="); ok && strings.HasPrefix(name, "-") {
			flags[name] = val
		} else if strings.HasPrefix(w.text, "-") {
			flags[w.text] = ""
			if i+2 < len(words) {
				flags[w.text] = words[i+2].text
			}
		}
	}
	// The store's write rules.
	if _, ok := flags["--add-try"]; ok {
		l.add(p.Path, s.ID, "ticket: no --add-try; the store counts tries")
	}
	if _, ok := flags["--expect-version"]; ok && sub == "comment" {
		l.add(p.Path, s.ID, "ticket comment takes no --expect-version")
	}
	if sub == "create" {
		switch typ := flags["--type"]; {
		case strings.Contains(typ, "{{"):
		case typ != "ship" && typ != "scout":
			l.add(p.Path, s.ID, fmt.Sprintf("ticket create --type %q: want ship or scout", typ))
		case typ == "scout" && flags["--owner"] == "":
			l.add(p.Path, s.ID, "ticket create --type scout needs --owner")
		}
	}
	if sub == "transition" && flags["--to"] == "done" {
		l.endsDone(p, s.ID)
	}
	l.template(p, s, "ticket", s.Ticket, schema)
}

// doneWord finds done in a done.to, templated or not.
var doneWord = regexp.MustCompile(`(^|[^\w.])done($|[^\w])`)

// DoneEnders are the playbooks that may move a ticket to done: none. The
// reviewer's /review merges the PR and sets done itself.
var DoneEnders []string

func (l *linter) endsDone(p *playbook.Playbook, step string) {
	if !playbook.Contains(DoneEnders, p.Role+"/"+p.Name) {
		l.add(p.Path, step, "moves the ticket to done; no playbook may: /review does")
	}
}

func (l *linter) runStep(p *playbook.Playbook, s *playbook.Step, schema map[string]any) {
	for _, line := range s.Run {
		words, err := splitWords(line)
		if err != nil || len(words) == 0 {
			l.add(p.Path, s.ID, fmt.Sprintf("run: can't split %q", line))
			continue
		}
		req := playbook.FindRequest(plainWords(words))
		switch {
		case req == nil:
			var names []string
			for _, r := range playbook.Requests {
				if !r.Engine {
					names = append(names, r.Name)
				}
			}
			l.add(p.Path, s.ID, fmt.Sprintf("run %q: not a built-in request (want %s)", words[0].text, strings.Join(names, ", ")))
		case req.Engine:
			l.add(p.Path, s.ID, fmt.Sprintf("run %s: the engine sends this itself", req.Name))
		default:
			l.requestArgs(p, s, req, words[len(strings.Fields(req.Name)):])
		}
		l.template(p, s, "run", line, schema)
	}
}

// requestArgs checks a request's literal flags and, when no word is a
// template that may render to nothing or to several words, its shape.
func (l *linter) requestArgs(p *playbook.Playbook, s *playbook.Step, req *playbook.Request, words []word) {
	plain := true
	var texts []string
	for _, w := range words {
		t := w.text
		if strings.Contains(t, "{{") {
			if inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(t, "{{"), "}}")); strings.Count(t, "{{") != 1 ||
				!strings.HasPrefix(t, "{{") || !strings.HasSuffix(t, "}}") || !strings.HasPrefix(inner, ".") || strings.ContainsAny(inner, " |") {
				plain = false
			}
			t = "x" // one word, whatever it renders
		}
		texts = append(texts, t)
	}
	for _, t := range texts {
		if strings.HasPrefix(t, "-") {
			name, _, _ := strings.Cut(t, "=")
			if !playbook.Contains(req.Flags, "--"+strings.TrimLeft(name, "-")) {
				l.add(p.Path, s.ID, fmt.Sprintf("run %s takes no %s (want %s)", req.Name, name, req.Usage))
				return
			}
		}
	}
	if !plain {
		return
	}
	if _, _, err := reqArgs(req, texts); err != nil {
		l.add(p.Path, s.ID, "run "+err.Error())
	}
}

func (l *linter) promptStep(p *playbook.Playbook, s *playbook.Step, schema map[string]any) {
	if strings.HasPrefix(s.Prompt, "_") {
		l.add(p.Path, s.ID, fmt.Sprintf("prompt %q: partials are added by the engine", s.Prompt))
	}
	if !playbook.Contains(playbook.Tiers, s.Tier()) {
		l.add(p.Path, s.ID, fmt.Sprintf("model %q: want one of %s", s.Model, strings.Join(playbook.Tiers, ", ")))
	}
	if s.Family != "" && s.Family != "not-author" {
		l.add(p.Path, s.ID, fmt.Sprintf("family %q: want not-author", s.Family))
	}
	if s.Retry != nil && (s.Retry.Max < 0 || s.Retry.Max > 5) {
		l.add(p.Path, s.ID, "retry.max: want 0 to 5")
	}
	for _, f := range s.Out {
		if !playbook.IdentRE.MatchString(f) {
			l.add(p.Path, s.ID, fmt.Sprintf("out %q: want lower case letters, digits and _", f))
		}
		if f == "status" || f == "summary" {
			l.add(p.Path, s.ID, fmt.Sprintf("out %q: every reply needs it already", f))
		}
	}
	text, ok := l.prompts[s.Prompt]
	if !ok {
		l.add(p.Path, s.ID, fmt.Sprintf("prompt %q: no %s", s.Prompt, filepath.Join(l.dir, "prompts", s.Prompt+".md")))
		return
	}
	if c, ok := l.prompts["_contract"]; ok {
		l.template(p, s, "_contract.md", c, schema)
	}
	l.template(p, s, s.Prompt+".md", text, schema)
}

// template checks one template: it parses, names only known fields, and
// renders against zero data (with and without .retry).
func (l *linter) template(p *playbook.Playbook, s *playbook.Step, name, text string, schema map[string]any) {
	l.check(p, s, name, text, schema, false)
}

// boolTemplate is template for a when, gate or until, which must also
// render true or false when it renders on zero data: and and or return an
// operand, so a list in one renders [].
func (l *linter) boolTemplate(p *playbook.Playbook, s *playbook.Step, name, text string, schema map[string]any) {
	l.check(p, s, name, text, schema, true)
}

func (l *linter) check(p *playbook.Playbook, s *playbook.Step, name, text string, schema map[string]any, boolean bool) {
	id := ""
	if s != nil {
		id = s.ID
	}
	t, err := parseTemplate(name, text)
	if err != nil {
		l.add(p.Path, id, err.Error())
		return
	}
	if msgs := checkFields(t, schema); len(msgs) > 0 {
		for _, msg := range msgs {
			l.add(p.Path, id, name+": "+msg)
		}
		return
	}
	for _, retry := range []any{nil, map[string]any{"reason": "missing: x", "n": 1}} {
		d := lintData(p, s)
		d["retry"] = retry
		var b strings.Builder
		err := t.Execute(&b, d)
		if err != nil && !zeroDataError(err) {
			l.add(p.Path, id, err.Error())
			return
		}
		if out := strings.TrimSpace(b.String()); boolean && err == nil && out != "true" && out != "false" {
			l.add(p.Path, id, fmt.Sprintf("%s rendered %q on empty data, want true or false (wrap it in {{if}}true{{else}}false{{end}})", name, out))
			return
		}
	}
}

// zeroDataError is an execution error that only zero data causes: a nil
// field walked into, or a comparison with nil.
func zeroDataError(err error) bool {
	msg := err.Error()
	for _, s := range []string{"nil pointer evaluating", "incompatible types for comparison", "invalid type for comparison",
		"nil data", "error calling len", "error calling index", "error calling slice", "index of untyped nil", "can't iterate over"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// uses checks skills/use.yaml: known template and role keys, and a folder
// for every listed skill.
func (l *linter) uses(skillsDir string) {
	path := filepath.Join(skillsDir, "use.yaml")
	u, err := playbook.LoadUses(skillsDir)
	if err != nil {
		l.add(path, "", err.Error())
		return
	}
	check := func(section, key string, names []string) {
		for _, n := range names {
			if n == "" || strings.ContainsAny(n, "/\\") || strings.HasPrefix(n, ".") {
				l.add(path, "", fmt.Sprintf("%s.%s: bad skill name %q", section, key, n))
				continue
			}
			if fi, err := os.Stat(filepath.Join(skillsDir, n)); err != nil || !fi.IsDir() {
				l.add(path, "", fmt.Sprintf("%s.%s: skill %q has no folder %s", section, key, n, filepath.Join(skillsDir, n)))
			}
		}
	}
	for _, key := range sortedKeys(u.Prompts) {
		if _, ok := l.prompts[key]; key != "all" && (!ok || strings.HasPrefix(key, "_")) {
			l.add(path, "", fmt.Sprintf("prompts.%s: no prompt template %q", key, key))
		}
		check("prompts", key, u.Prompts[key])
	}
	for _, key := range sortedKeys(u.Bots) {
		if key != "all" && !playbook.Contains(playbook.Roles, key) {
			l.add(path, "", fmt.Sprintf("bots.%s: not a role", key))
		}
		check("bots", key, u.Bots[key])
	}
	l.commands(path, skillsDir, u)
}

// commands checks use.yaml's commands: each is a skill folder, only the
// user can start it (Claude Code's disable-model-invocation and Codex's
// agents/openai.yaml), no prompt names it, and no prompt skill tells
// a step to call it, except on a line, or under a heading, about the
// user's own session.
func (l *linter) commands(path, skillsDir string, u *playbook.Uses) {
	seen := map[string]bool{}
	for _, n := range u.Commands {
		if n == "" || strings.ContainsAny(n, "/\\") || strings.HasPrefix(n, ".") {
			l.add(path, "", fmt.Sprintf("commands: bad skill name %q", n))
			continue
		}
		if seen[n] {
			l.add(path, "", fmt.Sprintf("commands: %q listed twice", n))
		}
		seen[n] = true
		dir := filepath.Join(skillsDir, n)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			l.add(path, "", fmt.Sprintf("commands: skill %q has no folder %s", n, dir))
			continue
		}
		front, _, err := skillFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			l.add(path, "", fmt.Sprintf("commands: %s: %v", n, err))
		} else if front["disable-model-invocation"] != true {
			l.add(path, "", fmt.Sprintf("commands: %s: SKILL.md needs disable-model-invocation: true, so only the user starts it", n))
		}
		var oa struct {
			Policy struct {
				Implicit *bool `yaml:"allow_implicit_invocation"`
			} `yaml:"policy"`
		}
		data, err := os.ReadFile(filepath.Join(dir, "agents", "openai.yaml"))
		if err == nil {
			err = yaml.Unmarshal(data, &oa)
		}
		if err != nil || oa.Policy.Implicit == nil || *oa.Policy.Implicit {
			l.add(path, "", fmt.Sprintf("commands: %s: agents/openai.yaml needs policy.allow_implicit_invocation: false, so Codex never starts it", n))
		}
	}
	var prompted []string
	for _, key := range sortedKeys(u.Prompts) {
		for _, n := range u.Prompts[key] {
			if seen[n] {
				l.add(path, "", fmt.Sprintf("prompts.%s: %q is a command; a prompt never names one", key, n))
			}
			if !playbook.Contains(prompted, n) {
				prompted = append(prompted, n)
			}
		}
	}
	for _, key := range sortedKeys(u.Bots) {
		for _, n := range u.Bots[key] {
			if seen[n] {
				l.add(path, "", fmt.Sprintf("bots.%s: %q is a command; only the user starts one", key, n))
			}
		}
	}
	if len(seen) == 0 {
		return
	}
	var names []string
	for n := range seen {
		names = append(names, regexp.QuoteMeta(n))
	}
	sort.Strings(names)
	call := regexp.MustCompile(`(^|[^\w/.$-])[/$](` + strings.Join(names, "|") + `)\b`)
	// A prompt template is always a step, so it never calls a command.
	for _, name := range sortedKeys(toLists(l.prompts)) {
		for i, line := range strings.Split(l.prompts[name], "\n") {
			if m := call.FindStringSubmatch(line); m != nil {
				l.add(filepath.Join(l.dir, "prompts", name+".md"), "", fmt.Sprintf("line %d calls the command /%s; a step can't start one", i+1, m[2]))
			}
		}
	}
	for _, n := range prompted {
		skill := filepath.Join(skillsDir, n, "SKILL.md")
		_, body, err := skillFile(skill)
		if err != nil {
			continue // the folder check above reports it
		}
		heading := ""
		for i, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "#") {
				heading = line
			}
			own := strings.Contains(strings.ToLower(line), "in your own session") || (ownHeading.MatchString(heading) && !notWord.MatchString(heading))
			if m := call.FindStringSubmatch(line); m != nil && !own {
				l.add(skill, "", fmt.Sprintf("line %d calls the command /%s; a step can't start one, so say \"in your own session\" on the line or its heading", i+1, m[2]))
			}
		}
	}
}

// ownHeading is a heading about the user's own session, and not one that
// says "not in your own session".
var ownHeading, notWord = regexp.MustCompile(`(?i)^#+.*\byour own session\b`), regexp.MustCompile(`(?i)\bnot\b`)

// skillFile reads a SKILL.md: its YAML frontmatter and the whole text.
func skillFile(path string) (map[string]any, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	text := string(data)
	front := map[string]any{}
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return front, text, nil
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, text, fmt.Errorf("frontmatter has no closing ---")
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &front); err != nil {
		return nil, text, fmt.Errorf("frontmatter: %w", err)
	}
	return front, text, nil
}

func toLists(m map[string]string) map[string][]string {
	out := map[string][]string{}
	for k := range m {
		out[k] = nil
	}
	return out
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
