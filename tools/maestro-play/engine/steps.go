package engine

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

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
		switch out.status {
		case "":
		case StatusLost, StatusStopped, StatusWaiting:
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

// route picks the next step from on:, or the default: ok, done, skipped
// and every ask choice go to the next step, anything else blocks.
func (e *Engine) route(p *playbook.Playbook, st *State, s *playbook.Step, outcome string) {
	target, ok := s.On[outcome]
	if !ok && strings.HasPrefix(outcome, "exit:") {
		target, ok = s.On["fail"]
	}
	if !ok {
		switch {
		case outcome == "ok", outcome == "done", outcome == "skipped":
			target = playbook.TargetNext
		case s.Kind() == playbook.KindAsk && s.Each == "":
			target = playbook.TargetNext
		default:
			target = playbook.TargetBlock
		}
	}
	switch target {
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

// call is one execution of a step: the whole step, or one item of an
// each: step.
type call struct {
	rec     map[string]any // where the outcome goes
	extra   map[string]any // .item and .index
	suffix  string         // added to the idempotency key and file names
	index   int            // the item, or -1
	resumed bool           // the run crashed or paused inside this call
}

func (c call) label(s *playbook.Step) string {
	if c.index >= 0 {
		return fmt.Sprintf("%s[%d]", s.ID, c.index)
	}
	return s.ID
}

func (e *Engine) step(p *playbook.Playbook, st *State, s *playbook.Step, resumed bool) (stepOut, error) {
	rec := st.Steps[s.ID]
	rec["visits"] = st.Visits[s.ID]
	if s.Each != "" {
		return e.eachStep(p, st, s, resumed)
	}
	return e.one(p, st, s, call{rec: rec, index: -1, resumed: resumed})
}

// one runs a step once: when:, then its type.
func (e *Engine) one(p *playbook.Playbook, st *State, s *playbook.Step, c call) (stepOut, error) {
	answered := s.Kind() == playbook.KindAsk && st.Ask != nil && st.Ask.Step == s.ID && st.Ask.Index == c.index && st.Ask.Answered
	if s.When != nil && !answered {
		d := e.data(p, st, s, c.extra)
		ok, err := renderBool(c.label(s)+".when", s.When.If, d)
		if err != nil {
			return blockOut("step %s: when: %v", c.label(s), err), nil
		}
		if !ok {
			reason := "when was false: " + strings.TrimSpace(s.When.If)
			if tmpl := firstNonEmpty(s.When.Reason, s.Reason); tmpl != "" {
				if r, err := renderTemplate(c.label(s)+".reason", tmpl, d); err == nil {
					reason = strings.TrimSpace(r)
				}
			}
			line := fmt.Sprintf("Skipped: %s — %s", c.label(s), reason)
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
		ok, err := renderBool(c.label(s)+".gate", s.Gate, e.data(p, st, s, c.extra))
		if err != nil {
			return blockOut("step %s: gate: %v", c.label(s), err), nil
		}
		c.rec["value"], c.rec["status"] = ok, "ok"
		if ok {
			return stepOut{outcome: "ok"}, nil
		}
		return stepOut{outcome: "fail"}, nil
	case playbook.KindWait:
		return e.waitStep(p, st, s)
	case playbook.KindAsk:
		return e.askStep(p, st, s, c)
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

// items evaluates an each: path. A path that names nothing is no items.
func (e *Engine) items(p *playbook.Playbook, st *State, s *playbook.Step) ([]any, error) {
	var got any
	t, err := template.New(s.ID + ".each").Funcs(funcs).
		Funcs(template.FuncMap{"capture": func(v any) string { got = v; return "" }}).
		Option("missingkey=error").Parse("{{capture (" + s.Each + ")}}")
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	if err := t.Execute(&b, e.data(p, st, s, nil)); err != nil {
		if zeroDataError(err) || strings.Contains(err.Error(), "map has no entry") {
			return nil, nil
		}
		return nil, err
	}
	switch l := got.(type) {
	case []any:
		return l, nil
	case nil:
		return nil, nil
	case []map[string]any:
		out := make([]any, len(l))
		for i := range l {
			out[i] = l[i]
		}
		return out, nil
	}
	return nil, fmt.Errorf("each: %s is a %T, not a list", s.Each, got)
}

// eachStep runs the step once per item. Items already done (before a
// crash or an ask) aren't run again. The outcome is fail if any item
// failed, after trying them all.
func (e *Engine) eachStep(p *playbook.Playbook, st *State, s *playbook.Step, resumed bool) (stepOut, error) {
	rec := st.Steps[s.ID]
	items, err := e.items(p, st, s)
	if err != nil {
		return blockOut("step %s: %v", s.ID, err), nil
	}
	results, _ := rec["results"].([]any)
	if !resumed || results == nil {
		results = []any{}
	}
	rec["results"] = results
	// A run: list is a sequence: its first failure leaves later items unrun.
	seq := len(s.Run) > 1
	for i := len(results); i < len(items) && !(seq && anyFailed(s, results)); i++ {
		irec := zeroRecord(s)
		c := call{rec: irec, extra: map[string]any{"item": items[i], "index": i}, suffix: fmt.Sprint(i), index: i,
			resumed: resumed && i == len(results)}
		if pending, ok := rec["pending"].(map[string]any); ok && c.resumed {
			c.rec = pending
		}
		out, err := e.one(p, st, s, c)
		if err != nil {
			return out, err
		}
		if out.status != "" {
			rec["pending"] = c.rec
			rec["results"] = results
			return out, nil
		}
		c.rec["outcome"] = out.outcome
		results = append(results, c.rec)
		rec["results"] = results
		delete(rec, "pending")
		if err := e.save(st); err != nil {
			return stepOut{}, err
		}
	}
	if anyFailed(s, results) {
		rec["status"] = "failed"
		return stepOut{outcome: "fail"}, nil
	}
	rec["status"] = "ok"
	return stepOut{outcome: "ok"}, nil
}

// anyFailed reports whether an item of an each: step ended in anything but
// ok, done, a skip or one of its ask choices.
func anyFailed(s *playbook.Step, results []any) bool {
	for _, r := range results {
		m, _ := r.(map[string]any)
		o := str(m["outcome"])
		if o == "ok" || o == "done" || o == "skipped" || (s.Ask != nil && playbook.Contains(s.Ask.Choices, o)) {
			continue
		}
		return true
	}
	return false
}

// key is the idempotency key for a write of a step: <run>:<step>, with the
// visit number from the second visit on so a loop never replays an old
// write, and the item's index for an each: step.
func key(st *State, step, suffix string) string {
	k := st.ID + ":" + step
	if n := st.Visits[step]; n > 1 {
		k += fmt.Sprintf(":%d", n)
	}
	if suffix != "" {
		k += ":" + suffix
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

// runnerFlags refuses a run: line that sets a flag the engine sends the
// runner itself, which a runner reading flags anywhere would take.
func runnerFlags(args []string) error {
	for _, f := range playbook.RunnerFlags {
		if hasFlag(args, f) {
			return fmt.Errorf("run: the engine sends %s itself", f)
		}
	}
	return nil
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
	d := e.data(p, st, s, c.extra)
	args, err := renderArgs(c.label(s), s.Ticket, d)
	if err != nil {
		return blockOut("step %s: %v", c.label(s), err), nil
	}
	if len(args) == 0 {
		return blockOut("step %s: ticket: nothing to run", c.label(s)), nil
	}
	if args[0] == "transition" && flagArg(args, "--to") == "done" && !playbook.Contains(DoneEnders, p.Role+"/"+p.Name) {
		return blockOut("step %s: %s/%s may not move a ticket to done (0760)", c.label(s), p.Role, p.Name), nil
	}
	if s.Body != "" {
		body, err := renderTemplate(c.label(s)+".body", s.Body, d)
		if err != nil {
			return blockOut("step %s: body: %v", c.label(s), err), nil
		}
		if body = strings.TrimSpace(body); body != "" {
			flag := "--body"
			if args[0] == "transition" {
				flag = "--note"
			}
			args = append(args, flag, body)
		}
	}
	return e.ticketCall(st, s, c, args, key(st, s.ID, c.suffix))
}

// ticketCall runs one maestro-ticket call for a step and records it.
func (e *Engine) ticketCall(st *State, s *playbook.Step, c call, args []string, idemKey string) (stepOut, error) {
	full, err := e.ticketArgs(st, args, idemKey)
	if err != nil {
		return blockOut("step %s: %v", c.label(s), err), nil
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

// runStep sends a run: step's requests in order and stops at the first
// that doesn't succeed. The record is the last request's.
func (e *Engine) runStep(p *playbook.Playbook, st *State, s *playbook.Step, c call) (stepOut, error) {
	var out stepOut
	for i, line := range s.Run {
		if i > 0 {
			visits := c.rec["visits"]
			clear(c.rec)
			merge(c.rec, zeroRecord(s))
			c.rec["visits"] = visits
		}
		var err error
		if out, err = e.runLine(p, st, s, c, line); err != nil || out.status != "" || out.outcome != "ok" {
			return out, err
		}
	}
	return out, nil
}

func (e *Engine) runLine(p *playbook.Playbook, st *State, s *playbook.Step, c call, line string) (stepOut, error) {
	args, err := renderArgs(c.label(s), line, e.data(p, st, s, c.extra))
	if err != nil {
		return blockOut("step %s: %v", c.label(s), err), nil
	}
	req := playbook.FindRequest(args)
	if err := runnerFlags(args); err != nil {
		return blockOut("step %s: %v", c.label(s), err), nil
	}
	switch {
	case req == nil:
		return blockOut("step %s: %q isn't a runner request", c.label(s), strings.Join(args, " ")), nil
	case req.Engine:
		return blockOut("step %s: the engine sends %s itself", c.label(s), req.Name), nil
	case req.Name == "play":
		return blockOut("step %s: a run never starts another run (0787)", c.label(s)), nil
	case !req.Allows(st.Role):
		return blockOut("step %s: %s isn't allowed for %s", c.label(s), req.Name, st.Role), nil
	}
	reply, err := e.runner(st, args...)
	if err != nil {
		return stepOut{}, err
	}
	merge(c.rec, reply)
	c.rec["json"] = reply
	switch reply["status"] {
	case "stopped":
		return stepOut{status: StatusStopped}, nil
	case "refused":
		return blockOut("step %s: runner refused %s: %v", c.label(s), req.Name, reply["output"]), nil
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

func (e *Engine) waitStep(p *playbook.Playbook, st *State, s *playbook.Step) (stepOut, error) {
	w := s.Wait
	target := p.Step(w.Step)
	if target == nil || (target.Kind() != playbook.KindRun && target.Kind() != playbook.KindTicket) {
		return blockOut("step %s: wait.step %q isn't a run or ticket step", s.ID, w.Step), nil
	}
	every, timeout, err := w.Durations()
	if err != nil {
		return blockOut("step %s: %v", s.ID, err), nil
	}
	rec := st.Steps[s.ID]
	deadline := e.now().Add(timeout)
	for polls := 1; ; polls++ {
		trec := zeroRecord(target)
		tc := call{rec: trec, index: -1}
		var out stepOut
		if target.Kind() == playbook.KindRun {
			out, err = e.runStep(p, st, target, tc)
		} else {
			args, rerr := renderArgs(target.ID, target.Ticket, e.data(p, st, target, nil))
			if rerr != nil {
				return blockOut("step %s: %v", s.ID, rerr), nil
			}
			out, err = e.ticketCall(st, target, tc, args, key(st, s.ID, fmt.Sprintf("%s:%d", target.ID, polls)))
		}
		if err != nil || out.status != "" {
			return out, err
		}
		trec["outcome"], trec["visits"] = out.outcome, st.Visits[target.ID]
		st.Steps[target.ID] = trec
		rec["polls"] = polls
		ok, err := renderBool(s.ID+".until", w.Until, e.data(p, st, s, nil))
		if err != nil {
			return blockOut("step %s: until: %v", s.ID, err), nil
		}
		if ok {
			rec["status"] = "ok"
			return stepOut{outcome: "ok"}, nil
		}
		if !e.now().Add(every).Before(deadline) {
			rec["status"] = "timeout"
			return stepOut{outcome: "timeout"}, nil
		}
		if err := e.save(st); err != nil {
			return stepOut{}, err
		}
		e.cfg.Sleep(every)
		if e.stopped() {
			return stepOut{status: StatusStopped}, nil
		}
	}
}

func (e *Engine) askStep(p *playbook.Playbook, st *State, s *playbook.Step, c call) (stepOut, error) {
	if st.Ask != nil && st.Ask.Step == s.ID && st.Ask.Index == c.index && st.Ask.Answered {
		c.rec["choice"], c.rec["text"], c.rec["status"] = st.Ask.Choice, st.Ask.Text, "ok"
		choice := st.Ask.Choice
		st.Ask = nil
		return stepOut{outcome: choice}, nil
	}
	q, err := renderTemplate(c.label(s)+".question", s.Ask.Question, e.data(p, st, s, c.extra))
	if err != nil {
		return blockOut("step %s: question: %v", c.label(s), err), nil
	}
	q = strings.TrimSpace(q)
	c.rec["question"] = q
	st.Ask = &AskState{Step: s.ID, Index: c.index, Question: q, Choices: s.Ask.Choices}
	if st.Token > 0 {
		// Nothing heartbeats while the run waits, so stretch the lease.
		ttl, err := s.Ask.LeaseTTL()
		if err != nil {
			return blockOut("step %s: %v", s.ID, err), nil
		}
		hb, err := e.ticket("heartbeat", st.Ticket, "--holder", st.Holder, "--token", fmt.Sprint(st.Token), "--ttl", ttl.String(), "--json")
		if err != nil {
			return stepOut{}, err
		}
		if lostLease(st, hb) {
			return stepOut{status: StatusLost, reason: "heartbeat: " + hb.code}, nil
		}
	}
	return stepOut{status: StatusWaiting}, nil
}

func (e *Engine) limits(st *State) map[string]any {
	l := defaultLimits()
	if m, ok := st.Env["limits"].(map[string]any); ok {
		merge(l, m)
	}
	return l
}

// data is the template data for step s (nil outside a step), with extra's
// keys (.item, .index) on top.
func (e *Engine) data(p *playbook.Playbook, st *State, s *playbook.Step, extra map[string]any) map[string]any {
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
	var cursor any = 0
	if st.Cursor0 != nil {
		cursor = st.Cursor0
	}
	d := map[string]any{
		"ticket": ticket, "project": project, "env": env, "limits": e.limits(st),
		"run": run, "steps": steps, "retry": nil, "mode": "", "step": zeroStep(nil),
		"item": nil, "index": 0, "input": map[string]any{"text": st.Input}, "cursor": cursor,
	}
	if st.Retry != nil {
		d["retry"] = st.Retry
	}
	merge(d, extra)
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
