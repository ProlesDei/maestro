package cli

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// A note that ends a run's turn on a ticket, and the move that goes with
// it. The session writes the note; the comment, the move and the release
// are fixed steps, so they are one call here, checked before any write.
type noteKind struct {
	name    string
	from    ticket.Status
	to      ticket.Status // "" posts the note and moves nothing
	release bool
	note    string // the move's note, before the sha
}

var (
	// "Kickback: 2 blockers at <sha>", or "at none" for a PR that isn't there.
	kickbackHead = regexp.MustCompile(`^Kickback: ([0-9]+) blockers? at ([0-9a-f]{40}|none)(\s|$)`)
	// "- F3 [major, auto-fix] file:line: ...": a finding and its severity.
	findingLine = regexp.MustCompile(`^\s*[-*]\s+(F[0-9]+)\s+\[\s*([a-z]+)`)
	atSHA       = regexp.MustCompile(` at [0-9a-f]{40}(\W|$)`)
)

// verdictKind sorts a review's or QA's note by its first line and checks
// the note says what its kind must. Only a blocker sends a change back
// (docs/decisions.md, 22), so a Kickback: that lists anything else is
// refused here, before the engineer pays a round for it.
func verdictKind(body string) (noteKind, error) {
	first := firstLine(body)
	bad := func(format string, a ...any) (noteKind, error) {
		return noteKind{}, fmt.Errorf("%w: verdict: %s", ticket.ErrInvalid, fmt.Sprintf(format, a...))
	}
	var k noteKind
	switch {
	case strings.HasPrefix(first, "Review: GO at "):
		k = noteKind{name: "review-go", from: ticket.StatusInReview}
	case strings.HasPrefix(first, "QA: pass at "):
		k = noteKind{name: "qa-pass", from: ticket.StatusInReview}
	case strings.HasPrefix(first, "Rereview: QA at "):
		k = noteKind{name: "rereview", from: ticket.StatusInReview}
	case strings.HasPrefix(first, "Kickback: QA fail at "):
		k = noteKind{name: "qa-fail", from: ticket.StatusInReview, to: ticket.StatusInProgress, release: true, note: "QA fail at"}
	case strings.HasPrefix(first, "Escalation: review at "):
		k = noteKind{name: "review-escalation", from: ticket.StatusInReview, to: ticket.StatusBlocked, release: true, note: "Review escalated at"}
	case strings.HasPrefix(first, "Escalation: QA at "):
		k = noteKind{name: "qa-escalation", from: ticket.StatusInReview, to: ticket.StatusBlocked, release: true, note: "QA escalated at"}
	case strings.HasPrefix(first, "Kickback:"):
		m := kickbackHead.FindStringSubmatch(first)
		if m == nil {
			return bad("a review's kickback opens %q, with the head's full sha or none; majors and minors go under ## Follow-ups in the report, not here", "Kickback: <n> blockers at <sha>")
		}
		n, _ := strconv.Atoi(m[1])
		var found, slid []string
		for _, l := range strings.Split(lineBreaks.Replace(body), "\n") {
			if f := findingLine.FindStringSubmatch(l); f != nil {
				found = append(found, f[1])
				if f[2] != "blocker" {
					slid = append(slid, f[1]+" ("+f[2]+")")
				}
			}
		}
		switch {
		case len(slid) > 0:
			return bad("only a blocker sends a change back: take %s off the Kickback and list them under ## Follow-ups in the report", strings.Join(slid, ", "))
		case n == 0 || len(found) == 0:
			return bad("a Kickback with no blocker is a GO: post %q instead", "Review: GO at <sha>")
		case n != len(found):
			return bad("the first line counts %d blockers, and %d finding lines follow (%s)", n, len(found), strings.Join(found, ", "))
		}
		// "at none" is a PR that isn't there: nothing to name in the note.
		k = noteKind{name: "review-kickback", from: ticket.StatusInReview, to: ticket.StatusInProgress, release: true, note: "Kickback at"}
		if m[2] == "none" {
			return k, nil
		}
	default:
		return bad("the note opens %q; want Review: GO at, Kickback:, Escalation: review at, QA: pass at, Kickback: QA fail at, Escalation: QA at or Rereview: QA at", first)
	}
	if !atSHA.MatchString(first) {
		return bad("the first line names no full head sha after \" at \": a verdict covers one head")
	}
	if k.to == ticket.StatusBlocked && !lastLineHas(body, "Return-role:") {
		return bad("an Escalation: ends with a line %q, so the ticket goes back to its role once answered", "Return-role: <the ticket's role>")
	}
	return k, nil
}

// lastLineHas reports whether the note's last line with text starts with p.
func lastLineHas(body, p string) bool {
	lines := strings.Split(strings.TrimSpace(lineBreaks.Replace(body)), "\n")
	return strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), p)
}

func runVerdict(ctx context.Context, c *call) error {
	return c.postNote(ctx, "verdict", verdictKind)
}

// runHandoff posts an engineer's Handoff: and sends the ticket to review.
func runHandoff(ctx context.Context, c *call) error {
	return c.postNote(ctx, "handoff", func(body string) (noteKind, error) {
		if !strings.HasPrefix(firstLine(body), "Handoff:") {
			return noteKind{}, fmt.Errorf("%w: handoff: the note opens %q; want Handoff:", ticket.ErrInvalid, firstLine(body))
		}
		if !hexSHA.MatchString(body) {
			return noteKind{}, fmt.Errorf("%w: handoff: the note names no full head sha: review and QA rule on one head", ticket.ErrInvalid)
		}
		return noteKind{name: "handoff", from: ticket.StatusInProgress, to: ticket.StatusInReview, release: true, note: "Handoff at"}, nil
	})
}

type posted struct {
	Kind    string         `json:"kind"`
	Comment ticket.Comment `json:"comment"`
	Moved   string         `json:"moved,omitempty"`
	Ticket  any            `json:"ticket"`
}

// postNote posts the note, then makes its kind's move under the same
// claim: comment first, so the next run reads the note as the latest. The
// key makes a retry a replay: the comment under it, the move under
// <key>-move.
func (c *call) postNote(ctx context.Context, cmd string, kindOf func(string) (noteKind, error)) error {
	body := c.fs.String("body", "", "the note")
	bodyFile := c.fs.String("body-file", "", "read the note from a file, or - for stdin")
	holder := c.fs.String("holder", "", "your <bot-id>/<session-id>, from your claim")
	token := c.fs.Int64("token", 0, "lease token from your claim")
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		if *holder == "" || *token == 0 {
			return usagef("%s: --holder and --token are required: claim the ticket first", cmd)
		}
		if c.idemKey == "" {
			return usagef("%s: --idempotency-key is required, so a retry replays instead of posting twice", cmd)
		}
		b, err := c.readBody(*body, *bodyFile)
		if err != nil {
			return err
		}
		k, err := kindOf(b)
		if err != nil {
			return err
		}
		d, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		// A replay finds the ticket where the first try left it, with this
		// note as its latest.
		replay := k.to != "" && d.Status == k.to && len(d.Comments) > 0 && d.Comments[len(d.Comments)-1].Body == b
		if d.Status != k.from && !replay {
			return fmt.Errorf("%w: %s is %s, not %s: it moved on without this run, so post nothing", ticket.ErrStatusConflict, id, d.Status, k.from)
		}
		cm, err := s.Comment(ctx, ticket.CommentRequest{
			ID: id, Body: b, Holder: *holder, Token: *token, Actor: c.actor, IdempotencyKey: c.idemKey,
		})
		if err != nil {
			return err
		}
		out := posted{Kind: k.name, Comment: cm, Ticket: withoutBody(d.Ticket)}
		if k.to != "" {
			note := k.note
			if sha := shaIn(b); sha != "" {
				note += " " + sha
			}
			t, err := s.Transition(ctx, ticket.TransitionRequest{
				ID: id, From: k.from, To: k.to, Holder: *holder, Token: *token, Note: note, Release: k.release,
				Actor: c.actor, IdempotencyKey: c.idemKey + "-move",
			})
			if err != nil {
				return fmt.Errorf("the note is posted (comment %d) but the move to %s failed; run the same command again: %w", cm.ID, k.to, err)
			}
			out.Moved, out.Ticket = string(k.from)+" -> "+string(k.to), withoutBody(t)
		}
		return c.out(out, func(w io.Writer) {
			fmt.Fprintf(w, "posted %s on %s (comment %d)", k.name, id, cm.ID)
			if out.Moved != "" {
				fmt.Fprintf(w, "; moved %s", out.Moved)
				if k.release {
					fmt.Fprint(w, ", claim released")
				}
			}
			fmt.Fprintln(w)
		})
	})
}
