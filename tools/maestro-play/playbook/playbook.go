// Package playbook holds the playbook schema: the YAML files under
// playbooks/<role>/<job>.yaml, the step types, and the names the engine and
// the lint agree on. It has no I/O beyond reading the files.
package playbook

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Roles are the bot-profile folder names a playbook can belong to.
var Roles = []string{
	"chief-of-staff", "planner", "senior-software-engineer", "junior-software-engineer",
	"code-reviewer", "qa-lead", "operations-manager",
}

// Statuses are maestro-ticket's statuses (tools/maestro-ticket/ticket).
var Statuses = []string{
	"backlog", "todo", "in_progress", "in_review", "in_qa", "ready_to_release", "done", "blocked", "cancelled",
}

// Categories are the intake categories, in the order --next tries them.
var Categories = []string{"request", "sent-back", "new-work", "scheduled"}

// Tiers are the model tiers a prompt step can ask for, cheapest first. The
// runner maps them to model ids; playbooks never name a model.
var Tiers = []string{"mid", "frontier"}

// ReplyStatuses are the statuses a prompt step's reply can carry; each one is
// also the step's outcome.
var ReplyStatuses = []string{"done", "partial", "blocked", "failed"}

// BaseReplyFields are the fields every prompt reply may carry (_contract.md).
var BaseReplyFields = []string{"status", "summary", "evidence", "retro", "memory", "processes", "questions"}

// EscalateFields are the reply fields _escalate.md asks for.
var EscalateFields = []string{"happened", "tried", "options", "recommendation", "question"}

// Step kinds.
const (
	KindTicket = "ticket"
	KindRun    = "run"
	KindPrompt = "prompt"
	KindGate   = "gate"
	KindWait   = "wait"
	KindAsk    = "ask"
)

// Targets an on: entry can name besides a step id. done goes to the done:
// wrap-up; end stops without it (no note, no move), but still releases,
// logs and cleans up.
const (
	TargetNext  = "next"
	TargetBlock = "block"
	TargetDone  = "done"
	TargetEnd   = "end"
)

// Targets are the reserved on: targets.
var Targets = []string{TargetNext, TargetBlock, TargetDone, TargetEnd}

// Playbook is one playbooks/<role>/<job>.yaml file.
type Playbook struct {
	Name        string     `yaml:"name"`
	Role        string     `yaml:"role"`
	Description string     `yaml:"description"`
	Intake      IntakeList `yaml:"intake"`
	Claim       bool       `yaml:"claim"`
	// Input is required or optional for a playbook started with --input
	// <text> (.input.text); empty means it takes none.
	Input string `yaml:"input"`
	// Cursor is a value kept per bot between runs (.cursor), saved at done.
	Cursor *Cursor `yaml:"cursor"`
	// Session is "ticket" or "run" (one harness session for every prompt
	// step, the default) or "step" (a fresh session per prompt step).
	Session string `yaml:"session"`
	Steps   []Step `yaml:"steps"`
	Done    Done   `yaml:"done"`
	Block   Block  `yaml:"block"`

	Path string `yaml:"-"` // file it was read from
}

// Ref is the playbook's <role>/<job> name.
func (p *Playbook) Ref() string { return p.Role + "/" + p.Name }

// Step returns the step with this id, or nil.
func (p *Playbook) Step(id string) *Step {
	for i := range p.Steps {
		if p.Steps[i].ID == id {
			return &p.Steps[i]
		}
	}
	return nil
}

// Index returns the position of step id, or -1.
func (p *Playbook) Index(id string) int {
	for i := range p.Steps {
		if p.Steps[i].ID == id {
			return i
		}
	}
	return -1
}

// Cursor names a stored value; Save is the template stored at done.
type Cursor struct {
	Name string `yaml:"name"`
	Save string `yaml:"save"`
}

// IntakeList is one intake, or a list: the playbook takes a ticket that
// matches any entry.
type IntakeList []Intake

// UnmarshalYAML takes a mapping or a sequence of mappings.
func (l *IntakeList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.MappingNode:
		var in Intake
		if err := decodeStrict(n, &in); err != nil {
			return err
		}
		*l = IntakeList{in}
		return nil
	case yaml.SequenceNode:
		*l = nil
		for _, c := range n.Content {
			var in Intake
			if err := decodeStrict(c, &in); err != nil {
				return err
			}
			*l = append(*l, in)
		}
		return nil
	}
	return fmt.Errorf("line %d: intake: want a mapping or a list of them", n.Line)
}

// decodeStrict decodes a node with unknown keys as errors (node.Decode
// doesn't carry the decoder's KnownFields).
func decodeStrict(n *yaml.Node, v any) error {
	b, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	return nil
}

// Intake says which tickets the playbook takes, for run --next.
type Intake struct {
	Category string     `yaml:"category"`
	Status   StringList `yaml:"status"`
	// Role filters on the ticket's role: the playbook's role when empty, no
	// filter when "any".
	Role         string   `yaml:"role"`
	Type         string   `yaml:"type"`  // ship or scout; empty for both
	Kinds        []string `yaml:"kinds"` // the body's "Type: <kind>" line (0430)
	Latest       string   `yaml:"latest"`
	UnlessLatest string   `yaml:"unless_latest"`
	Body         string   `yaml:"body"`        // some body line starts with it
	UnlessBody   string   `yaml:"unless_body"` // no body line starts with it
	Owner        string   `yaml:"owner"`       // the ticket's owner role
	Title        string   `yaml:"title"`       // the title starts with it
}

// TicketRole is the --role filter for list, or "" for none.
func (in *Intake) TicketRole(playbookRole string) string {
	switch in.Role {
	case "":
		return playbookRole
	case "any":
		return ""
	}
	return in.Role
}

// Step is one entry of steps:. Exactly one of the kind keys is set.
type Step struct {
	ID     string `yaml:"id"`
	Ticket string `yaml:"ticket"`
	Run    Lines  `yaml:"run"`
	Prompt string `yaml:"prompt"`
	Gate   string `yaml:"gate"`
	Wait   *Wait  `yaml:"wait"`
	Ask    *Ask   `yaml:"ask"`

	When  *When             `yaml:"when"`
	On    map[string]string `yaml:"on"`
	Retry *Retry            `yaml:"retry"`
	Out   []string          `yaml:"out"`

	Model  string `yaml:"model"`  // prompt: tier, default mid
	Family string `yaml:"family"` // prompt: "not-author" for review
	Mode   string `yaml:"mode"`   // prompt: .mode and .step.mode
	// With (or its other name, Vars) holds templates rendered before the
	// prompt, read as .step.with.<k> (or .step.vars.<k>).
	With map[string]string `yaml:"with"`
	Vars map[string]string `yaml:"vars"`
	// Session "fresh" gives this prompt step a new harness session.
	Session string `yaml:"session"`

	// Reason is the Skipped: text when when: is false.
	Reason string `yaml:"reason"`
	// Body is rendered and passed as --body (comment, create) or --note
	// (transition) when it isn't empty.
	Body string `yaml:"body"`
	// Each is a dot path to a list: the step runs once per item, with
	// .item and .index, and .steps.<id>.results holds one record per item.
	Each string `yaml:"each"`
	// Block overrides the playbook's block: for this step's escalation.
	Block *Block `yaml:"block"`
}

// Params is the step's with: (or vars:) map.
func (s *Step) Params() map[string]string {
	if s.With != nil {
		return s.With
	}
	return s.Vars
}

// Kind is the step's type, or "" when no kind key or more than one is set.
func (s *Step) Kind() string {
	var kinds []string
	if s.Ticket != "" {
		kinds = append(kinds, KindTicket)
	}
	if len(s.Run) > 0 {
		kinds = append(kinds, KindRun)
	}
	if s.Prompt != "" {
		kinds = append(kinds, KindPrompt)
	}
	if s.Gate != "" {
		kinds = append(kinds, KindGate)
	}
	if s.Wait != nil {
		kinds = append(kinds, KindWait)
	}
	if s.Ask != nil {
		kinds = append(kinds, KindAsk)
	}
	if len(kinds) != 1 {
		return ""
	}
	return kinds[0]
}

// Tier is the model tier for a prompt step.
func (s *Step) Tier() string {
	if s.Model == "" {
		return "mid"
	}
	return s.Model
}

// Wait re-runs a run or ticket step until a condition holds.
type Wait struct {
	Step    string `yaml:"step"`
	Until   string `yaml:"until"`
	Every   string `yaml:"every"`   // default 1m
	Timeout string `yaml:"timeout"` // default 30m
}

// Durations parses every and timeout, with their defaults.
func (w *Wait) Durations() (every, timeout time.Duration, err error) {
	every, timeout = time.Minute, 30*time.Minute
	if w.Every != "" {
		if every, err = time.ParseDuration(w.Every); err != nil || every <= 0 {
			return 0, 0, fmt.Errorf("every %q: want a positive duration like 1m", w.Every)
		}
	}
	if w.Timeout != "" {
		if timeout, err = time.ParseDuration(w.Timeout); err != nil || timeout <= 0 {
			return 0, 0, fmt.Errorf("timeout %q: want a positive duration like 30m", w.Timeout)
		}
	}
	return every, timeout, nil
}

// Ask pauses the run for the bot's answer.
type Ask struct {
	Question string   `yaml:"question"`
	Choices  []string `yaml:"choices"`
	// Timeout is how long the lease is kept while the run waits (default 4h).
	Timeout string `yaml:"timeout"`
}

// LeaseTTL is how long a waiting run keeps its lease.
func (a *Ask) LeaseTTL() (time.Duration, error) {
	if a.Timeout == "" {
		return 4 * time.Hour, nil
	}
	d, err := time.ParseDuration(a.Timeout)
	if err != nil || d < 5*time.Second || d > 24*time.Hour {
		return 0, fmt.Errorf("timeout %q: want a duration from 5s to 24h", a.Timeout)
	}
	return d, nil
}

// When is a step's condition: a template that renders true or false, and an
// optional template for the reason logged when it's false.
type When struct {
	If     string `yaml:"if"`
	Reason string `yaml:"reason"`
}

// UnmarshalYAML takes either a bare template or {if, reason}.
func (w *When) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		w.If = n.Value
		return nil
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			k, v := n.Content[i].Value, n.Content[i+1]
			switch k {
			case "if":
				w.If = v.Value
			case "reason":
				w.Reason = v.Value
			default:
				return fmt.Errorf("line %d: when: unknown key %q (want if, reason)", n.Content[i].Line, k)
			}
		}
		return nil
	}
	return fmt.Errorf("line %d: when: want a template or {if, reason}", n.Line)
}

// Retry says how often a prompt step is re-asked when its reply misses a
// field or evidence.
type Retry struct {
	Max    int  `yaml:"max"`
	TierUp bool `yaml:"tier_up"`
}

// Done is the built-in wrap-up on success. Every field is a template.
type Done struct {
	// To is the status to move the ticket to; empty or "keep" keeps it
	// (the note is posted and the lease released).
	To    string `yaml:"to"`
	Note  string `yaml:"note"`  // posted as a comment
	Role  string `yaml:"role"`  // --role on the move
	Actor string `yaml:"actor"` // --actor on the move (a scout's owner)
	Log   string `yaml:"log"`   // the log line's note
}

// Block is the built-in escalation.
type Block struct {
	// To is a template for who the escalation goes to: "owner" (the
	// default: the ticket's owner role, at blocked, prefix "Escalation:"),
	// "chief-of-staff" (role chief-of-staff, at blocked, prefix
	// "Escalation (chief-of-staff):") or another role (a hand-off: that
	// role, at todo, prefix "Escalation:").
	To string `yaml:"to"`
	// Status overrides the status: blocked, todo or keep (forward a blocked
	// ticket: note and release, no move).
	Status string `yaml:"status"`
}

// Prefix is the escalation comment's first word(s) for a rendered to.
func Prefix(to string) string {
	if to == "chief-of-staff" {
		return "Escalation (chief-of-staff):"
	}
	return "Escalation:"
}

// MoveTo is the status an escalation to a rendered to moves to, or "keep".
func (b Block) MoveTo(to string) string {
	switch {
	case b.Status != "":
		return b.Status
	case to == "" || to == "owner" || to == "chief-of-staff":
		return "blocked"
	}
	return "todo"
}

// Lines is a run: step's runner request: one line, or a list of them,
// sent in order until one fails.
type Lines []string

// UnmarshalYAML takes a line or a list of lines.
func (l *Lines) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*l = nil
		if n.Value != "" {
			*l = Lines{n.Value}
		}
		return nil
	case yaml.SequenceNode:
		var s []string
		if err := n.Decode(&s); err != nil {
			return fmt.Errorf("line %d: run: want a request or a list of them", n.Line)
		}
		*l = s
		return nil
	}
	return fmt.Errorf("line %d: run: want a request or a list of them", n.Line)
}

// StringList is a YAML scalar or sequence of strings.
type StringList []string

// UnmarshalYAML takes "a", "a,b" or [a, b].
func (l *StringList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*l = nil
		for _, s := range strings.Split(n.Value, ",") {
			if s = strings.TrimSpace(s); s != "" {
				*l = append(*l, s)
			}
		}
		return nil
	case yaml.SequenceNode:
		var s []string
		if err := n.Decode(&s); err != nil {
			return err
		}
		*l = s
		return nil
	}
	return fmt.Errorf("line %d: want a string or a list", n.Line)
}

// IdentRE is what step ids, reply fields and ask choices must match, so
// templates can name them as .steps.<id>.reply.<field>.
var IdentRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Parse decodes one playbook. Unknown keys are errors, so a typo never
// silently drops a step setting.
func Parse(path string, data []byte) (*Playbook, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var p Playbook
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	p.Path = path
	return &p, nil
}

// Load reads <dir>/<ref>.yaml, where ref is <role>/<job>.
func Load(dir, ref string) (*Playbook, error) {
	role, job, ok := strings.Cut(ref, "/")
	if !ok || role == "" || job == "" || strings.Contains(job, "/") {
		return nil, fmt.Errorf("playbook %q: want <role>/<job>", ref)
	}
	path := filepath.Join(dir, role, job+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, data)
}

// Intakes is the playbook's intake entries.
func (p *Playbook) Intakes() []Intake { return p.Intake }

// Scheduled reports whether the playbook takes no ticket from intake.
func (p *Playbook) Scheduled() bool {
	for _, in := range p.Intake {
		if in.Category != "scheduled" {
			return false
		}
	}
	return len(p.Intake) > 0
}

// LoadRole reads every playbook of a role, sorted by file name.
func LoadRole(dir, role string) ([]*Playbook, error) {
	paths, err := filepath.Glob(filepath.Join(dir, role, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []*Playbook
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		p, err := Parse(path, data)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no playbooks for role %q in %s", role, dir)
	}
	return out, nil
}

// Uses is skills/use.yaml (B3). Missing or empty means no skill is named.
type Uses struct {
	Prompts map[string][]string `yaml:"prompts"`
	Bots    map[string][]string `yaml:"bots"`
	// Commands are skills the user starts as /<name>. The installer copies
	// them; a prompt never names one (0781, 0788).
	Commands []string `yaml:"commands"`
}

// PromptSkills is prompts.all plus prompts.<template>, less any command.
func (u *Uses) PromptSkills(template string) []string {
	if u == nil {
		return nil
	}
	var out []string
	for _, s := range dedupe(append(append([]string(nil), u.Prompts["all"]...), u.Prompts[template]...)) {
		if !Contains(u.Commands, s) {
			out = append(out, s)
		}
	}
	return out
}

// LoadUses reads <skillsDir>/use.yaml; a missing file is an empty list.
func LoadUses(skillsDir string) (*Uses, error) {
	path := filepath.Join(skillsDir, "use.yaml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Uses{}, nil
	}
	if err != nil {
		return nil, err
	}
	var u Uses
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&u); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &u, nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Contains reports whether list has s.
func Contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
