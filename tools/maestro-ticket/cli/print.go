package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func oneLine(t ticket.Ticket) string {
	return fmt.Sprintf("%s [%s] P%d v%d %s%s", t.ID, statusText(t), t.Priority, t.Version, t.Title, claimSuffix(t.Claim))
}

// statusText adds a backlog ticket's reason, as backlog:hold, so no reader
// takes a ticket parked for a plan or a later slice for one held on purpose.
func statusText(t ticket.Ticket) string {
	if t.Status == ticket.StatusBacklog && t.Parked != "" {
		return string(t.Status) + ":" + string(t.Parked)
	}
	return string(t.Status)
}

func claimSuffix(c *ticket.Claim) string {
	if c == nil {
		return ""
	}
	if c.Expired {
		return fmt.Sprintf(" (claim by %s expired)", c.Holder)
	}
	return fmt.Sprintf(" (claimed by %s, token %d, until %s)", c.Holder, c.Token, c.ExpiresAt.Format(time.RFC3339))
}

func claimText(c *ticket.Claim) string {
	switch {
	case c == nil:
		return "-"
	case c.Expired:
		return c.Holder + " (expired)"
	default:
		return c.Holder
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func printDetail(w io.Writer, d ticket.Detail) {
	fmt.Fprintf(w, "%s  %s\n", d.ID, d.Title)
	fmt.Fprintf(w, "type:     %s\n", d.Type)
	fmt.Fprintf(w, "status:   %s%s\n", d.Status, blockedText(d.Ticket))
	if d.Parked != "" {
		fmt.Fprintf(w, "parked:   %s\n", d.Parked)
	}
	if d.Goal != "" {
		fmt.Fprintf(w, "goal:     %s\n", d.Goal)
	}
	fmt.Fprintf(w, "role:     %s\n", dash(d.Role))
	fmt.Fprintf(w, "owner:    %s\n", dash(d.Owner))
	fmt.Fprintf(w, "tries:    %d\n", d.Tries)
	if len(d.DependsOn) > 0 {
		ids := make([]string, len(d.DependsOn))
		for i, id := range d.DependsOn {
			ids[i] = id.String()
		}
		fmt.Fprintf(w, "deps:     %s\n", strings.Join(ids, " "))
	}
	if len(d.Areas) > 0 {
		fmt.Fprintf(w, "areas:    %s\n", strings.Join(d.Areas, " "))
	}
	fmt.Fprintf(w, "priority: P%d\n", d.Priority)
	fmt.Fprintf(w, "version:  %d\n", d.Version)
	fmt.Fprintf(w, "claim:    %s%s\n", claimText(d.Claim), expiresText(d.Claim))
	fmt.Fprintf(w, "created:  %s by %s\n", d.CreatedAt.Format(time.RFC3339), d.CreatedBy)
	fmt.Fprintf(w, "updated:  %s\n", d.UpdatedAt.Format(time.RFC3339))
	if d.Body != "" {
		fmt.Fprintf(w, "\n%s\n", indent(d.Body))
	}
	for _, c := range d.Comments {
		fmt.Fprintf(w, "\n--- comment %d by %s at %s\n%s\n", c.ID, c.Author, c.CreatedAt.Format(time.RFC3339), indent(c.Body))
	}
}

// lineBreaks are what a reader or a terminal may take for a new line.
var lineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\f", "\n", "\v", "\n", "\u0085", "\n", "\u2028", "\n", "\u2029", "\n")

// indent sets a ticket body or comment four spaces in, so a line at the
// left margin is always the tool's: text can't pass for a field or for a
// later comment's header. Every kind of line break starts an indented
// line, and any other control character but a tab, such as a backspace or
// an escape that moves the cursor, prints as U+FFFD. --json has the text
// as written.
func indent(body string) string {
	lines := strings.Split(strings.TrimRight(lineBreaks.Replace(body), "\n"), "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = "    " + strings.Map(func(r rune) rune {
			if r != '\t' && unicode.Is(unicode.Cc, r) {
				return unicode.ReplacementChar
			}
			return r
		}, l)
	}
	return strings.Join(lines, "\n")
}

func blockedText(t ticket.Ticket) string {
	if t.Status != ticket.StatusBlocked || t.BlockedFrom == "" {
		return ""
	}
	return " (from " + string(t.BlockedFrom) + ")"
}

func expiresText(c *ticket.Claim) string {
	if c == nil || c.Expired {
		return ""
	}
	return fmt.Sprintf(" token %d until %s", c.Token, c.ExpiresAt.Format(time.RFC3339))
}

func printList(w io.Writer, list []ticket.Ticket) {
	if len(list) == 0 {
		fmt.Fprintln(w, "no tickets")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTYPE\tSTATUS\tP\tROLE\tCLAIM\tTRIES\tV\tTITLE")
	for _, t := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\tP%d\t%s\t%s\t%d\t%d\t%s\n",
			t.ID, t.Type, statusText(t), t.Priority, dash(t.Role), claimText(t.Claim), t.Tries, t.Version, t.Title)
	}
	tw.Flush()
}

func printGoals(w io.Writer, rows []goalRow, goal string) {
	switch {
	case len(rows) == 0 && goal != "":
		fmt.Fprintf(w, "no ticket has goal %s\n", goal)
		return
	case len(rows) == 0:
		fmt.Fprintln(w, "no goals with open tickets")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "GOAL\tDONE\tREVIEW\tBUILD\tTODO\tBLOCKED\tPARKED\tTOTAL\tDONE%")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d%%\n",
			dash(r.Goal), r.Done, r.InReview, r.InProgress, r.Todo, r.Blocked, r.Backlog, r.Total, r.PercentDone)
	}
	tw.Flush()
}

func printEvents(w io.Writer, evs []ticket.Event, next int64) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SEQ\tTIME\tTICKET\tTYPE\tACTOR\tDATA")
	for _, e := range evs {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", e.Seq, e.CreatedAt.Format(time.RFC3339), e.TicketID, e.Type, e.Actor, e.Data)
	}
	tw.Flush()
	fmt.Fprintf(w, "next_seq: %d\n", next)
}

func printDoctor(w io.Writer, rep ticket.DoctorReport) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range rep.Checks {
		mark := "ok"
		switch {
		case !c.OK:
			mark = "FAIL"
		case c.Warn:
			mark = "warn"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", mark, c.Name, c.Detail)
	}
	tw.Flush()
}

func printReap(w io.Writer, r ticket.ReapResult) {
	if len(r.Reaped) == 0 {
		fmt.Fprintln(w, "reaped 0 expired leases")
		return
	}
	ids := make([]string, len(r.Reaped))
	for i, id := range r.Reaped {
		ids[i] = id.String()
	}
	fmt.Fprintf(w, "reaped %d expired leases: %s\n", len(r.Reaped), strings.Join(ids, " "))
}
