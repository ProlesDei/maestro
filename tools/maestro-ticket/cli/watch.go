package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	if s == nil {
		st, err := c.open(ctx)
		if err != nil {
			fmt.Fprintf(c.env.Stderr, "maestro-ticket log-run: can't record the run's end: %v\n", err)
			return
		}
		defer st.Close()
		s = st
	}
	// A cancelled context (SIGTERM) must not lose the end: the run is over.
	ctx = context.WithoutCancel(ctx)
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
		return !alive(l.PID)
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

func sameBootStamp(stamp string) bool {
	self := procStamp(os.Getpid())
	if stamp == "" || self == "" {
		return true
	}
	return strings.SplitN(stamp, "|", 2)[0] == strings.SplitN(self, "|", 2)[0]
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
		rep := alertReport{Died: []diedRun{}, Expired: []expiredLease{}, ETAMissed: []etaState{}, Revived: []revivedClaim{}, Stalled: []stalledTicket{}}
		if err := reapReport(ctx, s, &rep); err != nil {
			return err
		}
		if err := diedReport(ctx, s, c.actor, &rep); err != nil {
			return err
		}
		now := c.now()
		etas, err := s.ETAs(ctx)
		if err != nil {
			return err
		}
		for _, e := range etas {
			st, err := etaCheck(ctx, s, e, now)
			if err != nil {
				return err
			}
			if st.State == "missed" {
				rep.ETAMissed = append(rep.ETAMissed, st)
			}
		}
		yes, no := true, false
		held, err := s.List(ctx, ticket.ListFilter{Claimed: &yes})
		if err != nil {
			return err
		}
		for _, t := range held {
			if cl := t.LiveClaim(); cl != nil && cl.Revivals > 0 {
				rep.Revived = append(rep.Revived, revivedClaim{t.ID, cl.Holder, cl.Revivals})
			}
		}
		idle, err := s.List(ctx, ticket.ListFilter{Statuses: []ticket.Status{ticket.StatusInReview}, Claimed: &no})
		if err != nil {
			return err
		}
		for _, t := range idle {
			if d := now.Sub(t.UpdatedAt); d >= *stall {
				rep.Stalled = append(rep.Stalled, stalledTicket{t.ID, t.Status, t.UpdatedAt, roundMin(d)})
			}
		}
		return c.out(rep, func(w io.Writer) { printAlerts(w, rep, now) })
	})
}

// reapReport frees every expired lease, as reap does, and lists whose.
func reapReport(ctx context.Context, s ticket.Store, rep *alertReport) error {
	all, err := s.List(ctx, ticket.ListFilter{})
	if err != nil {
		return err
	}
	holder := map[ticket.ID]string{}
	for _, t := range all {
		if t.Claim != nil && t.Claim.Expired {
			holder[t.ID] = t.Claim.Holder
		}
	}
	res, err := s.Reap(ctx)
	if err != nil {
		return err
	}
	for _, id := range res.Reaped {
		rep.Expired = append(rep.Expired, expiredLease{id, holder[id]})
	}
	return nil
}

// diedReport ends each open run whose log-run is gone as died, and frees
// the claim it left on its ticket, as freeRunClaim says.
func diedReport(ctx context.Context, s ticket.Store, actor string, rep *alertReport) error {
	open, err := s.OpenLaunches(ctx)
	if err != nil {
		return err
	}
	for _, l := range open {
		if !launchGone(l) {
			continue
		}
		ended, err := s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: l.ID, Outcome: ticket.LaunchDied})
		if err != nil {
			return err
		}
		if !ended {
			continue // another look got there first
		}
		d := diedRun{Launch: l}
		d.Outcome = ticket.LaunchDied
		if !sameBootStamp(l.Stamp) {
			rep.Restarted = true
		}
		if d.Freed, err = freeRunClaim(ctx, s, l.Actor, l.Ticket, actor); err != nil {
			return err
		}
		rep.Died = append(rep.Died, d)
	}
	return nil
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

// etaCheck says whether e was met: every ticket it names done, or, naming
// none, no open ticket left in its goal. Past due and not met is missed.
func etaCheck(ctx context.Context, s ticket.Store, e ticket.ETA, now time.Time) (etaState, error) {
	st := etaState{ETA: e, Open: []ticket.Ticket{}, IDs: []ticket.ID{}}
	var list []ticket.Ticket
	if len(e.Tickets) > 0 {
		for _, id := range e.Tickets {
			d, err := s.Get(ctx, id)
			if err != nil {
				return st, err
			}
			list = append(list, d.Ticket)
		}
	} else {
		var err error
		if list, err = s.List(ctx, ticket.ListFilter{Goal: e.Goal}); err != nil {
			return st, err
		}
	}
	for _, t := range list {
		// A promised ticket that was cancelled wasn't delivered.
		if t.Status != ticket.StatusDone && (len(e.Tickets) > 0 || !t.Status.Terminal()) {
			st.Open = append(st.Open, t)
			st.IDs = append(st.IDs, t.ID)
		}
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

func roundMin(d time.Duration) string {
	s := d.Round(time.Minute).String()
	s = strings.TrimSuffix(s, "0s")
	if s == "" {
		return "0m"
	}
	return s
}

func printAlerts(w io.Writer, r alertReport, now time.Time) {
	if r.empty() {
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
		fmt.Fprintf(w, "expired: %s lease of %s ran out and is freed: its run hung or died\n", e.Ticket, dash(e.Holder))
	}
	for _, e := range r.ETAMissed {
		fmt.Fprintf(w, "eta missed: %s due %s, %s ago; open: %s\n", e.Goal, e.Due.Local().Format("Jan 2 15:04"),
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
