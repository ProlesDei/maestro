package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// claudeReply is the part of a claude -p --output-format json reply that
// log-run reads. On a resumed session the totals are the session's so far.
type claudeReply struct {
	Type         string   `json:"type"`
	SessionID    string   `json:"session_id"`
	TotalCostUSD *float64 `json:"total_cost_usd"`
	// ModelUsage holds every model the session called, its subagents' too.
	ModelUsage map[string]struct {
		InputTokens              int64 `json:"inputTokens"`
		OutputTokens             int64 `json:"outputTokens"`
		CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
	} `json:"modelUsage"`
}

// lastResult finds the last result message in a reply: the one value
// --output-format json prints, the last line of stream-json, or the last
// message of the array json --verbose prints.
func lastResult(b []byte) (claudeReply, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return claudeReply{}, fmt.Errorf("%w: no reply on stdin: the run gave no result", ticket.ErrInvalid)
	}
	bad := func(err error) error {
		return fmt.Errorf("%w: the reply isn't claude -p --output-format json: %v", ticket.ErrInvalid, err)
	}
	var msgs []claudeReply
	if b[0] == '[' {
		if err := json.Unmarshal(b, &msgs); err != nil {
			return claudeReply{}, bad(err)
		}
	} else {
		for dec := json.NewDecoder(bytes.NewReader(b)); ; {
			var r claudeReply
			err := dec.Decode(&r)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return claudeReply{}, bad(err)
			}
			msgs = append(msgs, r)
		}
	}
	var last *claudeReply
	for i := range msgs {
		if msgs[i].Type == "result" {
			last = &msgs[i]
		}
	}
	switch {
	case last == nil:
		return claudeReply{}, fmt.Errorf("%w: the reply holds no result message: want claude -p --output-format json", ticket.ErrInvalid)
	case last.SessionID == "" || last.TotalCostUSD == nil:
		return claudeReply{}, fmt.Errorf("%w: the reply's result has no session_id or total_cost_usd", ticket.ErrInvalid)
	}
	return *last, nil
}

func runLogRun(ctx context.Context, c *call) error {
	tk := c.fs.String("ticket", "", "the ticket the run worked on (default none)")
	// Flags are read first, to record the run's start, but their errors
	// wait: the reply goes through first, whatever fails, since the bot
	// reads it here and a run must never lose its reply to a logging error.
	err := c.parse()
	asked := c.jsonOut
	c.jsonOut = false // stdout carries the reply, so errors go to stderr
	if err == nil && !asked {
		err = c.noArgs()
	}
	var id ticket.ID
	if err == nil && !asked && *tk != "" {
		id, err = ticket.ParseID(*tk)
	}
	var launch int64
	if err == nil && !asked {
		launch = c.startLaunch(ctx, id)
	}
	var buf bytes.Buffer
	_, copyErr := io.Copy(c.env.Stdout, io.TeeReader(c.env.Stdin, &buf))
	// The run is over once its reply is in: a SIGTERM now must not lose
	// its record.
	ctx = context.WithoutCancel(ctx)
	switch {
	case err != nil:
		return err
	case asked:
		return usagef("log-run takes no --json: stdout carries the reply as it came")
	case copyErr != nil:
		c.endLaunch(ctx, nil, launch, ticket.LaunchNoResult, "")
		return fmt.Errorf("pass the reply through: %w", copyErr)
	}
	r, err := lastResult(buf.Bytes())
	if err != nil {
		c.endLaunch(ctx, nil, launch, ticket.LaunchNoResult, "")
		c.freeClaim(ctx, launch, id)
		return err
	}
	req := ticket.LogRunRequest{Session: r.SessionID, Ticket: id, Actor: logActor(c.actor), CostUSD: *r.TotalCostUSD}
	for _, u := range r.ModelUsage {
		req.InputTokens += u.InputTokens
		req.OutputTokens += u.OutputTokens
		req.CacheWriteTokens += u.CacheCreationInputTokens
		req.CacheReadTokens += u.CacheReadInputTokens
	}
	s, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	c.endLaunch(ctx, s, launch, ticket.LaunchResult, r.SessionID)
	_, err = s.LogRun(ctx, req)
	// A wrong id must not lose the cost: log it with no ticket.
	if errors.Is(err, ticket.ErrNotFound) && req.Ticket != 0 {
		fmt.Fprintf(c.env.Stderr, "maestro-ticket: no ticket %s; logged the run with no ticket\n", req.Ticket)
		req.Ticket = 0
		_, err = s.LogRun(ctx, req)
	}
	return err
}

// costRow is one group of runs in a costs report.
type costRow struct {
	Key              string  `json:"key"`
	Runs             int     `json:"runs"`
	USD              float64 `json:"usd"`
	Percent          float64 `json:"percent"`
	RunMedian        float64 `json:"run_median"`
	Tickets          int     `json:"tickets"`
	TicketMedian     float64 `json:"ticket_median"`
	TicketP90        float64 `json:"ticket_p90"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
}

// costKeys name each run's group. A grouping that reads the ticket puts a
// run with no ticket in "(no ticket)".
var costKeys = map[string]func(r ticket.RunRow) string{
	"actor":  func(r ticket.RunRow) string { return r.Actor },
	"ticket": byTicket(func(r ticket.RunRow) string { return r.Ticket.String() }),
	"goal": byTicket(func(r ticket.RunRow) string {
		if r.Goal == "" {
			return "(no goal)"
		}
		return r.Goal
	}),
	"origin": byTicket(func(r ticket.RunRow) string { return r.Origin }),
	"tries":  byTicket(func(r ticket.RunRow) string { return strconv.Itoa(r.Tries) }),
}

func byTicket(key func(ticket.RunRow) string) func(ticket.RunRow) string {
	return func(r ticket.RunRow) string {
		if r.Ticket == 0 {
			return "(no ticket)"
		}
		return key(r)
	}
}

func runCosts(ctx context.Context, c *call) error {
	by := c.fs.String("by", "actor", "group by actor, ticket, goal, origin or tries")
	tk := c.fs.String("ticket", "", "only this ticket's runs")
	since := c.fs.String("since", "", "only sessions first logged on or after this date (2006-01-02), or within this long (such as 72h)")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		key := costKeys[*by]
		if key == nil {
			return usagef("costs: --by wants actor, ticket, goal, origin or tries, not %q", *by)
		}
		var f ticket.RunFilter
		var err error
		if *tk != "" {
			if f.Ticket, err = ticket.ParseID(*tk); err != nil {
				return err
			}
		}
		if f.Since, err = c.parseSince(*since); err != nil {
			return err
		}
		runs, err := s.Runs(ctx, f)
		if err != nil {
			return err
		}
		rows, total := costRows(runs, key)
		return c.out(map[string]any{"by": *by, "rows": rows, "total": total}, func(w io.Writer) { printCosts(w, *by, rows, total) })
	})
}

func (c *call) parseSince(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return time.Time{}, usagef("--since wants a date such as 2026-11-12 or a length of time such as 72h, not %q", s)
	}
	now := time.Now()
	if c.env.Now != nil {
		now = c.env.Now()
	}
	return now.Add(-d), nil
}

// costRows groups runs by key, most spent first, and totals them all. A
// run with no ticket counts in its group's runs and dollars, not its
// tickets.
func costRows(runs []ticket.RunRow, key func(ticket.RunRow) string) ([]costRow, costRow) {
	type group struct {
		row     costRow
		runs    []float64
		tickets map[ticket.ID]float64
	}
	groups := map[string]*group{}
	all := &group{row: costRow{Key: "all"}, tickets: map[ticket.ID]float64{}}
	add := func(g *group, r ticket.RunRow) {
		g.row.Runs++
		g.row.USD += r.CostUSD
		g.row.InputTokens += r.InputTokens
		g.row.OutputTokens += r.OutputTokens
		g.row.CacheWriteTokens += r.CacheWriteTokens
		g.row.CacheReadTokens += r.CacheReadTokens
		g.runs = append(g.runs, r.CostUSD)
		if r.Ticket != 0 {
			g.tickets[r.Ticket] += r.CostUSD
		}
	}
	for _, r := range runs {
		k := key(r)
		g := groups[k]
		if g == nil {
			g = &group{row: costRow{Key: k}, tickets: map[ticket.ID]float64{}}
			groups[k] = g
		}
		add(g, r)
		add(all, r)
	}
	finish := func(g *group) costRow {
		if all.row.USD > 0 {
			g.row.Percent = g.row.USD * 100 / all.row.USD
		}
		g.row.RunMedian = quantile(g.runs, 0.5)
		per := make([]float64, 0, len(g.tickets))
		for _, v := range g.tickets {
			per = append(per, v)
		}
		g.row.Tickets = len(per)
		g.row.TicketMedian = quantile(per, 0.5)
		g.row.TicketP90 = quantile(per, 0.9)
		return g.row
	}
	rows := []costRow{}
	for _, g := range groups {
		rows = append(rows, finish(g))
	}
	slices.SortFunc(rows, func(a, b costRow) int {
		if a.USD != b.USD {
			if a.USD > b.USD {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Key, b.Key)
	})
	return rows, finish(all)
}

// quantile returns the median for q 0.5 (the mean of the middle two of an
// even count), else the nearest-rank value; 0 for no values.
func quantile(v []float64, q float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	n := len(s)
	if q == 0.5 {
		if n%2 == 1 {
			return s[n/2]
		}
		return (s[n/2-1] + s[n/2]) / 2
	}
	i := int(math.Ceil(q*float64(n))) - 1
	return s[max(0, min(i, n-1))]
}

func printCosts(w io.Writer, by string, rows []costRow, total costRow) {
	if total.Runs == 0 {
		fmt.Fprintln(w, "no runs logged")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "%s\tRUNS\tUSD\tSHARE\tMEDIAN/RUN\tTICKETS\tMEDIAN/TICKET\tP90/TICKET\n", strings.ToUpper(by))
	for _, r := range append(rows, total) {
		fmt.Fprintf(tw, "%s\t%d\t%.2f\t%.0f%%\t%.2f\t%d\t%.2f\t%.2f\n",
			r.Key, r.Runs, r.USD, r.Percent, r.RunMedian, r.Tickets, r.TicketMedian, r.TicketP90)
	}
	tw.Flush()
	fmt.Fprintf(w, "tokens: input %d, output %d, cache write %d, cache read %d\n",
		total.InputTokens, total.OutputTokens, total.CacheWriteTokens, total.CacheReadTokens)
}
