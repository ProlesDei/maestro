package playbook

import "strings"

// Request is one built-in request a run: step can send. The engine runs
// each one itself (engine/builtin.go); there is no other binary.
type Request struct {
	Name string
	// Engine marks requests only the engine sends: a run: step can't name
	// them.
	Engine bool
	// Usage is the request's shape, for error messages.
	Usage string
	// MinArgs and MaxArgs bound the words after the name that aren't
	// flags or flag values.
	MinArgs, MaxArgs int
	// Flags are the flags the request takes; Need, the ones it must have.
	// Any other flag is refused.
	Flags, Need []string
	// Fields the reply carries besides status, exit_code and output. Each
	// run step's record starts with them set to nil, so templates can name
	// them before the step runs and lint catches a misspelled one.
	Fields []string
}

// Requests are the built-in requests. README.md lists what each one runs.
var Requests = []Request{
	{Name: "session start", Engine: true},
	{Name: "session resume", Engine: true},
	{Name: "worktree add", Usage: "worktree add <ticket> --from <branch>", MinArgs: 1, MaxArgs: 1,
		Flags: []string{"--from"}, Need: []string{"--from"}, Fields: []string{"path", "branch"}},
	{Name: "check", Usage: "check all, or check <name>", MinArgs: 1, MaxArgs: 1,
		Fields: []string{"summary", "checks", "output_path"}},
	{Name: "head", Usage: "head", Fields: []string{"sha"}},
	{Name: "open pr", Usage: "open pr [--title <t>] [--body <b>]", Flags: []string{"--title", "--body"},
		Fields: []string{"url", "number"}},
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
