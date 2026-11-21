package cli

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
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
	// "- F3 [major, auto-fix] file:line: ...": a finding and its severity,
	// as a bullet or a numbered line, in any case, bold or not.
	findingLine = regexp.MustCompile(`(?i)^\s*(?:[-*+]|[0-9]+[.)])\s+[*_]*(F[0-9]+)[*_:]*\s*[\[(]\s*[*_]*([a-z]+)`)
	// A bullet that opens with a finding's id, whatever follows.
	findingID = regexp.MustCompile(`(?i)^\s*(?:[-*+]|[0-9]+[.)])\s+[*_]*(F[0-9]+)\b`)
	redGates  = regexp.MustCompile(`(?im)^\s*Gates:\s*(red|not run)`)
	fullSHA   = regexp.MustCompile(`^[0-9a-f]{40}(\W|$)`)
)

// fixedNotes are the verdict notes that open with fixed words, by those
// words. Any other Kickback: is a review's.
var fixedNotes = []struct {
	prefix string
	kind   noteKind
}{
	{"Review: GO at ", noteKind{name: "review-go", from: ticket.StatusInReview}},
	{"QA: pass at ", noteKind{name: "qa-pass", from: ticket.StatusInReview}},
	{"Rereview: QA at ", noteKind{name: "rereview", from: ticket.StatusInReview}},
	{"Kickback: QA fail at ", noteKind{name: "qa-fail", from: ticket.StatusInReview, to: ticket.StatusInProgress, release: true, note: "QA fail at"}},
	{"Escalation: review at ", noteKind{name: "review-escalation", from: ticket.StatusInReview, to: ticket.StatusBlocked, release: true, note: "Review escalated at"}},
	{"Escalation: QA at ", noteKind{name: "qa-escalation", from: ticket.StatusInReview, to: ticket.StatusBlocked, release: true, note: "QA escalated at"}},
}

// reviewKickback is a review's Kickback:, which sends the change back.
var reviewKickback = noteKind{name: "review-kickback", from: ticket.StatusInReview, to: ticket.StatusInProgress, release: true, note: "Kickback at"}

// noteOpening is the kind a note's first line opens, and the fixed words
// it opens with ("" for a review's Kickback:); ok is false for no kind.
// It reads the first line only: verdictKind checks the rest.
func noteOpening(first string) (k noteKind, prefix string, ok bool) {
	for _, f := range fixedNotes {
		if strings.HasPrefix(first, f.prefix) {
			return f.kind, f.prefix, true
		}
	}
	if strings.HasPrefix(first, "Kickback:") {
		return reviewKickback, "", true
	}
	return noteKind{}, "", false
}

// verdictKind sorts a review's or QA's note by its first line and checks
// the note says what its kind must. Only a blocker sends a change back
// (docs/decisions.md, 22), so a Kickback: that lists anything else is
// refused here, before the engineer pays a round for it.
func verdictKind(body string) (noteKind, error) {
	first := firstLine(body)
	bad := func(format string, a ...any) (noteKind, error) {
		return noteKind{}, fmt.Errorf("%w: verdict: %s", ticket.ErrInvalid, fmt.Sprintf(format, a...))
	}
	k, prefix, ok := noteOpening(first)
	switch {
	case !ok:
		return bad("the note opens %q; want Review: GO at, Kickback:, Escalation: review at, QA: pass at, Kickback: QA fail at, Escalation: QA at or Rereview: QA at", first)
	case k.name == reviewKickback.name:
		m := kickbackHead.FindStringSubmatch(first)
		if m == nil {
			return bad("a review's kickback opens %q, with the head's full sha or none; majors and minors go under ## Follow-ups in the report, not here", "Kickback: <n> blockers at <sha>")
		}
		n, _ := strconv.Atoi(m[1])
		var found, slid, unread []string
		for _, l := range strings.Split(lineBreaks.Replace(body), "\n") {
			if f := findingLine.FindStringSubmatch(l); f != nil {
				found = append(found, strings.ToUpper(f[1]))
				if sev := strings.ToLower(f[2]); sev != "blocker" {
					slid = append(slid, strings.ToUpper(f[1])+" ("+sev+")")
				}
			} else if f := findingID.FindStringSubmatch(l); f != nil {
				unread = append(unread, strings.ToUpper(f[1]))
			}
		}
		line := "- F1 [blocker, auto-fix] file:line: what's wrong. Fix: ..."
		switch {
		case len(unread) > 0:
			return bad("can't read the severity of %s: write each finding as %q", strings.Join(unread, ", "), line)
		case len(slid) > 0:
			return bad("only a blocker sends a change back: take %s off the Kickback and list them under ## Follow-ups in the report", strings.Join(slid, ", "))
		case n == 0 && len(found) == 0 && !redGates.MatchString(body):
			return bad("a Kickback with no blocker is a GO: post %q instead", "Review: GO at <sha>")
		case len(found) == 0:
			// Never a hint to post a GO here: the note says something stands.
			return bad("the first line counts %d blockers, and no finding line follows: give each reason for the NO-GO, a red gate or a conflict too, a line %q, and count them", n, line)
		case n != len(found):
			return bad("the first line counts %d blockers, and %d finding lines follow (%s)", n, len(found), strings.Join(found, ", "))
		}
		// "at none" is a PR that isn't there: nothing to name in the note.
		return k, nil
	}
	if !fullSHA.MatchString(first[len(prefix):]) {
		return bad("the first line wants the head's full sha right after %q: a verdict covers one head", strings.TrimSpace(prefix))
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
		if !hexSHA.MatchString(firstLine(body)) {
			return noteKind{}, fmt.Errorf("%w: handoff: the first line names no full head sha, as in %q: review and QA rule on one head", ticket.ErrInvalid, "Handoff: PR <url> at <sha>")
		}
		return noteKind{name: "handoff", from: ticket.StatusInProgress, to: ticket.StatusInReview, release: true, note: "Handoff at"}, nil
	})
}

// runQABasis reports which exact head has current authority to enter QA.
func runQABasis(ctx context.Context, c *call) error {
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		d, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		result := struct {
			Route     string `json:"route"`
			Head      string `json:"head,omitempty"`
			CommentID int64  `json:"comment_id,omitempty"`
			Passed    bool   `json:"passed,omitempty"`
		}{Route: "none"}
		note, head, qa := recoveryProof(d)
		if d.Status == ticket.StatusInReview && head != "" {
			result.Head, result.CommentID, result.Route = head, note.ID, "review"
			result.Passed = qa == head && (d.Criteria == nil || d.Criteria.QA == head)
			if strings.HasPrefix(note.Body, "Handoff:") {
				result.Route = "direct"
			}
		}
		return c.out(result, func(w io.Writer) { fmt.Fprintln(w, strings.TrimSpace(result.Route+" "+result.Head)) })
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
	ac := c.fs.Int("ac-revision", 0, "criteria revision used for this verdict")
	brief := c.fs.Int("brief-revision", 0, "accepted brief used for this verdict")
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
		// Routing reads a note's first line from its first byte.
		b = strings.TrimLeft(lineBreaks.Replace(b), " \t\n")
		k, err := kindOf(b)
		if err != nil {
			return err
		}
		d, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		// A replay finds this note on the ticket already, wherever the
		// ticket went since: the store answers it from the key.
		replay := slices.ContainsFunc(d.Comments, func(cm ticket.Comment) bool { return cm.Body == b })
		if d.Status != k.from && !replay {
			return fmt.Errorf("%w: %s is %s, and this note goes on a ticket in %s: post nothing", ticket.ErrStatusConflict, id, d.Status, k.from)
		}
		if d.DirectQA && d.Criteria == nil && k.name == "qa-pass" && !replay {
			_, head, _ := recoveryProof(d)
			if head != shaIn(b) || c.actor != ticket.RoleQALead || ticket.HolderBot(*holder) != c.actor {
				return fmt.Errorf("%w: QA needs its claim and the current handoff or review at this head", ticket.ErrForbidden)
			}
		}
		cm, err := c.postProof(ctx, s, d, k, ticket.CommentRequest{
			ID: id, Body: b, Holder: *holder, Token: *token, Actor: c.actor, IdempotencyKey: c.idemKey,
		}, *ac, *brief)
		if err != nil {
			return err
		}
		out := posted{Kind: k.name, Comment: cm, Ticket: withoutBody(d.Ticket)}
		if k.to != "" {
			note := k.note
			if sha := shaIn(b); sha != "" {
				note += " " + sha
			}
			directQA := false
			if k.name == "handoff" && d.DirectQA {
				current, err := s.Get(ctx, id)
				if err != nil {
					return err
				}
				directQA = directQAHandoff(current)
			}
			t, err := s.Transition(ctx, ticket.TransitionRequest{
				ID: id, DirectQAHandoff: directQA, From: k.from, To: k.to, Holder: *holder, Token: *token, Note: note, Release: k.release,
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
