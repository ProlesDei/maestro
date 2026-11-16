package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// The stages a ship ticket's time is counted in. A ticket in review
// waits on the reviewer until review's GO, and on QA from then on.
var flowStages = []string{"queue", "build", "review", "qa", "blocked"}

type flowStage struct {
	Stage   string  `json:"stage"`
	Tickets int     `json:"tickets"`
	Median  float64 `json:"median_s"`
	P90     float64 `json:"p90_s"`
	Total   float64 `json:"total_s"`
	Percent float64 `json:"percent"`
}

type flowTicket struct {
	ID        ticket.ID `json:"id"`
	Lead      float64   `json:"lead_s"`
	SendBacks int       `json:"send_backs"`
}

type flowReport struct {
	Since            string       `json:"since,omitempty"`
	Goal             string       `json:"goal,omitempty"`
	Tickets          int          `json:"tickets"`
	LeadMedian       float64      `json:"lead_median_s"`
	LeadP90          float64      `json:"lead_p90_s"`
	Stages           []flowStage  `json:"stages"`
	ReviewSendBacks  int          `json:"review_send_backs"`
	QASendBacks      int          `json:"qa_send_backs"`
	FirstPassPercent float64      `json:"first_pass_percent"`
	Slowest          []flowTicket `json:"slowest"`
}

// runFlow reports where the ship tickets that reached done spent their
// time, and how often review and QA sent them back, from the events and
// comments the store already keeps: the numbers that say which stage to
// speed up next, and whether a change to the flow helped.
func runFlow(ctx context.Context, c *call) error {
	since := c.fs.String("since", "", "only tickets done on or after this date (2006-01-02), or within this long (such as 72h)")
	goal := c.fs.String("goal", "", "only the tickets of this goal")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		from, err := c.parseSince(*since)
		if err != nil {
			return err
		}
		ex, err := s.Export(ctx)
		if err != nil {
			return err
		}
		rep := flowOf(ex, from, strings.TrimSpace(*goal))
		rep.Since = *since
		return c.out(rep, func(w io.Writer) { printFlow(w, rep) })
	})
}

// flowOf builds the report from one snapshot of the store.
func flowOf(ex ticket.Export, since time.Time, goal string) flowReport {
	events := map[ticket.ID][]ticket.Event{}
	for _, e := range ex.Events {
		if e.Type == ticket.EventCreated || e.Type == ticket.EventTransitioned {
			events[e.TicketID] = append(events[e.TicketID], e)
		}
	}
	rep := flowReport{Goal: goal, Stages: []flowStage{}, Slowest: []flowTicket{}}
	per := map[string][]float64{}
	var leads []float64
	firstPass := 0
	for _, d := range ex.Tickets {
		if d.Type != ticket.TypeShip || d.Status != ticket.StatusDone || (goal != "" && d.Goal != goal) {
			continue
		}
		spent, done, review, qa := ticketFlow(d, events[d.ID])
		if done.IsZero() || done.Before(since) {
			continue
		}
		rep.Tickets++
		lead := 0.0
		for st, sec := range spent {
			per[st] = append(per[st], sec)
			lead += sec
		}
		rep.ReviewSendBacks += review
		rep.QASendBacks += qa
		if review+qa == 0 {
			firstPass++
		}
		leads = append(leads, lead)
		rep.Slowest = append(rep.Slowest, flowTicket{ID: d.ID, Lead: lead, SendBacks: review + qa})
	}
	if rep.Tickets == 0 {
		return rep
	}
	rep.LeadMedian, rep.LeadP90 = quantile(leads, 0.5), quantile(leads, 0.9)
	rep.FirstPassPercent = 100 * float64(firstPass) / float64(rep.Tickets)
	all := 0.0
	for _, st := range flowStages {
		row := flowStage{Stage: st, Tickets: len(per[st]), Median: quantile(per[st], 0.5), P90: quantile(per[st], 0.9)}
		for _, v := range per[st] {
			row.Total += v
		}
		all += row.Total
		rep.Stages = append(rep.Stages, row)
	}
	for i := range rep.Stages {
		if all > 0 {
			rep.Stages[i].Percent = 100 * rep.Stages[i].Total / all
		}
	}
	slices.SortFunc(rep.Slowest, func(a, b flowTicket) int {
		if a.Lead != b.Lead {
			if a.Lead > b.Lead {
				return -1
			}
			return 1
		}
		return int(a.ID - b.ID)
	})
	rep.Slowest = rep.Slowest[:min(3, len(rep.Slowest))]
	return rep
}

// ticketFlow walks one ticket's status changes and returns the seconds it
// spent in each stage, when it last reached done, and how many times
// review and QA sent it back. Time in backlog counts nowhere, before the
// work starts or after: a parked ticket waits on a plan, not on the flow.
func ticketFlow(d ticket.Detail, evs []ticket.Event) (spent map[string]float64, done time.Time, review, qa int) {
	spent = map[string]float64{}
	status, at := ticket.Status(""), d.CreatedAt
	// Who held the ticket when it last left review: a stay that comes
	// back from blocked goes on with them, since no new GO is posted.
	held, fromBlocked := "review", false
	leave := func(to time.Time) {
		sec := to.Sub(at).Seconds()
		if sec <= 0 {
			return
		}
		switch status {
		case ticket.StatusTodo:
			spent["queue"] += sec
		case ticket.StatusInProgress:
			spent["build"] += sec
		case ticket.StatusBlocked:
			spent["blocked"] += sec
		case ticket.StatusInReview:
			// Within one stay, review's GO hands the ticket to QA, and QA's
			// Rereview: hands it back.
			stage, from := "review", at
			if fromBlocked {
				stage = held
			}
			for _, cm := range d.Comments {
				if cm.CreatedAt.Before(at) || !cm.CreatedAt.Before(to) {
					continue
				}
				next := ""
				switch l := firstLine(cm.Body); {
				case strings.HasPrefix(l, "Review: GO"):
					next = "qa"
				case strings.HasPrefix(l, "Rereview:"):
					next = "review"
				}
				if next != "" && next != stage {
					spent[stage] += cm.CreatedAt.Sub(from).Seconds()
					stage, from = next, cm.CreatedAt
				}
			}
			spent[stage] += to.Sub(from).Seconds()
			held = stage
		}
	}
	for _, e := range evs {
		var data struct{ Status, From, To ticket.Status }
		json.Unmarshal(e.Data, &data)
		switch e.Type {
		case ticket.EventCreated:
			status, at = data.Status, e.CreatedAt
		case ticket.EventTransitioned:
			if status == "" {
				status = data.From
			}
			leave(e.CreatedAt)
			if status == ticket.StatusInReview && data.To == ticket.StatusInProgress {
				// A send-back, counted once however often its note was
				// posted: by whoever posted the newest Kickback: of this
				// stay. A move back with none was no verdict.
				var by *int
				for _, cm := range d.Comments {
					if cm.CreatedAt.After(e.CreatedAt) {
						break
					}
					if cm.CreatedAt.Before(at) {
						continue
					}
					if l := firstLine(cm.Body); strings.HasPrefix(l, "Kickback: QA fail") {
						by = &qa
					} else if strings.HasPrefix(l, "Kickback:") {
						by = &review
					}
				}
				if by != nil {
					*by++
				}
			}
			fromBlocked = status == ticket.StatusBlocked
			status, at = data.To, e.CreatedAt
			if data.To == ticket.StatusDone {
				done = e.CreatedAt
			}
		}
	}
	return spent, done, review, qa
}

// span prints seconds as a duration to the minute, or to the second
// under a minute.
func span(sec float64) string {
	d := time.Duration(sec * float64(time.Second))
	if d < time.Minute {
		return short(d.Round(time.Second))
	}
	return short(d.Round(time.Minute))
}

func printFlow(w io.Writer, r flowReport) {
	scope := ""
	if r.Goal != "" {
		scope += " of goal " + r.Goal
	}
	if r.Since != "" {
		scope += " since " + r.Since
	}
	if r.Tickets == 0 {
		fmt.Fprintf(w, "flow: no ship ticket done%s\n", scope)
		return
	}
	fmt.Fprintf(w, "flow: %d ship tickets done%s; start to done: median %s, p90 %s\n", r.Tickets, scope, span(r.LeadMedian), span(r.LeadP90))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STAGE\tTICKETS\tMEDIAN\tP90\tTOTAL\tSHARE")
	for _, s := range r.Stages {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%.0f%%\n", s.Stage, s.Tickets, span(s.Median), span(s.P90), span(s.Total), s.Percent)
	}
	tw.Flush()
	fmt.Fprintf(w, "sent back: %d by review, %d by QA; %.0f%% of tickets passed both first time\n", r.ReviewSendBacks, r.QASendBacks, r.FirstPassPercent)
	var slow []string
	for _, t := range r.Slowest {
		slow = append(slow, fmt.Sprintf("%s %s (%d sent back)", t.ID, span(t.Lead), t.SendBacks))
	}
	fmt.Fprintf(w, "slowest: %s\n", strings.Join(slow, ", "))
}
