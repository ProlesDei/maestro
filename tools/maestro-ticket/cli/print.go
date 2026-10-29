package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func oneLine(t ticket.Ticket) string {
	return fmt.Sprintf("%s [%s] P%d v%d %s%s", t.ID, t.Status, t.Priority, t.Version, t.Title, claimSuffix(t.Claim))
}

func claimSuffix(c *ticket.Claim) string {
	if c == nil {
		return ""
	}
	if c.Expired {
		return fmt.Sprintf(" (claim by %s expired)", c.Holder)
	}
	return fmt.Sprintf(" (claimed by %s until %s)", c.Holder, c.ExpiresAt.Format(time.RFC3339))
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
	fmt.Fprintf(w, "status:   %s\n", d.Status)
	fmt.Fprintf(w, "role:     %s\n", dash(d.Role))
	fmt.Fprintf(w, "priority: P%d\n", d.Priority)
	fmt.Fprintf(w, "version:  %d\n", d.Version)
	fmt.Fprintf(w, "claim:    %s%s\n", claimText(d.Claim), expiresText(d.Claim))
	fmt.Fprintf(w, "created:  %s by %s\n", d.CreatedAt.Format(time.RFC3339), d.CreatedBy)
	fmt.Fprintf(w, "updated:  %s\n", d.UpdatedAt.Format(time.RFC3339))
	if d.Body != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimRight(d.Body, "\n"))
	}
	for _, c := range d.Comments {
		fmt.Fprintf(w, "\n--- comment %d by %s at %s\n%s\n", c.ID, c.Author, c.CreatedAt.Format(time.RFC3339), strings.TrimRight(c.Body, "\n"))
	}
}

func expiresText(c *ticket.Claim) string {
	if c == nil || c.Expired {
		return ""
	}
	return " until " + c.ExpiresAt.Format(time.RFC3339)
}

func printList(w io.Writer, list []ticket.Ticket) {
	if len(list) == 0 {
		fmt.Fprintln(w, "no tickets")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tP\tROLE\tCLAIM\tV\tTITLE")
	for _, t := range list {
		fmt.Fprintf(tw, "%s\t%s\tP%d\t%s\t%s\t%d\t%s\n", t.ID, t.Status, t.Priority, dash(t.Role), claimText(t.Claim), t.Version, t.Title)
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
		if !c.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", mark, c.Name, c.Detail)
	}
	tw.Flush()
}
