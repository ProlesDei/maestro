package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func (c *call) now() time.Time {
	if c.env.Now != nil {
		return c.env.Now()
	}
	return time.Now()
}

// startLaunch records a run's start for log-run and returns its id, or 0
// when it can't: the run's reply matters more than its record, so a
// failure here only warns.
func (c *call) startLaunch(ctx context.Context, id ticket.ID) int64 {
	s, err := c.open(ctx)
	if err != nil {
		fmt.Fprintf(c.env.Stderr, "maestro-ticket log-run: can't record the run's start: %v\n", err)
		return 0
	}
	defer s.Close()
	req := ticket.StartLaunchRequest{Actor: logActor(c.actor), Ticket: id, PID: os.Getpid(), Stamp: procStamp(os.Getpid())}
	l, err := s.StartLaunch(ctx, req)
	if errors.Is(err, ticket.ErrNotFound) && req.Ticket != 0 {
		req.Ticket = 0 // the end says so on stderr, once
		l, err = s.StartLaunch(ctx, req)
	}
	if err != nil {
		fmt.Fprintf(c.env.Stderr, "maestro-ticket log-run: can't record the run's start: %v\n", err)
		return 0
	}
	return l.ID
}

// endLaunch records how the run ended, best effort, on s or on a store it
// opens.
func (c *call) endLaunch(ctx context.Context, s ticket.Store, id int64, outcome, session string) {
	if id == 0 {
		return
	}
	// A cancelled context (SIGTERM) must not lose the end: the run is over.
	ctx = context.WithoutCancel(ctx)
	if s == nil {
		st, err := c.open(ctx)
		if err != nil {
			fmt.Fprintf(c.env.Stderr, "maestro-ticket log-run: can't record the run's end: %v\n", err)
			return
		}
		defer st.Close()
		s = st
	}
	if _, err := s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: id, Outcome: outcome, Session: session}); err != nil {
		fmt.Fprintf(c.env.Stderr, "maestro-ticket log-run: can't record the run's end: %v\n", err)
	}
}

// freeClaim frees what a run with no result held on its ticket, once its
// end is recorded, and says so on stderr. Best effort: the bot learns the
// run gave no result from the exit code either way.
func (c *call) freeClaim(ctx context.Context, launch int64, id ticket.ID) {
	if launch == 0 || id == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	s, err := c.open(ctx)
	if err == nil {
		defer s.Close()
		var freed string
		if freed, err = freeRunClaim(ctx, s, logActor(c.actor), id, logActor(c.actor)); freed != "" {
			fmt.Fprintf(c.env.Stderr, "maestro-ticket log-run: freed %s's claim on %s, counted as a lease expiry\n", freed, id)
		}
	}
	if err != nil {
		fmt.Fprintf(c.env.Stderr, "maestro-ticket log-run: can't free the run's claim on %s: %v\n", id, err)
	}
}

func logActor(a string) string {
	if a = strings.TrimSpace(a); a != "" {
		return a
	}
	return "unknown"
}

// launchGone reports whether l's log-run process is gone, so its run ended
// without anyone seeing it end. A stamp from another boot is gone. One from
// another pid namespace, such as a container, names a pid this process
// can't see, so it counts as still going.
func launchGone(l ticket.Launch) bool {
	self := procStamp(os.Getpid())
	if l.Stamp == "" || self == "" {
		return !exists(l.PID)
	}
	was, cur := strings.SplitN(l.Stamp, "|", 3), strings.SplitN(self, "|", 3)
	switch {
	case len(was) != 3 || was[0] != cur[0]:
		return true
	case was[1] != cur[1]:
		return false
	}
	return procStamp(l.PID) != l.Stamp || zombie(l.PID)
}

// fromAnotherBoot reports whether stamp was taken before this machine
// last started. False when either stamp is unknown.
func fromAnotherBoot(stamp string) bool {
	self := procStamp(os.Getpid())
	if stamp == "" || self == "" {
		return false
	}
	return strings.SplitN(stamp, "|", 2)[0] != strings.SplitN(self, "|", 2)[0]
}

type diedRun struct {
	ticket.Launch
	// Freed is the claim alerts released for the run, if any.
	Freed string `json:"freed,omitempty"`
}

type expiredLease struct {
	Ticket ticket.ID `json:"ticket"`
	Holder string    `json:"holder"`
}

type stalledTicket struct {
	Ticket ticket.ID     `json:"ticket"`
	Status ticket.Status `json:"status"`
	Since  time.Time     `json:"since"`
	Idle   string        `json:"idle"`
}

type revivedClaim struct {
	Ticket   ticket.ID `json:"ticket"`
	Holder   string    `json:"holder"`
	Revivals int       `json:"revivals"`
}

type etaState struct {
	ticket.ETA
	State string `json:"state"` // met, open or missed
	// Open lists what the ETA promised that isn't done yet.
	Open []ticket.Ticket `json:"-"`
	IDs  []ticket.ID     `json:"open"`
}

type alertReport struct {
	Restarted bool            `json:"restarted"`
	Died      []diedRun       `json:"died"`
	Expired   []expiredLease  `json:"expired"`
	ETAMissed []etaState      `json:"eta_missed"`
	Revived   []revivedClaim  `json:"revived"`
	Stalled   []stalledTicket `json:"stalled"`
	// Errors lists steps that failed; the rest of the report still holds.
	Errors []string `json:"errors"`
}

func (r alertReport) empty() bool {
	return len(r.Died)+len(r.Expired)+len(r.ETAMissed)+len(r.Revived)+len(r.Stalled) == 0
}

// runAlerts is the planner's first step each look. It frees what dead runs
// hold, then lists what needs a person or a bot now: runs that died with
// no result, leases that ran out, ETAs past due, leases that ran out and
// came back, and handoffs no one picked up.
func runAlerts(ctx context.Context, c *call) error {
	stall := c.fs.Duration("stall", ticket.DefaultStall, "an in_review ticket with no one on it this long is stalled")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		if *stall <= 0 {
			return usagef("alerts: --stall must be more than 0")
		}
		rep := alertReport{Died: []diedRun{}, Expired: []expiredLease{}, ETAMissed: []etaState{}, Revived: []revivedClaim{}, Stalled: []stalledTicket{}, Errors: []string{}}
		// Each step goes on past another's error: what an earlier step
		// freed or ended is printed once, here, and must not be lost.
		var errs []error
		keep := func(err error) {
			if err != nil {
				errs = append(errs, err)
				rep.Errors = append(rep.Errors, err.Error())
			}
		}
		keep(reapReport(ctx, s, &rep))
		keep(diedReport(ctx, s, c.actor, &rep))
		now := c.now()
		keep(etaReport(ctx, s, now, &rep))
		yes, no := true, false
		held, err := s.List(ctx, ticket.ListFilter{Claimed: &yes})
		keep(err)
		for _, t := range held {
			if cl := t.LiveClaim(); cl != nil && cl.Revivals > 0 {
				rep.Revived = append(rep.Revived, revivedClaim{t.ID, cl.Holder, cl.Revivals})
			}
		}
		idle, err := s.List(ctx, ticket.ListFilter{Statuses: []ticket.Status{ticket.StatusInReview}, Claimed: &no})
		keep(err)
		for _, t := range idle {
			if d := now.Sub(t.UpdatedAt); d >= *stall {
				rep.Stalled = append(rep.Stalled, stalledTicket{t.ID, t.Status, t.UpdatedAt, roundMin(d)})
			}
		}
		if err := c.out(rep, func(w io.Writer) { printAlerts(w, rep, now) }); err != nil {
			return err
		}
		switch {
		case len(errs) == 0:
			return nil
		case c.jsonOut:
			return silentExit{ticket.ExitInternal} // the errors are in the report
		}
		return errors.Join(errs...)
	})
}

func etaReport(ctx context.Context, s ticket.Store, now time.Time, rep *alertReport) error {
	etas, err := s.ETAs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range etas {
		st, err := etaCheck(ctx, s, e, now)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		switch st.State {
		case "missed":
			rep.ETAMissed = append(rep.ETAMissed, st)
		case "met":
			// Once met, an ETA stays met: a ticket tagged into its goal
			// later is new work, not a slip.
			if e.MetAt.IsZero() {
				if err := s.MarkETAMet(ctx, e.Goal, e.Due, e.SetAt); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}

// reapReport frees every expired lease, as reap does, and lists whose,
// from the lease_expired events the reap logged.
func reapReport(ctx context.Context, s ticket.Store, rep *alertReport) error {
	before, err := s.LastSeq(ctx)
	if err != nil {
		return err
	}
	res, err := s.Reap(ctx)
	if err != nil || len(res.Reaped) == 0 {
		return err
	}
	// What the reap freed is printed even if the holders can't be read.
	evs, evErr := s.Events(ctx, ticket.EventFilter{Since: before, Type: ticket.EventLeaseExpired})
	holder := map[ticket.ID]string{}
	for _, e := range evs {
		var d struct{ Holder string }
		if json.Unmarshal(e.Data, &d) == nil {
			holder[e.TicketID] = d.Holder
		}
	}
	for _, id := range res.Reaped {
		rep.Expired = append(rep.Expired, expiredLease{id, holder[id]})
	}
	return evErr
}

// diedReport ends each open run whose log-run is gone as died. After a
// restart it also frees the claim the run left on its ticket, as
// freeRunClaim says.
func diedReport(ctx context.Context, s ticket.Store, actor string, rep *alertReport) error {
	open, err := s.OpenLaunches(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, l := range open {
		if !launchGone(l) {
			continue
		}
		ended, err := s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: l.ID, Outcome: ticket.LaunchDied})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ended {
			continue // another look got there first
		}
		d := diedRun{Launch: l}
		d.Outcome = ticket.LaunchDied
		// Only a restart proves the run itself is gone: on the same boot
		// log-run may have died alone while claude runs on, so its lease
		// and keepalive decide.
		if fromAnotherBoot(l.Stamp) {
			rep.Restarted = true
			if d.Freed, err = freeRunClaim(ctx, s, l.Actor, l.Ticket, actor); err != nil {
				errs = append(errs, err)
			}
		}
		rep.Died = append(rep.Died, d)
	}
	return errors.Join(errs...)
}

// freeRunClaim frees the claim a run that ended with no result left on its
// ticket, as the team rules once had each bot do by hand: a live claim
// held by the run's bot, counted as a lease expiry. It leaves the claim
// while another run of that bot on that ticket still goes, and returns the
// holder it freed, or "".
func freeRunClaim(ctx context.Context, s ticket.Store, bot string, id ticket.ID, by string) (string, error) {
	if id == 0 {
		return "", nil
	}
	open, err := s.OpenLaunches(ctx)
	if err != nil {
		return "", err
	}
	for _, l := range open {
		if l.Actor == bot && l.Ticket == id && !launchGone(l) {
			return "", nil
		}
	}
	t, err := s.Get(ctx, id)
	if errors.Is(err, ticket.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	cl := t.LiveClaim()
	if cl == nil || ticket.HolderBot(cl.Holder) != bot {
		return "", nil
	}
	_, err = s.Release(ctx, ticket.ReleaseRequest{ID: id, Holder: cl.Holder, Token: cl.Token, Died: true, Actor: by})
	switch {
	case err == nil:
		return cl.Holder, nil
	case errors.Is(err, ticket.ErrLeaseLost), errors.Is(err, ticket.ErrClaimed), errors.Is(err, ticket.ErrVersionConflict):
		return "", nil // it moved on meanwhile
	}
	return "", err
}

// etaCheck says whether e was met. What it promised is the tickets it
// names, or else its goal's tickets that existed by the due time: work
// added later is a new promise, not a slip. A promised ticket split after
// the promise stands for the tickets its latest "Split into" names. A
// cancelled ticket with no split counts as not delivered when the ETA
// named it, and drops out of a goal, as the goal's percent drops it.
// Past due and not met is missed.
func etaCheck(ctx context.Context, s ticket.Store, e ticket.ETA, now time.Time) (etaState, error) {
	st := etaState{ETA: e, Open: []ticket.Ticket{}, IDs: []ticket.ID{}}
	if !e.MetAt.IsZero() {
		st.State = "met"
		return st, nil
	}
	named := len(e.Tickets) > 0
	var queue []ticket.Ticket
	if named {
		for _, id := range e.Tickets {
			d, err := s.Get(ctx, id)
			if err != nil {
				return st, err
			}
			queue = append(queue, d.Ticket)
		}
	} else {
		list, err := s.List(ctx, ticket.ListFilter{Goal: e.Goal})
		if err != nil {
			return st, err
		}
		for _, t := range list {
			if !t.CreatedAt.After(e.Due) {
				queue = append(queue, t)
			}
		}
	}
	seen := map[ticket.ID]bool{}
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		if seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		switch t.Status {
		case ticket.StatusDone:
			continue
		case ticket.StatusCancelled:
			kids, err := splitInto(ctx, s, t.ID)
			if err != nil {
				return st, err
			}
			if len(kids) > 0 {
				queue = append(queue, kids...)
				continue
			}
			if !named {
				continue
			}
		}
		st.Open = append(st.Open, t)
		st.IDs = append(st.IDs, t.ID)
	}
	switch {
	case len(st.Open) == 0:
		st.State = "met"
	case now.Before(e.Due):
		st.State = "open"
	default:
		st.State = "missed"
	}
	return st, nil
}

var ticketRef = regexp.MustCompile(`\bT-[0-9]+\b`)

// splitInto returns the tickets a cancelled ticket's latest "Split into"
// comment names, or none. A split makes new tickets, so only ids above the
// ticket's own count: no loop can read as delivered.
func splitInto(ctx context.Context, s ticket.Store, id ticket.ID) ([]ticket.Ticket, error) {
	d, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := len(d.Comments) - 1; i >= 0; i-- {
		body := d.Comments[i].Body
		if !strings.HasPrefix(body, "Split into") {
			continue
		}
		line, _, _ := strings.Cut(body, "\n")
		var out []ticket.Ticket
		for _, ref := range ticketRef.FindAllString(line, -1) {
			kid, err := ticket.ParseID(ref)
			if err != nil || kid <= id {
				continue
			}
			k, err := s.Get(ctx, kid)
			if errors.Is(err, ticket.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			out = append(out, k.Ticket)
		}
		return out, nil
	}
	return nil, nil
}

func roundMin(d time.Duration) string {
	s := d.Round(time.Minute).String()
	s = strings.TrimSuffix(s, "0s")
	if s == "" {
		return "0m"
	}
	return s
}

func printAlerts(w io.Writer, r alertReport, now time.Time) {
	if r.empty() && len(r.Errors) == 0 {
		fmt.Fprintln(w, "alerts: none")
		return
	}
	if r.Restarted {
		fmt.Fprintln(w, "restart: the box restarted since the runs below began")
	}
	for _, d := range r.Died {
		on := "no ticket"
		if d.Ticket != 0 {
			on = d.Ticket.String()
		}
		fmt.Fprintf(w, "died: run %d, %s on %s, started %s, never gave a result", d.ID, d.Actor, on, d.StartedAt.Local().Format("Jan 2 15:04"))
		if d.Freed != "" {
			fmt.Fprintf(w, "; freed %s", d.Freed)
		}
		fmt.Fprintln(w)
	}
	for _, e := range r.Expired {
		fmt.Fprintf(w, "expired: %s lease of %s ran out; freed\n", e.Ticket, dash(e.Holder))
	}
	for _, e := range r.ETAMissed {
		fmt.Fprintf(w, "eta missed: %s due %s, %s ago; open: %s; send the slip up and record a new eta\n", e.Goal, e.Due.Local().Format("Jan 2 15:04"),
			roundMin(now.Sub(e.Due)), openText(e.Open))
	}
	for _, v := range r.Revived {
		fmt.Fprintf(w, "revived: %s lease of %s ran out %dx and came back: its run may be hung; ask its bot if it still answers\n", v.Ticket, v.Holder, v.Revivals)
	}
	for _, st := range r.Stalled {
		fmt.Fprintf(w, "stalled: %s %s %s with no one on it\n", st.Ticket, st.Status, st.Idle)
	}
}

// openText names up to 5 open tickets with their status.
func openText(list []ticket.Ticket) string {
	var parts []string
	for i, t := range list {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("and %d more", len(list)-5))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s", t.ID, statusText(t)))
	}
	return strings.Join(parts, ", ")
}

func runETA(ctx context.Context, c *call) error {
	goal := c.fs.String("goal", "", "the goal the ETA is for")
	by := c.fs.String("by", "", "when it's due: 15:04 today, 2006-01-02 15:04, RFC 3339, or a length of time such as 90m")
	tickets := c.fs.String("tickets", "", "the tickets promised by then (default the whole goal)")
	clear := c.fs.Bool("clear", false, "drop the goal's ETA")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		g := strings.TrimSpace(*goal)
		switch {
		case *clear && (*by != "" || *tickets != ""):
			return usagef("eta: --clear takes only --goal")
		case *clear && g == "":
			return usagef("eta: --clear needs --goal")
		case *clear:
			if err := s.ClearETA(ctx, g); err != nil {
				return err
			}
			return c.out(map[string]any{"cleared": g}, func(w io.Writer) { fmt.Fprintf(w, "cleared the ETA of %s\n", g) })
		case g == "" && *by == "" && *tickets == "":
			return listETAs(ctx, c, s)
		case *by == "":
			return usagef("eta: setting an ETA needs --goal and --by")
		}
		now := c.now()
		due, err := parseDue(*by, now)
		if err != nil {
			return err
		}
		req := ticket.SetETARequest{Goal: g, Due: due, Actor: c.actor}
		if *tickets != "" {
			if req.Tickets, err = ticket.ParseIDs(*tickets); err != nil {
				return err
			}
		}
		e, err := s.SetETA(ctx, req)
		if err != nil {
			return err
		}
		return c.out(e, func(w io.Writer) {
			fmt.Fprintf(w, "eta: %s due %s (in %s)\n", e.Goal, e.Due.Local().Format("Jan 2 15:04"), roundMin(e.Due.Sub(now)))
		})
	})
}

func listETAs(ctx context.Context, c *call, s ticket.Store) error {
	now := c.now()
	etas, err := s.ETAs(ctx)
	if err != nil {
		return err
	}
	rows := []etaState{}
	for _, e := range etas {
		st, err := etaCheck(ctx, s, e, now)
		if err != nil {
			return err
		}
		rows = append(rows, st)
	}
	return c.out(map[string]any{"etas": rows}, func(w io.Writer) {
		if len(rows) == 0 {
			fmt.Fprintln(w, "no ETAs")
			return
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "GOAL\tDUE\tSTATE\tOPEN\tSET BY")
		for _, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", r.Goal, r.Due.Local().Format("Jan 2 15:04"), r.State, len(r.Open), r.SetBy)
		}
		tw.Flush()
	})
}

// parseDue reads --by against now, in local time. It refuses a time
// already past: an ETA is a promise about the future.
func parseDue(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	var due time.Time
	if d, err := time.ParseDuration(s); err == nil {
		due = now.Add(d)
	} else if t, err := time.Parse(time.RFC3339, s); err == nil {
		due = t
	} else if t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local); err == nil {
		due = t
	} else if t, err := time.ParseInLocation("15:04", s, time.Local); err == nil {
		l := now.In(time.Local)
		due = time.Date(l.Year(), l.Month(), l.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
	} else {
		return time.Time{}, usagef("eta: --by wants 15:04, 2006-01-02 15:04, an RFC 3339 time or a length of time such as 90m, not %q", s)
	}
	if !due.After(now) {
		return time.Time{}, usagef("eta: --by %s is already past; give a later time, with its date if it is tomorrow", s)
	}
	return due, nil
}
