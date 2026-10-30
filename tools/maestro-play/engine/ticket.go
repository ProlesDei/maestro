package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

// readTicket re-reads the run's ticket into the state and returns it. A
// ticket someone else now holds, while the run thinks it holds the lease,
// means the lease is lost.
func (e *Engine) readTicket(st *State) (map[string]any, *Result) {
	call, err := e.ticket("get", st.Ticket, "--json")
	if err != nil {
		r := failed("%v", err)
		return nil, &r
	}
	m, ok := call.result.(map[string]any)
	if call.exit != 0 || !ok {
		r := failed("get %s: exit %d: %s", st.Ticket, call.exit, call.output)
		return nil, &r
	}
	t := ticketData(m)
	st.TicketD = t
	if st.Token > 0 {
		c, _ := t["claim"].(map[string]any)
		if c == nil || c["holder"] != st.Holder || toInt64(c["token"]) != st.Token || c["expired"] == true {
			r := Result{RunID: st.ID, Status: StatusLost, Ticket: st.Ticket, Playbook: st.Playbook, Reason: "the lease is no longer this run's"}
			return t, &r
		}
	}
	return t, nil
}

// ticketData adds kind and latest to get --json's ticket.
func ticketData(m map[string]any) map[string]any {
	t := merge(zeroTicket(), m)
	t["kind"] = kind(str(t["body"]))
	t["trailers"] = trailers(t)
	if cs, ok := t["comments"].([]any); ok && len(cs) > 0 {
		if c, ok := cs[len(cs)-1].(map[string]any); ok {
			t["latest"] = str(c["body"])
		}
	}
	return t
}

// kind is the body's "Type: <kind>" line.
func kind(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Type:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func bodyHasLine(body, prefix string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}

// statusMismatch says why run --ticket can't take a ticket, or "": it
// checks only the intake's statuses, so a bot can resume a ticket
// that waits on someone.
func statusMismatch(p *playbook.Playbook, t map[string]any) string {
	var want []string
	for _, in := range p.Intake {
		want = append(want, in.Status...)
	}
	status := str(t["status"])
	if len(want) > 0 && !playbook.Contains(want, status) {
		return fmt.Sprintf("status %s; %s takes %s", status, p.Ref(), strings.Join(want, ", "))
	}
	return ""
}

// matchIntake says why a ticket doesn't fit one intake entry, or "".
func matchIntake(in *playbook.Intake, role string, t map[string]any) string {
	if len(in.Status) > 0 && !playbook.Contains(in.Status, str(t["status"])) {
		return fmt.Sprintf("status %s, want %s", str(t["status"]), strings.Join(in.Status, ", "))
	}
	if r := in.TicketRole(role); r != "" && str(t["role"]) != r {
		return fmt.Sprintf("role %s, want %s", str(t["role"]), r)
	}
	return matchFilters(in, t)
}

// matchFilters applies the intake filters list can't: type, kind, body
// lines and the latest comment's prefix.
func matchFilters(in *playbook.Intake, t map[string]any) string {
	if in.Type != "" && str(t["type"]) != in.Type {
		return fmt.Sprintf("type %s, want %s", str(t["type"]), in.Type)
	}
	if len(in.Kinds) > 0 && !playbook.Contains(in.Kinds, str(t["kind"])) {
		return fmt.Sprintf("kind %q, want %s", str(t["kind"]), strings.Join(in.Kinds, ", "))
	}
	if in.Owner != "" && str(t["owner"]) != in.Owner {
		return fmt.Sprintf("owner %s, want %s", str(t["owner"]), in.Owner)
	}
	if in.Title != "" && !strings.HasPrefix(str(t["title"]), in.Title) {
		return fmt.Sprintf("title doesn't start %q", in.Title)
	}
	body := str(t["body"])
	if in.Body != "" && !bodyHasLine(body, in.Body) {
		return fmt.Sprintf("no body line starts %q", in.Body)
	}
	if in.UnlessBody != "" && bodyHasLine(body, in.UnlessBody) {
		return fmt.Sprintf("a body line starts %q", in.UnlessBody)
	}
	if latest, ok := t["latest"].(string); ok {
		if in.Latest != "" && !strings.HasPrefix(latest, in.Latest) {
			return fmt.Sprintf("latest comment doesn't start %q", in.Latest)
		}
		if in.UnlessLatest != "" && strings.HasPrefix(latest, in.UnlessLatest) {
			return fmt.Sprintf("latest comment starts %q", in.UnlessLatest)
		}
	}
	return ""
}

// claim takes the lease, with a keepalive tied to the engine's process. It
// returns the store's error code when someone else has the ticket or it
// can't be claimed yet (claimed, waiting_on_deps, area_busy).
func (e *Engine) claim(st *State) (string, *Result) {
	call, err := e.ticket("claim", st.Ticket, "--holder", st.Holder, "--keepalive", fmt.Sprint(e.cfg.PID),
		"--idempotency-key", st.ID+":claim", "--actor", st.Role, "--json")
	if err != nil {
		r := failed("%v", err)
		return "", &r
	}
	if call.exit != 0 {
		if call.code != "" && call.exit == 4 {
			return call.code, nil
		}
		r := failed("claim %s: exit %d: %s", st.Ticket, call.exit, call.output)
		return "", &r
	}
	m, _ := call.result.(map[string]any)
	c, _ := m["claim"].(map[string]any)
	if c == nil || toInt64(c["token"]) == 0 {
		r := failed("claim %s: no claim.token in the reply", st.Ticket)
		return "", &r
	}
	st.Token = toInt64(c["token"])
	if _, res := e.readTicket(st); res != nil {
		return "", res
	}
	return "", nil
}

// reclaim gets the lease back on resume: a heartbeat if the old lease is
// still live, so the token and every replayed write stay the same, or a new
// claim if it expired. Another holder means the run is lost.
func (e *Engine) reclaim(p *playbook.Playbook, st *State) *Result {
	lost := func(why string) *Result {
		st.Status, st.StopReason = StatusLost, why
		e.save(st)
		return &Result{RunID: st.ID, Status: StatusLost, Ticket: st.Ticket, Playbook: st.Playbook, Reason: why}
	}
	if st.Token > 0 {
		call, err := e.ticket("heartbeat", st.Ticket, "--holder", st.Holder, "--token", fmt.Sprint(st.Token), "--json")
		if err != nil {
			r := failed("%v", err)
			return &r
		}
		if call.exit == 0 {
			// The keepalive the claim started watched the process that
			// crashed or paused; this one needs its own, or the lease ends
			// one TTL into the resumed run.
			if err := e.keepalive(st); err != nil {
				r := failed("keepalive %s: %v", st.Ticket, err)
				return &r
			}
			return nil
		}
		if call.exit != 4 {
			r := failed("heartbeat %s: exit %d: %s", st.Ticket, call.exit, call.output)
			return &r
		}
	}
	// The lease lapsed, so someone else may have moved the ticket on. A run
	// that took it back then would drive a ticket in a state it never saw,
	// and its notes would land after newer ones.
	was := str(st.TicketD["status"])
	st.Token = 0
	if t, res := e.readTicket(st); res != nil {
		return res
	} else if now := str(t["status"]); was != "" && now != was && !inflightWrite(p, st) {
		return lost(fmt.Sprintf("re-claim on resume: the ticket moved from %s to %s while the lease was down", was, now))
	}
	n := st.Visits["_claim"] + 1
	st.Visits["_claim"] = n
	call, err := e.ticket("claim", st.Ticket, "--holder", st.Holder, "--keepalive", fmt.Sprint(e.cfg.PID),
		"--idempotency-key", fmt.Sprintf("%s:claim:%d", st.ID, n+1), "--actor", st.Role, "--json")
	if err != nil {
		r := failed("%v", err)
		return &r
	}
	if call.exit != 0 {
		return lost("re-claim on resume: " + call.code + " " + call.output)
	}
	m, _ := call.result.(map[string]any)
	c, _ := m["claim"].(map[string]any)
	st.Token = toInt64(c["token"])
	if st.Token == 0 {
		return lost("re-claim on resume: no token")
	}
	return nil
}

// inflightWrite reports a crash inside a ticket step, whose own move may
// have landed before the run saw it; replaying its key settles that.
func inflightWrite(p *playbook.Playbook, st *State) bool {
	s := p.Step(st.Inflight)
	return s != nil && s.Kind() == playbook.KindTicket
}

// keepalive starts maestro-ticket keepalive for the run's lease in the
// background, heartbeating while this process lives, as claim --keepalive
// does for a new claim.
func (e *Engine) keepalive(st *State) error {
	cmd := exec.Command(e.cfg.TicketBin, "keepalive", st.Ticket, "--holder", st.Holder,
		"--token", fmt.Sprint(st.Token), "--pid", fmt.Sprint(e.cfg.PID))
	cmd.Dir, cmd.Env = e.cfg.Dir, e.cfg.Environ
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func cursorPath(common, bot, name string) string {
	return filepath.Join(common, "cursors", bot, name)
}

// loadCursor is the playbook cursor's stored value for this bot: an int if
// it's one, else the text; 0 the first time.
func (e *Engine) loadCursor(bot, name string) any {
	b, err := os.ReadFile(cursorPath(e.cfg.Common, bot, name))
	if err != nil {
		return 0
	}
	v := strings.TrimSpace(string(b))
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return v
}

func (e *Engine) saveCursor(bot, name, value string) error {
	return writeAtomic(cursorPath(e.cfg.Common, bot, name), []byte(strings.TrimSpace(value)+"\n"))
}
