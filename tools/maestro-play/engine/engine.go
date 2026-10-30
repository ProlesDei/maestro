// Package engine runs playbooks: it claims the ticket, runs each step as a
// maestro-ticket call, a built-in request or a harness prompt, records
// every output in the run state, and does the wrap-up (done), escalation
// (block) and clean-up itself.
package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

// Config is where the engine finds things.
type Config struct {
	Playbooks string // the playbooks folder
	Skills    string // the skills folder (use.yaml)
	Common    string // <git common dir>/maestro
	Dir       string // the main checkout, where commands run
	TicketBin string // maestro-ticket
	GitBin    string // git
	GhBin     string // gh
	ClaudeBin string // claude
	CodexBin  string // codex
	// Harness runs prompt steps: claude (the default) or codex.
	Harness string
	// Models maps a tier (mid, frontier) to the harness's model name.
	Models map[string]string
	// ClaudeArgs and CodexArgs go on every session call. Sessions may do
	// anything unless they hold a permission flag (permissionArgs).
	ClaudeArgs, CodexArgs []string
	// SessionTimeout, CheckTimeout and GitTimeout bound one session, one
	// project check and one git or gh call (defaults 30m, 15m, 5m); a
	// negative one is no limit.
	SessionTimeout, CheckTimeout, GitTimeout time.Duration
	// Limits override the default limits (step_visits, retries).
	Limits  map[string]any
	Environ []string
	// PID is the process the ticket lease stays alive with (claim
	// --keepalive); the engine's own by default.
	PID int
	Now func() time.Time
}

// Engine runs playbooks.
type Engine struct {
	cfg  Config
	uses *playbook.Uses
	// userSSH says the user set their own ssh command (noPromptEnv).
	userSSH bool
	sshOnce sync.Once
}

// New checks the config and reads skills/use.yaml.
func New(cfg Config) (*Engine, error) {
	if cfg.Playbooks == "" || cfg.Common == "" {
		return nil, errors.New("engine: playbooks and common dir are required")
	}
	if cfg.TicketBin == "" {
		cfg.TicketBin = "maestro-ticket"
	}
	for _, b := range []struct {
		p   *string
		def string
	}{
		{&cfg.GitBin, "git"}, {&cfg.GhBin, "gh"}, {&cfg.ClaudeBin, "claude"}, {&cfg.CodexBin, "codex"}, {&cfg.Harness, "claude"},
	} {
		if *b.p == "" {
			*b.p = b.def
		}
	}
	if cfg.PID == 0 {
		cfg.PID = os.Getpid()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	for _, t := range []struct {
		d   *time.Duration
		def time.Duration
	}{
		{&cfg.SessionTimeout, 30 * time.Minute}, {&cfg.CheckTimeout, 15 * time.Minute}, {&cfg.GitTimeout, 5 * time.Minute},
	} {
		if *t.d == 0 {
			*t.d = t.def
		}
	}
	if cfg.Environ == nil {
		cfg.Environ = os.Environ()
	}
	uses := &playbook.Uses{}
	if cfg.Skills != "" {
		var err error
		if uses, err = playbook.LoadUses(cfg.Skills); err != nil {
			return nil, err
		}
	}
	return &Engine{cfg: cfg, uses: uses}, nil
}

func (e *Engine) now() time.Time { return e.cfg.Now() }

// Result is what run and resume report.
type Result struct {
	RunID    string `json:"id,omitempty"`
	Status   string `json:"state"`
	Playbook string `json:"playbook,omitempty"`
	Ticket   string `json:"ticket,omitempty"`
	Step     string `json:"step,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Exit codes of run and resume, by status.
var exitCodes = map[string]int{
	StatusDone: 0, StatusFailed: 1, StatusBlocked: 3, StatusLost: 4, StatusStopped: 7,
}

// ExitCode is the process exit code for the result.
func (r Result) ExitCode() int {
	if c, ok := exitCodes[r.Status]; ok {
		return c
	}
	return 1
}

func failed(format string, args ...any) Result {
	return Result{Status: StatusFailed, Reason: fmt.Sprintf(format, args...)}
}

// RunOpts selects what run does.
type RunOpts struct {
	Ref    string // <role>/<job>
	Ticket string
	Bot    string
	Input  string // .input.text, for playbooks with input:
}

// Run starts a run: the harness check, intake, claim, the steps, then done
// or block, and clean-up.
func (e *Engine) Run(o RunOpts) (Result, error) {
	if id := e.insideRun(); id != "" {
		return failed("refused: this process belongs to run %s, and a run never starts another", id), nil
	}
	if o.Bot == "" || strings.Contains(o.Bot, "/") {
		return failed("--bot: want a bot id without /"), nil
	}
	role, job, _ := strings.Cut(o.Ref, "/")
	if !playbook.Contains(playbook.Roles, role) {
		return failed("unknown role %q", role), nil
	}
	if e.stopped() {
		return Result{Status: StatusStopped, Reason: "the stop file exists"}, nil
	}
	if msg := e.ready(); msg != "" {
		return failed("%s", msg), nil
	}
	env := e.environment()
	if job == "" {
		return failed("run %s: want <role>/<job>", o.Ref), nil
	}
	p, err := playbook.Load(e.cfg.Playbooks, o.Ref)
	if err != nil {
		return failed("%v", err), nil
	}
	if o.Ticket == "" && p.Claim {
		return failed("%s claims a ticket: pass --ticket", p.Ref()), nil
	}
	if p.Input == "required" && strings.TrimSpace(o.Input) == "" {
		return failed("%s needs --input", p.Ref()), nil
	}
	if p.Input == "" && o.Input != "" {
		return failed("%s takes no --input", p.Ref()), nil
	}
	st := e.newState(p, o.Bot, o.Ticket, env)
	st.Input = flat(o.Input)
	if o.Ticket != "" {
		t, res := e.readTicket(st)
		if res != nil {
			return *res, nil
		}
		// The store reads "1" as T-1; from here on the run uses its id, so
		// a step's write to .ticket.id is a write to the run's own ticket.
		if id := str(t["id"]); id != "" {
			st.Ticket = id
		}
		if msg := statusMismatch(p, t); msg != "" {
			return failed("%s: %s", st.Ticket, msg), nil
		}
		if p.Claim {
			code, res := e.claim(st)
			if res != nil {
				return *res, nil
			}
			if code != "" {
				return Result{Status: StatusFailed, Ticket: st.Ticket, Playbook: p.Ref(), Reason: "claim: " + code}, nil
			}
		}
	}
	return e.start(p, st)
}

func (e *Engine) newState(p *playbook.Playbook, bot, ticketID string, env map[string]any) *State {
	session := newUUID()
	st := &State{
		ID: newRunID(p.Name, ticketID, e.now()), Playbook: p.Ref(), Role: p.Role, Bot: bot,
		Holder: bot + "/" + session, Ticket: ticketID, Session: session,
		Status: StatusRunning, Phase: phaseSteps, Visits: map[string]int{},
		Steps: map[string]map[string]any{}, Env: env, Wrapped: map[string]bool{}, Created: e.now(),
	}
	for i := range p.Steps {
		st.Steps[p.Steps[i].ID] = zeroRecord(&p.Steps[i])
	}
	if len(p.Steps) > 0 {
		st.Cursor = p.Steps[0].ID
	}
	return st
}

// stopped reports whether the stop file, <common>/STOP, exists: every run
// stops before its next step until someone removes it.
func (e *Engine) stopped() bool {
	_, err := os.Stat(filepath.Join(e.cfg.Common, "STOP"))
	return err == nil
}

// Resume continues a run after a crash or a stop.
func (e *Engine) Resume(id string) (Result, error) {
	if in := e.insideRun(); in != "" {
		return failed("refused: this process belongs to run %s, and a run never resumes another", in), nil
	}
	if _, err := e.LoadState(id); err != nil {
		return failed("%v", err), nil
	}
	unlock, ok, err := e.lockRun(id)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{RunID: id, Status: StatusFailed, Reason: "another maestro-play process is driving run " + id}, nil
	}
	defer unlock()
	return e.resumeLocked(id)
}

func (e *Engine) resumeLocked(id string) (Result, error) {
	st, err := e.LoadState(id)
	if err != nil {
		return failed("%v", err), nil
	}
	p, err := playbook.Load(e.cfg.Playbooks, st.Playbook)
	if err != nil {
		return failed("%v", err), nil
	}
	if st.Status != StatusRunning && st.Status != StatusStopped {
		return Result{RunID: id, Status: st.Status, Playbook: st.Playbook, Ticket: st.Ticket, Reason: "run already " + st.Status}, nil
	}
	if e.stopped() {
		return e.result(st), nil
	}
	if msg := e.ready(); msg != "" {
		return failed("%s", msg), nil
	}
	env := e.environment()
	if f := str(st.Env["family"]); f != "" {
		env["family"] = f // the last session's, which the notes name
	}
	st.Env = env
	st.Status = StatusRunning
	if st.Ticket != "" {
		if p.Claim && !st.Wrapped["released"] {
			if res := e.reclaim(p, st); res != nil {
				return *res, nil
			}
		}
		if _, res := e.readTicket(st); res != nil {
			return *res, nil
		}
	}
	return e.drive(p, st)
}

// start drives a new run under its lock.
func (e *Engine) start(p *playbook.Playbook, st *State) (Result, error) {
	unlock, ok, err := e.lockRun(st.ID)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return failed("run %s: locked by another process", st.ID), nil
	}
	defer unlock()
	return e.drive(p, st)
}

// drive runs the phases until the run finishes or pauses. The caller holds
// the run's lock.
func (e *Engine) drive(p *playbook.Playbook, st *State) (Result, error) {
	if err := e.save(st); err != nil {
		return Result{}, err
	}
	if st.Phase == phaseSteps {
		if st.ClaimRole == "" && st.TicketD != nil {
			st.ClaimRole = str(st.TicketD["role"])
		}
		if err := e.steps(p, st); err != nil {
			return Result{}, err
		}
		if st.Status != StatusRunning {
			if st.Status == StatusLost {
				e.cleanup(st)
			}
			return e.result(st), e.save(st)
		}
	}
	switch st.Phase {
	case phaseDone:
		if err := e.finish(p, st); err != nil {
			return Result{}, err
		}
	case phaseBlock:
		if err := e.escalate(p, st); err != nil {
			return Result{}, err
		}
	case phaseEnd:
		if err := e.end(p, st); err != nil {
			return Result{}, err
		}
	}
	if st.Status == StatusStopped {
		return e.result(st), e.save(st)
	}
	e.cleanup(st)
	if st.Status == StatusDone {
		os.Remove(statePath(e.cfg.Common, st.ID))
		os.RemoveAll(runDir(e.cfg.Common, st.ID))
		return e.result(st), nil
	}
	return e.result(st), e.save(st)
}

func (e *Engine) result(st *State) Result {
	r := Result{RunID: st.ID, Status: st.Status, Playbook: st.Playbook, Ticket: st.Ticket}
	switch st.Status {
	case StatusBlocked, StatusLost, StatusFailed:
		r.Step, r.Reason = st.StoppedAt, st.StopReason
	case StatusStopped:
		r.Step, r.Reason = st.Cursor, "the stop file exists"
	}
	return r
}

func (e *Engine) evidenceDir(st *State) string {
	return filepath.Join(e.cfg.Common, "evidence", st.Ticket)
}
