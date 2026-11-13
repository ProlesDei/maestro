package cli

import (
	"strings"
	"unicode"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// noBody is a ticket as a write prints it: without its body. The caller
// wrote the body or reads it with get, and claim, heartbeat and transition
// run hundreds of times a day, each copy landing in a session's context.
// The outer Body shadows the embedded one, and omitempty drops it.
type noBody struct {
	ticket.Ticket
	Body *string `json:"body,omitempty"`
}

func withoutBody(t ticket.Ticket) noBody { return noBody{Ticket: t} }

// commentOut is a comment as get prints it. A trimmed comment keeps its
// first line and drops its body, so a reader that wants the text has to
// ask get --full for it, never mistaking a first line for the whole.
type commentOut struct {
	ticket.Comment
	Body      *string `json:"body,omitempty"`
	Trimmed   bool    `json:"trimmed,omitempty"`
	FirstLine string  `json:"first_line,omitempty"`
}

// briefDetail is get's default view: every field and the body, with the
// comments brief keeps in full and the first line of each other one.
type briefDetail struct {
	ticket.Ticket
	Comments []commentOut `json:"comments"`
}

func brief(d ticket.Detail) briefDetail {
	keep := keptComments(d.Comments)
	out := briefDetail{Ticket: d.Ticket, Comments: make([]commentOut, len(d.Comments))}
	for i, c := range d.Comments {
		o := commentOut{Comment: c}
		if keep[i] {
			o.Body = &d.Comments[i].Body
		} else {
			o.Trimmed, o.FirstLine = true, firstLine(c.Body)
		}
		out.Comments[i] = o
	}
	return out
}

// keptComments marks the comments get prints in full by default: the
// newest of each kind, every Answer:, and the newest Handoff: and every
// comment after it (all of them on a ticket with no Handoff:). These are
// what a rerun, a review, QA and the planner act on; the rest of the trail
// is history a session asks for with --full.
func keptComments(cs []ticket.Comment) []bool {
	keep := make([]bool, len(cs))
	seen := map[string]bool{}
	handoff := false
	for i := len(cs) - 1; i >= 0; i-- {
		f := strings.Fields(firstLine(cs[i].Body))
		k := commentKind(f)
		if !seen[k] || !handoff || (len(f) > 0 && f[0] == "Answer:") {
			keep[i] = true
		}
		seen[k] = true
		if len(f) > 0 && f[0] == "Handoff:" {
			handoff = true
		}
	}
	return keep
}

// commentKind names what a comment is from its first line: its first
// word, and the second too when that is all letters, so "Kickback: QA
// fail" and review's "Kickback: 2 blockers" are two kinds, as are
// "Escalation: review" and "Escalation: QA".
func commentKind(fields []string) string {
	switch {
	case len(fields) == 0:
		return ""
	case len(fields) > 1 && allLetters(fields[1]):
		return fields[0] + " " + fields[1]
	}
	return fields[0]
}

func allLetters(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return s != ""
}

// firstLine is the text up to the first line break of any kind.
func firstLine(body string) string {
	l, _, _ := strings.Cut(strings.TrimLeft(lineBreaks.Replace(body), " \t\n"), "\n")
	return strings.TrimRight(l, " \t")
}
