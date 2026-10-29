package engine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

type candidate struct {
	pb     *playbook.Playbook
	in     *playbook.Intake
	id     string
	prio   int
	number int
}

// runNext picks the first playbook of the role whose intake matches,
// trying the categories in order (request, sent-back, new-work; scheduled
// playbooks, and request entries with no status, run only by name) and, within
// one, tickets by priority then id. A ticket counts once, in the first
// category it matches. A claim that fails (someone got there first, its
// dependencies aren't done, its area is busy) moves on to the next one.
func (e *Engine) runNext(role, job, bot, input string, env map[string]any) (Result, error) {
	pbs, err := playbook.LoadRole(e.cfg.Playbooks, role)
	if err != nil {
		return failed("%v", err), nil
	}
	if job != "" {
		var only []*playbook.Playbook
		for _, p := range pbs {
			if p.Name == job {
				only = append(only, p)
			}
		}
		if len(only) == 0 {
			return failed("no playbook %s/%s", role, job), nil
		}
		pbs = only
	}
	seen := map[string]bool{}
	for _, cat := range playbook.Categories[:3] {
		var cands []candidate
		inCat := map[string]bool{}
		for _, p := range pbs {
			if p.Input == "required" && input == "" {
				continue
			}
			var list []candidate
			for i := range p.Intake {
				// An entry with no status (a user's request) names no
				// ticket: the bot runs it by name, like a scheduled one.
				if p.Intake[i].Category != cat || len(p.Intake[i].Status) == 0 {
					continue
				}
				l, err := e.intakeList(p, &p.Intake[i])
				if err != nil {
					return Result{}, err
				}
				list = append(list, l...)
			}
			for _, c := range list {
				if seen[c.id] || inCat[c.id] {
					continue
				}
				inCat[c.id] = true
				cands = append(cands, c)
			}
		}
		sort.SliceStable(cands, func(i, j int) bool {
			if cands[i].prio != cands[j].prio {
				return cands[i].prio < cands[j].prio
			}
			return cands[i].number < cands[j].number
		})
		for _, c := range cands {
			seen[c.id] = true
			st := e.newState(c.pb, bot, c.id, env)
			t, res := e.readTicket(st)
			if res != nil {
				continue
			}
			if matchIntake(c.in, c.pb.Role, t) != "" {
				continue
			}
			st.Input = input
			if c.pb.Claim {
				code, res := e.claim(st)
				if res != nil {
					return *res, nil
				}
				if code != "" {
					continue
				}
			}
			return e.start(c.pb, st)
		}
	}
	return Result{Status: StatusIdle, Playbook: role}, nil
}

// intakeList lists the unclaimed tickets one intake entry matches.
func (e *Engine) intakeList(p *playbook.Playbook, in *playbook.Intake) ([]candidate, error) {
	base := []string{"list", "--status", strings.Join(in.Status, ","), "--unclaimed", "--json"}
	if r := in.TicketRole(p.Role); r != "" {
		base = append(base, "--role", r)
	}
	if in.Latest != "" {
		base = append(base, "--latest-prefix", in.Latest)
	}
	if in.UnlessLatest != "" {
		base = append(base, "--not-latest-prefix", in.UnlessLatest)
	}
	tickets, err := e.listTickets(base)
	if err != nil {
		return nil, err
	}
	var out []candidate
	for _, t := range tickets {
		id := str(t["id"])
		td := ticketData(t)
		delete(td, "latest") // list has no comments; checked after get
		if matchFilters(in, td) != "" {
			continue
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(id, "T-"))
		out = append(out, candidate{pb: p, in: in, id: id, prio: toInt(t["priority"]), number: n})
	}
	return out, nil
}

func (e *Engine) listTickets(args []string) ([]map[string]any, error) {
	call, err := e.ticket(args...)
	if err != nil {
		return nil, err
	}
	if call.exit != 0 {
		return nil, fmt.Errorf("maestro-ticket %s: exit %d: %s", strings.Join(args, " "), call.exit, call.output)
	}
	m, _ := call.result.(map[string]any)
	list, _ := m["tickets"].([]any)
	var out []map[string]any
	for _, x := range list {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}
