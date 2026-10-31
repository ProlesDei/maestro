package engine

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

// blockAt moves the run to the block phase.
func (st *State) blockAt(step, reason string) {
	st.Phase, st.StoppedAt, st.StopReason, st.BlockStep = phaseBlock, step, reason, step
}

// steps runs steps from the cursor until the run leaves the steps phase or
// pauses.
func (e *Engine) steps(p *playbook.Playbook, st *State) error {
	limit := toInt(e.limits(st)["step_visits"])
	for st.Phase == phaseSteps {
		if e.stopped() {
			st.Status = StatusStopped
			return nil
		}
		idx := p.Index(st.Cursor)
		if idx < 0 {
			st.Phase = phaseDone
			break
		}
		s := &p.Steps[idx]
		resumed := st.Inflight == s.ID
		if !resumed {
			st.Visits[s.ID]++
			st.Steps[s.ID] = zeroRecord(s)
		}
		if st.Visits[s.ID] > limit {
			st.blockAt(s.ID, fmt.Sprintf("step %s ran %d times (limits.step_visits is %d)", s.ID, st.Visits[s.ID]-1, limit))
			break
		}
		st.Inflight = s.ID
		if err := e.save(st); err != nil {
			return err
		}
		out, err := e.step(p, st, s, resumed)
		if err != nil {
			return err
		}
		// A step that would block while the stop file exists, such as a
		// session that saw the kill switch and replied blocked, stops the
		// run instead: a stop across hundreds of runs must not post an
		// Escalation from each. Resume reruns the step as a fresh visit.
		if e.stopped() && (out.status == StatusBlocked || out.status == "" && next(s, out.outcome) == playbook.TargetBlock) {
			st.Visits[s.ID]--
			st.Inflight = ""
			st.Status = StatusStopped
			return nil
		}
		switch out.status {
		case "":
		case StatusLost, StatusStopped:
			st.Status = out.status
			if out.status == StatusLost {
				st.StoppedAt, st.StopReason = s.ID, out.reason
			}
			return nil
		case StatusBlocked:
			st.Inflight = ""
			st.blockAt(s.ID, out.reason)
			if out.noPrompt {
				st.NoPrompt = true
			}
			return nil
		}
		st.Steps[s.ID]["outcome"] = out.outcome
		st.Inflight = ""
		e.route(p, st, s, out.outcome)
		if err := e.save(st); err != nil {
			return err
		}
	}
	return nil
}

// next is where outcome sends the run from s: on:, or the default: ok,
// done and skipped go to the next step, anything else blocks.
func next(s *playbook.Step, outcome string) string {
	target, ok := s.On[outcome]
	if !ok && strings.HasPrefix(outcome, "exit:") {
		target, ok = s.On["fail"]
	}
	if ok {
		return target
	}
	switch outcome {
	case "ok", "done", "skipped":
		return playbook.TargetNext
	}
	return playbook.TargetBlock
}

// route moves the run to where outcome sends it from s.
func (e *Engine) route(p *playbook.Playbook, st *State, s *playbook.Step, outcome string) {
	switch target := next(s, outcome); target {
	case playbook.TargetNext:
		if i := p.Index(s.ID) + 1; i < len(p.Steps) {
			st.Cursor = p.Steps[i].ID
		} else {
			st.Cursor, st.Phase = "", phaseDone
		}
	case playbook.TargetDone:
		st.Cursor, st.Phase = "", phaseDone
	case playbook.TargetEnd:
		st.Cursor, st.Phase = "", phaseEnd
	case playbook.TargetBlock:
		reason := fmt.Sprintf("step %s: %s", s.ID, outcome)
		if r := stepReason(st.Steps[s.ID]); r != "" {
			reason += ": " + r
		}
		st.blockAt(s.ID, reason)
	default:
		st.Cursor = target
	}
}

// stepReason is the most useful line a step's record has for a block note.
func stepReason(rec map[string]any) string {
	if reply, ok := rec["reply"].(map[string]any); ok {
		if s, ok := reply["summary"].(string); ok && s != "" {
			return s
		}
	}
	if s, ok := rec["output"].(string); ok && s != "" {
		lines := strings.Split(strings.TrimSpace(s), "\n")
		return lines[len(lines)-1]
	}
	return ""
}

// stepOut is how a step ended. status is "" for a normal outcome, else the
// run status it forces.
type stepOut struct {
	outcome  string
	status   string
	reason   string
	noPrompt bool
}

func blockOut(format string, args ...any) stepOut {
	return stepOut{status: StatusBlocked, reason: fmt.Sprintf(format, args...)}
}

// call is one execution of a step.
type call struct {
	rec     map[string]any // where the outcome goes
	resumed bool           // the run crashed or paused inside this call
}

// step runs a step: when:, then its type.
func (e *Engine) step(p *playbook.Playbook, st *State, s *playbook.Step, resumed bool) (stepOut, error) {
	c := call{rec: st.Steps[s.ID], resumed: resumed}
	c.rec["visits"] = st.Visits[s.ID]
	if s.When != nil {
		d := e.data(p, st, s)
		ok, err := renderBool(s.ID+".when", s.When.If, d)
		if err != nil {
			return blockOut("step %s: when: %v", s.ID, err), nil
		}
		if !ok {
			reason := "when was false: " + strings.TrimSpace(s.When.If)
			if tmpl := firstNonEmpty(s.When.Reason, s.Reason); tmpl != "" {
				if r, err := renderTemplate(s.ID+".reason", tmpl, d); err == nil {
					reason = strings.TrimSpace(r)
				}
			}
			line := fmt.Sprintf("Skipped: %s — %s", s.ID, reason)
			if !playbook.Contains(st.Skipped, line) {
				st.Skipped = append(st.Skipped, line)
			}
			c.rec["skipped"], c.rec["status"] = true, "skipped"
			return stepOut{outcome: "skipped"}, nil
		}
	}
	switch s.Kind() {
	case playbook.KindTicket:
		return e.ticketStep(p, st, s, c)
	case playbook.KindRun:
		return e.runStep(p, st, s, c)
	case playbook.KindPrompt:
		return e.promptStep(p, st, s, c)
	case playbook.KindGate:
		ok, err := renderBool(s.ID+".gate", s.Gate, e.data(p, st, s))
		if err != nil {
			return blockOut("step %s: gate: %v", s.ID, err), nil
		}
		c.rec["value"], c.rec["status"] = ok, "ok"
		if ok {
			return stepOut{outcome: "ok"}, nil
		}
		return stepOut{outcome: "fail"}, nil
	}
	return blockOut("step %s: no step type", s.ID), nil
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// key is the idempotency key for a write of a step: <run>:<step>, with the
// visit number from the second visit on so a loop never replays an old
// write.
func key(st *State, step string) string {
	k := st.ID + ":" + step
	if n := st.Visits[step]; n > 1 {
		k += fmt.Sprintf(":%d", n)
	}
	return k
}

// hasFlag reports whether args set flag (--x), in either dash form, as Go's
// flag package reads both: --x, --x=v, -x or -x=v.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if isFlag(a, flag) {
			return true
		}
	}
	return false
}

func isFlag(arg, flag string) bool {
	for _, f := range []string{flag, strings.TrimPrefix(flag, "-")} {
		if arg == f || strings.HasPrefix(arg, f+"=") {
			return true
		}
	}
	return false
}

// ticketArgs adds the flags the engine owns to a ticket: step's arguments.
func (e *Engine) ticketArgs(st *State, args []string, idemKey string) ([]string, error) {
	sub, ok := playbook.TicketSubs[args[0]]
	if !ok {
		return nil, fmt.Errorf("ticket %s: not a subcommand playbooks may run", args[0])
	}
	for _, f := range playbook.EngineTicketFlags {
		if hasFlag(args[1:], f) {
			return nil, fmt.Errorf("ticket: %s is the engine's", f)
		}
	}
	out := append(append([]string(nil), args...), "--json")
	if sub.Write {
		out = append(out, "--idempotency-key", idemKey)
		if !hasFlag(args, "--actor") {
			out = append(out, "--actor", st.Role)
		}
	}
	if sub.Leased && st.Token > 0 && targetsRunTicket(st, args) {
		out = append(out, "--holder", st.Holder, "--token", fmt.Sprint(st.Token))
	}
	if sub.ActorRole {
		out = append(out, "--actor-role", st.Role)
	}
	return out, nil
}

// targetsRunTicket reports whether a write names the run's own ticket, the
// only one its lease covers.
func targetsRunTicket(st *State, args []string) bool {
	return len(args) > 1 && args[1] == st.Ticket
}

func (e *Engine) ticketStep(p *playbook.Playbook, st *State, s *playbook.Step, c call) (stepOut, error) {
	d := e.data(p, st, s)
	args, err := renderArgs(s.ID, s.Ticket, d)
	if err != nil {
		return blockOut("step %s: %v", s.ID, err), nil
	}
	if len(args) == 0 {
		return blockOut("step %s: ticket: nothing to run", s.ID), nil
	}
	if args[0] == "transition" && flagArg(args, "--to") == "done" && !playbook.Contains(DoneEnders, p.Role+"/"+p.Name) {
		return blockOut("step %s: %s/%s may not move a ticket to done; /maestro-code-review does", s.ID, p.Role, p.Name), nil
	}
	if s.Body != "" {
		body, err := renderTemplate(s.ID+".body", s.Body, d)
		if err != nil {
			return blockOut("step %s: body: %v", s.ID, err), nil
		}
		if body = strings.TrimSpace(body); body != "" {
			flag := "--body"
			if args[0] == "transition" {
				flag = "--note"
			}
			args = append(args, flag, body)
		}
	}
	return e.ticketCall(st, s, c, args, key(st, s.ID))
}

// ticketCall runs one maestro-ticket call for a step and records it.
func (e *Engine) ticketCall(st *State, s *playbook.Step, c call, args []string, idemKey string) (stepOut, error) {
	full, err := e.ticketArgs(st, args, idemKey)
	if err != nil {
		return blockOut("step %s: %v", s.ID, err), nil
	}
	tc, err := e.ticket(full...)
	if err != nil {
		return stepOut{}, err
	}
	if tc.exit == 4 && tc.code == "idempotency_conflict" && c.resumed {
		// The write landed before the crash, under the lease token the run
		// had then.
		tc = ticketCall{exit: 0, output: "already applied before resume"}
	}
	c.rec["exit_code"], c.rec["output"], c.rec["result"], c.rec["json"] = tc.exit, tc.output, tc.result, tc.result
	c.rec["error"] = nil
	if tc.code != "" {
		c.rec["error"] = tc.code
	}
	if lostLease(st, tc) {
		return stepOut{status: StatusLost, reason: "ticket " + args[0] + ": " + tc.code}, nil
	}
	if tc.exit == 0 && st.Token > 0 && targetsRunTicket(st, args) && dropsLease(args) {
		// The write let the lease go, so the run holds none now: no more
		// --holder and --token, and nothing for end or done to release.
		st.Token = 0
		st.Wrapped["released"] = true
	}
	if playbook.TicketSubs[args[0]].Write && st.Ticket != "" {
		if _, res := e.readTicket(st); res != nil && res.Status == StatusLost {
			return stepOut{status: StatusLost, reason: res.Reason}, nil
		}
	}
	return exitOutcome(c.rec, tc.exit), nil
}

// dropsLease reports whether a write ends the lease: --release, or a move
// to done or cancelled, which clears it in the store.
func dropsLease(args []string) bool {
	if hasFlag(args, "--release") {
		return true
	}
	to := flagArg(args, "--to")
	return args[0] == "transition" && (to == "done" || to == "cancelled")
}

// flagArg is the value of --flag v or --flag=v, or "".
func flagArg(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}

// lostLease: exit 4 because the lease is gone stops the run, with no more
// writes.
func lostLease(st *State, c ticketCall) bool {
	return st.Token > 0 && c.exit == 4 && (c.code == "lease_lost" || c.code == "claimed")
}

func exitOutcome(rec map[string]any, exit int) stepOut {
	if exit == 0 {
		rec["status"] = "ok"
		return stepOut{outcome: "ok"}
	}
	rec["status"] = "failed"
	return stepOut{outcome: fmt.Sprintf("exit:%d", exit)}
}

// runStep sends a run: step's built-in request.
func (e *Engine) runStep(p *playbook.Playbook, st *State, s *playbook.Step, c call) (stepOut, error) {
	args, err := renderArgs(s.ID, s.Run, e.data(p, st, s))
	if err != nil {
		return blockOut("step %s: %v", s.ID, err), nil
	}
	req := playbook.FindRequest(args)
	switch {
	case req == nil:
		return blockOut("step %s: %q isn't a built-in request", s.ID, strings.Join(args, " ")), nil
	case req.Engine:
		return blockOut("step %s: the engine sends %s itself", s.ID, req.Name), nil
	}
	reply, err := e.request(st, req, args[len(strings.Fields(req.Name)):])
	if err != nil {
		return stepOut{}, err
	}
	merge(c.rec, reply)
	c.rec["json"] = reply
	if reply["status"] == "refused" {
		return blockOut("step %s: %s refused: %v", s.ID, req.Name, reply["output"]), nil
	}
	if req.Name == "worktree add" && reply["status"] == "ok" {
		if v, ok := reply["path"].(string); ok && v != "" {
			st.Worktree = v
		}
		if v, ok := reply["branch"].(string); ok && v != "" {
			st.Branch = v
		}
	}
	exit := toInt(reply["exit_code"])
	switch {
	case reply["status"] == "ok":
		exit = 0
	case exit == 0:
		exit = 1
	}
	return exitOutcome(c.rec, exit), nil
}

func (e *Engine) limits(st *State) map[string]any {
	l := defaultLimits()
	if m, ok := st.Env["limits"].(map[string]any); ok {
		merge(l, m)
	}
	return l
}

// data is the template data for step s (nil outside a step).
func (e *Engine) data(p *playbook.Playbook, st *State, s *playbook.Step) map[string]any {
	ticket := zeroTicket()
	if st.TicketD != nil {
		merge(ticket, st.TicketD)
	}
	project := defaultProject()
	if m, ok := st.Env["project"].(map[string]any); ok {
		merge(project, m)
	}
	env := defaultEnv()
	for k, v := range st.Env {
		if k != "project" && k != "limits" && k != "status" && k != "exit_code" && k != "output" {
			env[k] = v
		}
	}
	run := zeroRun()
	merge(run, map[string]any{
		"id": st.ID, "date": e.now().UTC().Format("2006-01-02"), "playbook": st.Playbook, "role": st.Role,
		"bot": st.Bot, "holder": st.Holder, "token": int(st.Token), "session": st.Session, "branch": st.Branch,
		"worktree": st.Worktree, "stopped_at": st.StoppedAt, "stop_reason": st.StopReason,
	})
	run["scratch_dir"] = filepath.Join(e.cfg.Common, "scratch", st.Bot)
	if st.Ticket != "" {
		run["scratch"] = filepath.Join(e.cfg.Common, "scratch", st.Bot, st.Ticket+".md")
		run["evidence_dir"] = e.evidenceDir(st)
	} else {
		run["scratch"] = filepath.Join(e.cfg.Common, "scratch", st.Bot, st.ID+".md")
	}
	if len(st.Skipped) > 0 {
		run["skipped"] = strings.Join(st.Skipped, "\n") + "\n"
	}
	steps := map[string]any{}
	for i := range p.Steps {
		id := p.Steps[i].ID
		rec := zeroRecord(&p.Steps[i])
		if r, ok := st.Steps[id]; ok {
			merge(rec, r)
		}
		steps[id] = rec
	}
	d := map[string]any{
		"ticket": ticket, "project": project, "env": env, "limits": e.limits(st),
		"run": run, "steps": steps, "retry": nil, "mode": "", "step": zeroStep(nil),
		"input": map[string]any{"text": st.Input},
	}
	if st.Retry != nil {
		d["retry"] = st.Retry
	}
	if s != nil {
		d["mode"] = s.Mode
		with := map[string]any{}
		for k, v := range s.Params() {
			out, err := renderTemplate(s.ID+".with."+k, v, d)
			if err != nil {
				out = ""
			}
			with[k] = out
		}
		d["step"] = map[string]any{"id": s.ID, "mode": s.Mode, "with": with, "vars": with}
	}
	return d
}
