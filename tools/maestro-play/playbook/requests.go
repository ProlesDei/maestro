package playbook

import "strings"

// Request is one row of the runner's request table (docs/runner.md).
type Request struct {
	Name string
	// Roles that may send it; nil means every role.
	Roles []string
	// Engine marks requests only the engine sends. A playbook's run: step
	// can't name them: the engine does preflight, sessions and ticket calls
	// itself.
	Engine bool
	// Fields the reply carries besides status, exit_code and output. Each
	// run step's record starts with them set to nil, so templates can name
	// them before the step runs and lint catches a misspelled one.
	Fields []string
}

var builders = []string{"senior-software-engineer", "junior-software-engineer", "qa-lead", "operations-manager"}

// Requests mirrors docs/runner.md. Keep the two in step.
var Requests = []Request{
	{Name: "preflight", Engine: true},
	{Name: "session start", Engine: true},
	{Name: "session resume", Engine: true},
	{Name: "session close", Engine: true},
	{Name: "ticket", Engine: true},
	{Name: "play", Fields: []string{"run"}},
	{Name: "usage check", Fields: []string{"level", "scopes"}},
	{Name: "trust", Fields: []string{"level", "risk", "risk_class", "merge", "deploy", "needs_user"}},
	{Name: "head", Fields: []string{"sha"}},
	{Name: "summary", Roles: []string{"chief-of-staff", "planner"}, Fields: []string{"goals", "workstreams", "spend", "next_seq"}},
	{Name: "worktree add", Roles: builders, Fields: []string{"path", "branch"}},
	{Name: "check", Roles: builders, Fields: []string{"summary", "output_path", "checks"}},
	{Name: "install detect", Roles: []string{"chief-of-staff", "qa-lead"}, Fields: []string{"harnesses"}},
	{Name: "install status", Roles: []string{"chief-of-staff", "qa-lead"}, Fields: []string{"files"}},
	{Name: "install write", Roles: []string{"chief-of-staff", "qa-lead"}, Fields: []string{"path", "branch", "commit"}},
	{Name: "open pr", Roles: []string{"operations-manager", "chief-of-staff"}, Fields: []string{"url", "number"}},
	{Name: "pr status", Roles: []string{"operations-manager"}, Fields: []string{"state", "sha", "merge_sha", "url"}},
	{Name: "merge", Roles: []string{"operations-manager"}, Fields: []string{"sha", "merge_sha"}},
	{Name: "deploy", Roles: []string{"operations-manager"}},
	{Name: "rollback", Roles: []string{"operations-manager"}},
	{Name: "smoke", Roles: []string{"operations-manager"}},
	{Name: "logs", Roles: []string{"operations-manager"}, Fields: []string{"count", "files", "cursor", "new_lines", "due", "next_cursor"}},
	{Name: "memory scan", Roles: []string{"operations-manager"}, Fields: []string{"proposals", "changed", "flagged", "size", "budget", "over_budget"}},
	{Name: "housekeep", Roles: []string{"operations-manager"}, Fields: []string{"plan", "items", "snapshot_recent", "snapshot"}},
	{Name: "evals", Roles: []string{"operations-manager"}, Fields: []string{"results", "passed", "failed"}},
	{Name: "stop", Roles: []string{"chief-of-staff"}},
	{Name: "resume", Roles: []string{"chief-of-staff"}},
}

// FindRequest matches the longest request name at the start of words.
func FindRequest(words []string) *Request {
	var best *Request
	for i := range Requests {
		r := &Requests[i]
		name := strings.Fields(r.Name)
		if len(name) > len(words) {
			continue
		}
		match := true
		for j := range name {
			if words[j] != name[j] {
				match = false
				break
			}
		}
		if match && (best == nil || len(name) > len(strings.Fields(best.Name))) {
			best = r
		}
	}
	return best
}

// Allows reports whether role may send the request.
func (r *Request) Allows(role string) bool {
	return r.Roles == nil || Contains(r.Roles, role)
}

// TicketSub describes what a ticket: step may run and which flags the
// engine adds to it.
type TicketSub struct {
	// Write subcommands get --idempotency-key and --actor.
	Write bool
	// Leased subcommands get --holder and --token while the run holds the
	// ticket's lease.
	Leased bool
	// ActorRole subcommands get --actor-role.
	ActorRole bool
}

// TicketSubs are the maestro-ticket subcommands a playbook may call. claim,
// heartbeat, release and keepalive belong to the engine.
var TicketSubs = map[string]TicketSub{
	"get":        {},
	"list":       {},
	"events":     {},
	"doctor":     {},
	"create":     {Write: true},
	"update":     {Write: true, Leased: true},
	"comment":    {Write: true, Leased: true},
	"transition": {Write: true, Leased: true, ActorRole: true},
}

// EngineTicketFlags are added by the engine, or never set: a ticket: step
// must not set them, so a reply value rendered into one can't either. The
// run's role is always the actor role; the store and files on disk are not
// a playbook's to pick (body: is the step's body).
var EngineTicketFlags = []string{"--json", "--holder", "--token", "--idempotency-key", "--actor", "--actor-role", "--store", "--body-file"}

// RunnerFlags are the flags the engine puts before a runner request's
// words; a run: step must not set them.
var RunnerFlags = []string{"--json", "--bot", "--role", "--ticket", "--run"}
