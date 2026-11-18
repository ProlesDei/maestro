package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

var (
	// "1. ..." or "2) ...": a numbered line, which is how a body lists its
	// acceptance criteria.
	// A bullet, a checkbox or bold before the number doesn't hide it.
	criterionLine = regexp.MustCompile(`(?m)^\s*(?:[-*+>]\s*)?(?:\[[ xX]\]\s*)?[*_]*[0-9]+[.)][*_]*\s+\S`)
	// "Not handled:", as a line of its own or a heading.
	notHandledLine = regexp.MustCompile(`(?im)^[#*_>\s-]*not handled[*_]*\s*(:|$)`)
)

// maxCriteria is where a ticket is likely two: each criterion costs a
// build, a review and a QA proof.
const maxCriteria = 6

// roleTitles are the roles look hands out by role, and the title each
// one's skill refuses to work without: look sends any qa-lead ticket as a
// live run and any designer ticket as a design, and the session refuses
// one that isn't a scout titled so, after it spent its start.
var roleTitles = map[string]string{
	ticket.RoleQALead:   "Live run:",
	ticket.RoleDesigner: "Design:",
}

// lintTicket checks that a ticket an engineer builds has the shape the
// maestro-create-ticket skill asks for, so a ticket that would cost a
// round in review is caught when it costs a line. It returns what must
// change before the ticket is made, and what is only worth a look. A
// qa-lead or designer ticket must be the scout its job takes; any other
// scout, or a ticket another role works, has no such shape.
func lintTicket(typ ticket.Type, role, title, body string, areas []string) (refuse, warn []string) {
	if want, ok := roleTitles[role]; ok {
		if typ != ticket.TypeScout {
			refuse = append(refuse, fmt.Sprintf("a %s ticket is a scout: pass --type scout", role))
		}
		if !strings.HasPrefix(strings.TrimSpace(title), want) {
			refuse = append(refuse, fmt.Sprintf("a %s ticket's title must start %q, which its job routes on: title it \"%s ...\"", role, want, want))
		}
		return refuse, nil
	}
	if typ != ticket.TypeShip || (role != "" && role != ticket.RoleEngineer) {
		return nil, nil
	}
	n := len(criterionLine.FindAllString(body, -1))
	if n == 0 {
		refuse = append(refuse, "the body has no numbered acceptance criterion (a line such as \"1. ...\")")
	}
	if !notHandledLine.MatchString(body) {
		refuse = append(refuse, "the body has no \"Not handled:\" list: name the cases this ticket leaves, or write \"Not handled: none\"")
	}
	if len(areas) == 0 {
		refuse = append(refuse, "no --areas: with none, the ticket locks no path and a second ticket can build over it")
	}
	if n > maxCriteria {
		warn = append(warn, fmt.Sprintf("%d numbered lines: past about %d criteria, split the ticket", n, maxCriteria))
	}
	return refuse, warn
}

// lintError joins what lint refuses into one error, so one retry fixes
// them all.
func lintError(refuse []string) error {
	return fmt.Errorf("%w: lint: %s", ticket.ErrInvalid, strings.Join(refuse, "; "))
}
