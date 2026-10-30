package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

// write runs one wrap-up ticket write once: the part is recorded in
// st.Wrapped, and the idempotency key makes a replay after a crash safe.
// It returns false when the lease is lost.
func (e *Engine) write(st *State, part string, args ...string) (bool, string, error) {
	if st.Wrapped[part] {
		return true, "", nil
	}
	full := append(append([]string(nil), args...), "--json", "--idempotency-key", st.ID+":"+part)
	if !hasFlag(args, "--actor") {
		full = append(full, "--actor", st.Role)
	}
	if st.Token > 0 {
		full = append(full, "--holder", st.Holder, "--token", fmt.Sprint(st.Token))
	}
	call, err := e.ticket(full...)
	if err != nil {
		return false, "", err
	}
	if call.exit == 4 && call.code == "idempotency_conflict" {
		call.exit = 0 // landed before a crash, under an older token
	}
	if lostLease(st, call) {
		st.Status, st.StopReason = StatusLost, part+": "+call.code
		return false, "", nil
	}
	if call.exit != 0 {
		return true, fmt.Sprintf("%s: exit %d: %s", part, call.exit, call.output), nil
	}
	st.Wrapped[part] = true
	return true, "", e.save(st)
}

// render renders a done: or block: field; an empty template is "".
func (e *Engine) render(p *playbook.Playbook, st *State, name, tmpl string) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	out, err := renderTemplate(name, tmpl, e.data(p, st, nil, nil))
	return strings.TrimSpace(out), err
}

// finish is the built-in done: memory proposals to the inbox, the note as
// a comment, the move with --release (or a release for to: keep), the
// cursor, the log line.
func (e *Engine) finish(p *playbook.Playbook, st *State) error {
	if err := e.writeMemory(p, st); err != nil {
		return err
	}
	fail := func(msg string) error {
		st.blockAt("done", msg)
		return e.escalate(p, st)
	}
	if st.Ticket != "" {
		to, err := e.render(p, st, "done.to", p.Done.To)
		if err != nil {
			return fail("done.to: " + err.Error())
		}
		if to != "" && to != "keep" && !playbook.Contains(playbook.Statuses, to) {
			return fail(fmt.Sprintf("done.to rendered %q, not a status", to))
		}
		if to == "done" && !playbook.Contains(DoneEnders, p.Role+"/"+p.Name) {
			return fail(fmt.Sprintf("%s/%s may not move a ticket to done; /review does", p.Role, p.Name))
		}
		role, err := e.render(p, st, "done.role", p.Done.Role)
		if err != nil {
			return fail("done.role: " + err.Error())
		}
		actor, err := e.render(p, st, "done.actor", p.Done.Actor)
		if err != nil {
			return fail("done.actor: " + err.Error())
		}
		if !st.Wrapped["done-note"] {
			note, err := e.render(p, st, "done.note", p.Done.Note)
			if err != nil {
				return fail("done.note: " + err.Error())
			}
			if note != "" {
				ok, msg, err := e.write(st, "done-note", "comment", st.Ticket, "--body", note)
				if err != nil || !ok {
					return err
				}
				if msg != "" {
					return fail(msg)
				}
			}
		}
		var ok bool
		var msg string
		switch {
		case to != "" && to != "keep":
			args := []string{"transition", st.Ticket, "--to", to, "--actor-role", st.Role}
			if role != "" {
				args = append(args, "--role", role)
			}
			if actor != "" {
				args = append(args, "--actor", actor)
			}
			if st.Token > 0 {
				args = append(args, "--release")
			}
			ok, msg, err = e.write(st, "done-move", args...)
		case st.Token > 0:
			ok, msg, err = e.write(st, "done-release", "release", st.Ticket)
		default:
			ok = true
		}
		if err != nil || !ok {
			return err
		}
		if msg != "" {
			return fail(msg)
		}
		st.Token = 0
		st.Wrapped["released"] = true
		e.readTicket(st)
	}
	if p.Cursor != nil && p.Cursor.Save != "" && !st.Wrapped["cursor"] {
		v, err := e.render(p, st, "cursor.save", p.Cursor.Save)
		if err != nil {
			return fail("cursor.save: " + err.Error())
		}
		if err := e.saveCursor(st.Bot, p.Cursor.Name, v); err != nil {
			return err
		}
		st.Wrapped["cursor"] = true
	}
	st.Status = StatusDone
	return e.logLine(p, st, "")
}

// end is on: end: no note and no move, but the lease is released, the log
// line written and the run cleaned up.
func (e *Engine) end(p *playbook.Playbook, st *State) error {
	if err := e.writeMemory(p, st); err != nil {
		return err
	}
	if st.Ticket != "" && st.Token > 0 {
		ok, msg, err := e.write(st, "end-release", "release", st.Ticket)
		if err != nil || !ok {
			return err
		}
		if msg != "" {
			st.StopReason = msg
		}
		st.Token = 0
		st.Wrapped["released"] = true
	}
	st.Status = StatusDone
	return e.logLine(p, st, "")
}

func (e *Engine) writeMemory(p *playbook.Playbook, st *State) error {
	ids, mem := memories(p, st)
	for _, id := range ids {
		b, err := json.MarshalIndent(map[string]any{
			"run": st.ID, "ticket": st.Ticket, "role": st.Role, "bot": st.Bot, "playbook": st.Playbook,
			"step": id, "memory": mem[id], "created": e.now().UTC(),
		}, "", "  ")
		if err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(e.cfg.Common, "memory-inbox", st.ID+"-"+id+".json"), b); err != nil {
			return err
		}
	}
	return nil
}

// escalate is the built-in block: _escalate.md in the same session, the
// Escalation: note, the move to blocked for the owner, the release.
func (e *Engine) escalate(p *playbook.Playbook, st *State) error {
	st.Phase = phaseBlock
	if st.Ticket == "" {
		return e.escalateAlone(p, st)
	}
	if st.Escalation == nil && !st.NoPrompt {
		if err := e.escalatePrompt(p, st); err != nil {
			return err
		}
		if st.Status == StatusStopped {
			return nil
		}
	}
	t := st.TicketD
	blk := p.Block
	if s := p.Step(st.BlockStep); s != nil && s.Block != nil {
		blk = *s.Block
	}
	to, err := e.render(p, st, "block.to", blk.To)
	if err != nil || (to != "" && to != "owner" && !playbook.Contains(playbook.Roles, to)) {
		st.StopReason += fmt.Sprintf("; block.to %q didn't render a role, so it goes to the owner", to)
		to = "owner"
	}
	moveTo := blk.MoveTo(to)
	if moveTo == "blocked" && str(t["status"]) == "blocked" {
		moveTo = "keep" // the store has no blocked -> blocked move: note and release
	}
	ok, msg, err := e.write(st, "escalate", "comment", st.Ticket, "--body", e.escalationNote(p, st, to))
	if err != nil || !ok {
		return err
	}
	if msg == "" {
		var args []string
		switch {
		case moveTo == "keep" && st.Token > 0:
			args = []string{"release", st.Ticket}
		case moveTo != "keep":
			args = []string{"transition", st.Ticket, "--to", moveTo, "--actor-role", st.Role,
				"--note", oneLine(playbook.Prefix(to) + " " + st.StopReason)}
			if role := blockRole(st, to); role != "" {
				args = append(args, "--role", role)
			}
			if st.Token > 0 {
				args = append(args, "--release")
			}
		}
		if args != nil {
			ok, msg, err = e.write(st, "escalate-move", args...)
			if err != nil || !ok {
				return err
			}
		}
	}
	if msg != "" {
		st.StopReason += "; escalation write failed: " + msg
	} else {
		st.Token = 0
		st.Wrapped["released"] = true
	}
	st.Status = StatusBlocked
	return e.logLine(p, st, st.StopReason)
}

// escalateAlone is block for a run with no ticket: a blocked chore ticket
// for the run's role, with the Escalation (chief-of-staff): note, so the
// Chief of Staff finds it like any other.
func (e *Engine) escalateAlone(p *playbook.Playbook, st *State) error {
	if st.Escalation == nil && !st.NoPrompt && st.SessionStarted {
		if err := e.escalatePrompt(p, st); err != nil {
			return err
		}
		if st.Status == StatusStopped {
			return nil
		}
	}
	st.Status = StatusBlocked
	if st.EscalationTicket == "" {
		call, err := e.ticket("create", "--type", "ship", "--role", st.Role, "--owner", st.Role, "--status", "blocked",
			"--title", oneLine(fmt.Sprintf("%s blocked at %s", p.Ref(), st.StoppedAt)),
			"--body", "Type: chore\nPlaybook run "+st.ID+" blocked: "+flat(st.StopReason),
			"--json", "--idempotency-key", st.ID+":escalate-create", "--actor", st.Role)
		if err != nil {
			return err
		}
		m, _ := call.result.(map[string]any)
		if call.exit != 0 || str(m["id"]) == "" {
			st.StopReason += fmt.Sprintf("; escalation ticket: exit %d: %s", call.exit, call.output)
			return e.logLine(p, st, st.StopReason)
		}
		st.EscalationTicket = str(m["id"])
		if err := e.save(st); err != nil {
			return err
		}
	}
	note := e.escalationNote(p, st, "chief-of-staff")
	ok, msg, err := e.write(st, "escalate", "comment", st.EscalationTicket, "--body", note)
	if err != nil || !ok {
		return err
	}
	if msg != "" {
		st.StopReason += "; escalation write failed: " + msg
	} else if !strings.Contains(st.StopReason, "escalated on ") {
		st.StopReason += "; escalated on " + st.EscalationTicket
	}
	return e.logLine(p, st, st.StopReason)
}

// blockRole is the --role an escalation hands the ticket to: the owner
// role, or the role block.to names.
func blockRole(st *State, to string) string {
	if to == "" || to == "owner" {
		return str(st.TicketD["owner"])
	}
	return to
}

// flat puts s on one line.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// oneLine is flat, cut to 200 bytes.
func oneLine(s string) string {
	s = flat(s)
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return s
}

// escalatePrompt asks the session for the escalation's content. If it
// can't, the note falls back on the stop reason.
func (e *Engine) escalatePrompt(p *playbook.Playbook, st *State) error {
	data := e.data(p, st, nil, nil)
	prompt, err := e.compose("_escalate", data, str(st.Env["harness"]))
	if err != nil {
		st.Escalation = map[string]any{"error": "render _escalate.md: " + err.Error()}
		return nil
	}
	r, err := e.session(st, "_escalate", prompt, playbook.EscalateFields, "mid", "", false)
	if err != nil {
		return err
	}
	if r["status"] == "stopped" {
		st.Status = StatusStopped
		return nil
	}
	reply := harnessReply(r)
	if checkReply(reply, nil) != "" {
		st.Escalation = map[string]any{"error": fmt.Sprintf("escalate reply: %v %v", r["status"], r["output"])}
		return nil
	}
	if ps, err := processes(reply["processes"]); err == nil && reply["processes"] != nil {
		st.Processes = append(st.Processes, ps...)
	}
	st.Escalation = reply
	return e.save(st)
}

// escalationNote is the Escalation: comment.
func (e *Engine) escalationNote(p *playbook.Playbook, st *State, to string) string {
	esc := st.Escalation
	var b strings.Builder
	// Every value from a reply or a command is flattened to one line, so
	// none can start a line a trailer reader would take.
	happened := flat(str(esc["happened"]))
	if happened == "" {
		happened = flat(st.StopReason)
	}
	fmt.Fprintf(&b, "%s %s\n", playbook.Prefix(to), happened)
	fmt.Fprintf(&b, "Stopped at: %s (%s): %s\n", st.StoppedAt, st.Playbook, flat(st.StopReason))
	list := func(title string, v any) {
		switch l := v.(type) {
		case []any:
			if len(l) == 0 {
				return
			}
			fmt.Fprintf(&b, "%s:\n", title)
			for _, x := range l {
				fmt.Fprintf(&b, "- %s\n", oneLine(str(x)))
			}
		case nil:
		default:
			if s := flat(str(l)); s != "" {
				fmt.Fprintf(&b, "%s: %s\n", title, s)
			}
		}
	}
	list("What was tried", esc["tried"])
	list("Options", esc["options"])
	list("Recommendation", esc["recommendation"])
	list("Decision needed", esc["question"])
	if st.Branch != "" || st.Worktree != "" {
		fmt.Fprintf(&b, "Where: branch %s, worktree %s\n", flat(st.Branch), flat(st.Worktree))
	}
	fmt.Fprintf(&b, "Tries: %d\n", toInt(st.TicketD["tries"]))
	for _, s := range st.Skipped {
		fmt.Fprintln(&b, s)
	}
	fmt.Fprintf(&b, "Run: %s\n", st.ID)
	ret := st.ClaimRole
	if ret == "" {
		ret = str(st.TicketD["role"])
	}
	if ret == "" {
		ret = st.Role // a run with no ticket
	}
	fmt.Fprintf(&b, "Return-role: %s", ret)
	return b.String()
}

// logLine appends one JSON line per run to logs/<date>.jsonl, once.
func (e *Engine) logLine(p *playbook.Playbook, st *State, failure string) error {
	if st.Wrapped["log"] {
		return nil
	}
	path := filepath.Join(e.cfg.Common, "logs", e.now().UTC().Format("2006-01-02")+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if logged(path, st.ID) {
		st.Wrapped["log"] = true
		return nil
	}
	var fail any
	if failure != "" {
		fail = failure
	}
	note, _ := e.render(p, st, "done.log", p.Done.Log)
	line := map[string]any{
		"note": note,
		"ts":   e.now().UTC().Format("2006-01-02T15:04:05Z"), "run": st.ID, "ticket": st.Ticket, "role": st.Role,
		"holder": st.Holder, "playbook": p.Name, "status": str(st.TicketD["status"]), "tries": toInt(st.TicketD["tries"]),
		"failure": fail, "retro": retros(p, st), "skipped": st.Skipped, "run_status": st.Status,
	}
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	st.Wrapped["log"] = true
	return nil
}

func logged(path, runID string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	needle := `"run":"` + runID + `"`
	for sc.Scan() {
		if strings.Contains(sc.Text(), needle) {
			return true
		}
	}
	return false
}

// cleanup kills the processes replies listed while their command still
// matches and removes the worktree if it's clean. The branch stays, so a
// later run reuses it. The harness keeps the session's transcript; there
// is nothing to close.
func (e *Engine) cleanup(st *State) {
	for _, pr := range st.Processes {
		if cmdline(pr.PID) == pr.Command && pr.PID != os.Getpid() {
			syscall.Kill(pr.PID, syscall.SIGTERM)
		}
	}
	st.Processes = nil
	if st.Worktree != "" {
		if r, err := e.git(st, st.Worktree, "status", "--porcelain"); err == nil && r.exit == 0 && strings.TrimSpace(r.stdout) == "" {
			if r, err := e.git(st, e.cfg.Dir, "worktree", "remove", st.Worktree); err == nil && r.exit == 0 {
				st.Worktree = ""
			}
		}
	}
	st.SessionStarted = false
}

// cmdline is the process's command line, space-joined, or "".
func cmdline(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err == nil {
		return strings.TrimSpace(strings.ReplaceAll(strings.TrimRight(string(b), "\x00"), "\x00", " "))
	}
	out, err := exec.Command("ps", "-o", "args=", "-p", fmt.Sprint(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
