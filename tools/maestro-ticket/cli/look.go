package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// A handout is a message pickup or look printed for a bot to send. Each is
// kept a while, one file per ticket and bot under handouts/ next to the
// store, so the next look doesn't send the same job while the first is
// still on its way to a claim.
var (
	ticketInLine = regexp.MustCompile(`\bT-[0-9]+\b`)
	urgentText   = regexp.MustCompile(`(?i)\burgent\b`)
)

func handoutName(id, bot string) string { return id + "." + bot }

// recordHandouts keeps each send that names a ticket.
func (co coord) recordHandouts(sends []send) {
	for _, s := range sends {
		// A line to the planner is a report, not a job it was handed.
		id := ticketInLine.FindString(s.Line)
		if id == "" || !runs.BotPattern.MatchString(s.Bot) || s.Bot == "planner" {
			continue
		}
		co.s.write(handoutMarks, handoutName(id, s.Bot), s.Line+"\n")
	}
}

// dropHandout frees a ticket's handout to bot, so the next look or pickup
// may send it again.
func (co coord) dropHandout(id, bot string) { co.s.remove(handoutMarks, handoutName(id, bot)) }

// handoutBody is what a ticket's handout to bot holds, trimmed, or "".
func (co coord) handoutBody(id, bot string) string {
	m, _ := co.s.read(handoutMarks, handoutName(id, bot))
	return strings.TrimSpace(m.body)
}

// urgentSince is the newest note marked urgent after the latest Taken:,
// so a later finding in the area doesn't hide it.
func urgentSince(d ticket.Detail) (ticket.Comment, bool) {
	for i := len(d.Comments) - 1; i >= 0; i-- {
		b := d.Comments[i].Body
		if strings.HasPrefix(b, "Taken:") {
			return ticket.Comment{}, false
		}
		if urgentText.MatchString(b) {
			return d.Comments[i], true
		}
	}
	return ticket.Comment{}, false
}

// claimHandout takes a ticket's handout to bot for this look, unless one
// stands from within d: the mark is made exclusive, so of two looks at
// once only one takes it. A stale one is moved aside first, and of two
// looks that both find it stale, one still wins the create. body is what
// the mark holds.
func (co coord) claimHandout(id, bot, body string, d time.Duration, now time.Time) bool {
	took, _ := co.tryHandout(id, bot, body, d, now)
	return took
}

// tryHandout is claimHandout that also says when the store, not a mark,
// stopped the claim.
func (co coord) tryHandout(id, bot, body string, d time.Duration, now time.Time) (bool, error) {
	name := handoutName(id, bot)
	for range 2 {
		err := co.s.create(handoutMarks, name, body+"\n")
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return false, err
		}
		m, err := co.s.read(handoutMarks, name)
		if err == nil && now.Sub(m.at) < d {
			return false, nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
		if !co.moveStale(handoutMarks, name, m) {
			return false, nil
		}
	}
	return false, nil
}

// moveStale moves a stale mark aside under a takeover lock, and only if it
// is still the mark seen, so of takers that all found it stale one wins.
func (co coord) moveStale(space, name string, seen mark) bool {
	took, _ := co.s.takeOver(space, name, func(now mark) bool { return sameMark(seen, now) })
	return took
}

// judgeWindow is how long a judged row stays judged while the ticket
// doesn't change: a "leave it" writes nothing, and what lifts a hold
// often lies on another ticket.
const judgeWindow = 6 * time.Hour

// judged reports whether the judge took this row, in this state, lately.
func (co coord) judged(id, fp string, now time.Time) bool {
	m, err := co.s.read(handoutMarks, handoutName(id, "judge"))
	return err == nil && strings.TrimSpace(m.body) == fp && now.Sub(m.at) < judgeWindow
}

// pruneHandouts drops records over a day old.
func (co coord) pruneHandouts(now time.Time) {
	ms, err := co.s.list(handoutMarks)
	if err != nil {
		return
	}
	for _, m := range ms {
		keep := 24 * time.Hour
		if strings.Contains(m.name, ".stale") || strings.HasSuffix(m.name, ".takeover") {
			keep = time.Hour
		}
		if strings.HasPrefix(m.name, "eta-") {
			keep = 30 * 24 * time.Hour // a missed date goes up once
		}
		if now.Sub(m.at) > keep {
			co.s.remove(handoutMarks, m.name)
		}
	}
}

// judgeRow is a ticket the planner's look can't settle by rule: the judge
// session reads it and decides.
type judgeRow struct {
	ID    string `json:"id"`
	Row   string `json:"row"`
	Facts string `json:"facts"`
	fp    string // the ticket's state as judged
	key   string // the handout it is judged under, if not the ticket's
}

func (j judgeRow) String() string { return "judge: " + j.ID + " " + j.Row + ": " + j.Facts }

type lookOut struct {
	Stopped bool       `json:"stopped"`
	Paused  string     `json:"paused,omitempty"`
	Alerts  []string   `json:"alerts"`
	Wrote   []string   `json:"wrote"`
	Started []string   `json:"started"`
	Send    []send     `json:"send"`
	Judge   []judgeRow `json:"judge"`
	Judged  string     `json:"judged,omitempty"`
	Report  []string   `json:"report,omitempty"`
	Counts  string     `json:"counts"`
	Errors  []string   `json:"errors"`
	// rest counts what a refill left for the planner's own look: a
	// missed date, a card or a send-back count it would send the chief of
	// staff.
	rest int
}

func (o lookOut) quiet() bool {
	return len(o.Alerts)+len(o.Wrote)+len(o.Started)+len(o.Send)+len(o.Judge)+len(o.Errors) == 0 && o.Judged == "" && !o.Stopped && o.Paused == ""
}

// lookPrefixes are the note starts the planner's rows turn on; look reads
// each ticket's latest one from the store in one query per prefix.
var lookPrefixes = []string{"Split into", "Answer:", "QA:", "Review:", "Rereview:", "Kickback:", "Sent up:", "Escalation:", "Hold:"}

// runLook is the planner's look in one call: alerts, every board query,
// the fixed writes, and one line per message to send. What needs reading
// comes out as judge rows, for --judge to hand to one Opus session.
func runLook(ctx context.Context, c *call) error {
	limit := c.fs.Int("limit", -1, "work runs at once: a one-off, or the planner's old way to set it (default: rules set limit's; 0 hands out no build)")
	stall := c.fs.Duration("stall", ticket.DefaultStall, "how long a handout stands before look sends the job again")
	judge := c.fs.Bool("judge", false, "start one judge session on the judge rows, with the standing rules of maestro-ticket rules")
	report := c.fs.Bool("report", false, "add the fixed lines of the report to the chief of staff")
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	if *stall <= 0 {
		return usagef("look: --stall must be more than 0")
	}
	if err := c.needActor("look"); err != nil {
		return err
	}
	lim, alerts, err := c.lookLimit(*limit)
	if err != nil {
		return err
	}
	if *judge {
		b, err := c.readStdin()
		if err != nil {
			return err
		}
		more, err := c.seedRules(string(b))
		if err != nil {
			return err
		}
		alerts = append(alerts, more...)
	}
	hdir, err := c.besideStore("handouts")
	if err != nil {
		return err
	}
	rdir := filepath.Join(filepath.Dir(hdir), "runs")
	co, err := c.coord()
	if err != nil {
		return err
	}
	// After a restart the planner's look may come before any pickup: it
	// takes over runs whose supervisor went away first, as pickup does,
	// so their ends route and their tickets show busy, not free. The
	// planner's own ring too, as doctor's do: only a ring brings it to
	// pickup, where a dead plan or judge run shows.
	c.ringOrphans(ctx, rdir, "")
	var out lookOut
	paused := ""
	if p, ok := co.readPause(time.Now()); ok {
		paused = p.Why + ", until " + p.Until.Format(time.RFC3339)
	}
	stopped, err := co.s.stopped()
	if err != nil {
		return err
	}
	err = c.withStore(ctx, func(s ticket.Store) error {
		var err error
		out, err = c.look(ctx, s, lookOpts{limit: lim, stall: *stall, report: *report, paused: paused,
			stopped: stopped, handouts: co, runs: rdir, host: co.host})
		return err
	})
	if err != nil {
		return err
	}
	c.startSends(ctx, rdir, &out)
	if len(alerts) > 0 {
		out.Alerts = append(alerts, out.Alerts...)
	}
	if *judge && len(out.Judge) > 0 && !out.Stopped && out.Paused == "" {
		if rules, ok := c.standingRules(); ok {
			c.startJudge(ctx, &out, rules, co)
		} else {
			out.Errors = append(out.Errors, "judge: no maestro/rules.md: set the planner's standing rules with maestro-ticket rules text")
		}
	}
	co.pruneHandouts(time.Now())
	if err := c.out(out, func(w io.Writer) { printLook(w, out) }); err != nil {
		return err
	}
	if len(out.Errors) > 0 {
		if c.jsonOut {
			return silentExit{ticket.ExitInternal}
		}
		return errors.New(strings.Join(out.Errors, "; "))
	}
	return nil
}

// startSends starts each send that is a fixed job for a worker bot with a
// webhook, as that bot: the bot would only have woken, reread its chat
// and run job (owner's ruling, Nov 17 2026). A send it can't start stays,
// for the bot to run and see why.
func (c *call) startSends(ctx context.Context, rdir string, out *lookOut) {
	notify := filepath.Join(filepath.Dir(rdir), "notify")
	left := []send{}
	for _, sd := range out.Send {
		k, id, ok := nextStep(runs.Record{}, sd)
		if !ok || !served(notify, sd.Bot) {
			left = append(left, sd)
			continue
		}
		r, err := c.startAs(ctx, rdir, sd.Bot, jobReq{kind: k, ids: []string{id}})
		var going alreadyGoing
		switch {
		case err == nil:
			out.Started = append(out.Started, fmt.Sprintf("%s %s as %s", r.ID, sd.Line, sd.Bot))
		case !errors.As(err, &going):
			left = append(left, sd)
		}
	}
	out.Send = left
}

// refill is the look a run's end takes when its only line for the planner
// was a freed slot or a merge, which woke the planner just to look. It
// looks as the planner, without the judge or the report, starts what it
// hands out, and frees each handout it couldn't start, for the planner's
// own look. It reports whether it left the planner nothing: no judge row,
// error, send or card.
func (c *call) refill(ctx context.Context, rdir string) bool {
	cc := *c
	cc.actor, cc.runCache = "planner", nil
	if cc.mayStart() != nil {
		return false // stopped or paused: the planner's look says so
	}
	co, err := cc.coord()
	if err != nil {
		return false
	}
	lim, _, err := cc.lookLimit(-1)
	if err != nil {
		return false
	}
	s, err := cc.open(ctx)
	if err != nil {
		return false
	}
	out, err := cc.look(ctx, s, lookOpts{limit: lim, stall: ticket.DefaultStall, refill: true, handouts: co, runs: rdir, host: co.host})
	s.Close()
	if err != nil {
		return false
	}
	cc.startSends(ctx, rdir, &out)
	for _, sd := range out.Send {
		if id := ticketInLine.FindString(sd.Line); id != "" {
			co.dropHandout(id, sd.Bot)
		}
	}
	return out.rest == 0 && len(out.Send)+len(out.Judge)+len(out.Errors) == 0
}

// runHolds reports whether run r still holds its ticket at now, so no
// other job goes on it: it runs, or it ended within stall and is its
// bot's to route, or a usage limit stopped it (limited) and it goes on
// as the limit lifts.
func runHolds(r runs.Record, now time.Time, stall time.Duration) (holds, limited bool) {
	// A job run that ended lately and isn't acked is its bot's to
	// route: its pickup sends what comes next. One whose end left
	// the rest to the planner is the planner's look's to route.
	fresh := r.Ended() && r.Job != "" && r.EndedAt != nil && now.Sub(*r.EndedAt) < stall && r.Notify != "planner"
	// One a usage limit stopped goes on when it lifts: its ticket
	// waits for it, however long that takes.
	// Until the limit is a stall past lifting: a ring lost to a
	// reboot must not hold it for good. One whose work landed and
	// whose end was left to the planner doesn't go on: that look
	// answers it.
	limited = r.Ended() && r.Job != "" && r.Notify != "planner" && func() bool {
		out, reset := routeOutcome(r)
		return out == outUsageLimit && now.Before(limitLifts(r, reset).Add(stall))
	}()
	return !r.Ended() || fresh || limited, limited
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return !errors.Is(err, fs.ErrNotExist)
}

// maxJudgeRows is how many rows one judge session takes; the rest wait
// for the next look, so a prompt and its reply stay small.
const maxJudgeRows = 20

// startJudge starts one judge session on the judge rows it can claim,
// each kept as judged in its state, so neither the next look nor one
// running beside this one judges it again.
func (c *call) startJudge(ctx context.Context, out *lookOut, rules string, co coord) {
	k, _ := findJob("judge")
	ids, rows := []string{}, []string{"Rows:"}
	seen := map[string]bool{}
	now := time.Now()
	var claimed []string
	for _, j := range out.Judge {
		if len(rows) > maxJudgeRows || !co.claimJudge(cmp.Or(j.key, j.ID), j.fp, now) {
			continue
		}
		claimed = append(claimed, cmp.Or(j.key, j.ID))
		if !seen[j.ID] {
			ids = append(ids, j.ID)
			seen[j.ID] = true
		}
		rows = append(rows, j.String())
	}
	if len(ids) == 0 {
		out.Judge = nil
		return
	}
	if rules == "" {
		rules = "none"
	}
	text := strings.Join(rows, "\n") + "\nStanding rules:\n" + rules
	r, _, _, err := c.startJob(ctx, jobReq{kind: k, ids: ids, text: text, inherit: true})
	if err != nil {
		for _, k := range claimed { // only this look's: another's stand
			co.dropHandout(k, "judge")
		}
		out.Errors = append(out.Errors, "judge: "+err.Error())
		return
	}
	out.Judged = fmt.Sprintf("%s on %d rows", r.ID, len(rows)-1)
	out.Judge = nil // the session reads them; the bot needn't
}

// claimJudge takes a row for this look's judge, unless the judge took the
// ticket in this state within judgeWindow; as claimHandout does, of two
// looks at once only one takes it.
func (co coord) claimJudge(id, fp string, now time.Time) bool {
	name := handoutName(id, "judge")
	for range 2 {
		if co.s.create(handoutMarks, name, fp+"\n") == nil {
			return true
		}
		if co.judged(id, fp, now) {
			return false
		}
		m, _ := co.s.read(handoutMarks, name)
		if !co.moveStale(handoutMarks, name, m) {
			return false
		}
	}
	return false
}

type lookOpts struct {
	limit    int
	stall    time.Duration
	report   bool
	stopped  bool
	paused   string // a usage limit pickup saw: why, and until when
	handouts coord
	runs     string
	host     string // this machine's id on a shared store, else ""
	refill   bool   // a run's end looks: it sends the chief of staff nothing
}

// launchesSince is a store that lists recent launches with their hosts:
// one machines share.
type launchesSince interface {
	LaunchesSince(ctx context.Context, since time.Time) ([]ticket.Launch, error)
}

// look reads the board once and sorts every open ticket into the
// planner's rows. The fixed rows become sends, starts or writes; the
// rest become judge rows. A ticket with a live claim or run is busy.
func (c *call) look(ctx context.Context, s ticket.Store, o lookOpts) (lookOut, error) {
	now := c.now()
	// Stopped or paused, look still frees what dead runs hold and counts
	// the board, but hands out, starts and sends nothing.
	idle := o.stopped || o.paused != ""
	out := lookOut{Stopped: o.stopped, Paused: o.paused, Alerts: []string{}, Wrote: []string{}, Started: []string{}, Send: []send{}, Judge: []judgeRow{}, Errors: []string{}}
	keep := func(err error) {
		if err != nil {
			out.Errors = append(out.Errors, err.Error())
		}
	}
	rep, errs := c.collectAlerts(ctx, s, now, o.stall)
	for _, e := range errs {
		keep(e)
	}
	out.Alerts = alertLines(rep, now)
	// A died run's bot learns of it through pickup, which resumes it; a
	// missed date goes to the chief of staff, who tells the owner.
	for _, e := range rep.ETAMissed {
		// Once per promise: a missed date stays missed every look.
		key := fmt.Sprintf("eta-%s-%d", e.Goal, e.Due.Unix())
		if o.refill {
			if !idle && o.handouts.handoutBody(key, "chief-of-staff") == "" {
				out.rest++ // the planner's own look sends it
			}
			continue
		}
		if !idle && o.handouts.claimHandout(key, "chief-of-staff", "eta missed", 30*24*time.Hour, now) {
			out.Send = append(out.Send, send{"chief-of-staff", fmt.Sprintf("eta missed: %s due %s; open: %s", e.Goal, e.Due.Local().Format("Jan 2 15:04"), openText(e.Open))})
		}
	}

	open, err := s.List(ctx, ticket.ListFilter{Statuses: openStatuses})
	if err != nil {
		return out, err
	}
	// Work already under way gets the free slots before new work: review
	// and QA first, then builds sent back or stopped, then todo.
	rank := map[ticket.Status]int{ticket.StatusInReview: 0, ticket.StatusInProgress: 1, ticket.StatusBlocked: 2, ticket.StatusTodo: 3, ticket.StatusBacklog: 4}
	slices.SortStableFunc(open, func(a, b ticket.Ticket) int { return rank[a.Status] - rank[b.Status] })
	prefix := map[ticket.ID]string{}
	for _, p := range lookPrefixes {
		l, err := s.List(ctx, ticket.ListFilter{Statuses: openStatuses, LatestPrefix: p})
		if err != nil {
			return out, err
		}
		for _, t := range l {
			prefix[t.ID] = p
		}
	}
	readyTodo, err := idSet(ctx, s, ticket.StatusTodo)
	if err != nil {
		return out, err
	}
	readyGo, err := idSet(ctx, s, ticket.StatusInProgress)
	if err != nil {
		return out, err
	}
	liveRun := map[string]bool{}
	building := map[string]bool{} // tickets with a live build run
	if list, err := runs.List(o.runs, now, false); err == nil {
		for _, r := range list {
			if !r.Ended() && r.Job == "build" && r.Ticket != "" {
				building[r.Ticket] = true
			}
			holds, limited := runHolds(r, now, o.stall)
			if holds && r.Ticket != "" {
				liveRun[r.Ticket] = true
			}
			// A build a usage limit stopped holds its slot till it goes on.
			if limited && r.Job == "build" && r.Ticket != "" {
				building[r.Ticket] = true
			}
			if !r.Ended() {
				for _, tk := range r.Tickets { // a judge run's
					liveRun[tk] = true
				}
			}
		}
	}
	// Another machine's runs have no record here: on a shared store their
	// launches stand for them, live until overdue, fresh for a stall once
	// ended, and held until a usage limit that stopped one is a stall past
	// lifting, as its own records are there.
	if ls, ok := s.(launchesSince); ok && o.host != "" {
		recent, err := ls.LaunchesSince(ctx, now.Add(-o.stall))
		keep(err)
		for _, l := range recent {
			if !elsewhere(l, o.host) {
				continue
			}
			live := l.EndedAt.IsZero() && !launchOver(l, o.host, now)
			fresh := !l.EndedAt.IsZero() && l.Job != "" && now.Sub(l.EndedAt) < o.stall && l.Notify != "planner"
			limited := !l.EndedAt.IsZero() && l.Job != "" && !l.Lifts.IsZero() && now.Before(l.Lifts.Add(o.stall)) && l.Notify != "planner"
			if l.Ticket != 0 && (live || limited) && l.Job == "build" {
				building[l.Ticket.String()] = true
			}
			if l.Ticket != 0 && (live || fresh || limited) {
				liveRun[l.Ticket.String()] = true
			}
			if live {
				for _, tk := range l.Tickets {
					liveRun[tk.String()] = true
				}
			}
		}
	}
	// A build handed out lately holds its slot on its way to a run, by
	// its mark, not by where its ticket falls in this look's order: a
	// ticket that became ready since would take the slot again. A mark
	// older than its ticket's last change was for a build that has run.
	sentAt := map[string]time.Time{}
	if ms, err := o.handouts.s.list(handoutMarks); err == nil {
		for _, m := range ms {
			id, bot, _ := strings.Cut(m.name, ".")
			if (bot == "junior-engineer" || bot == "senior-engineer") && now.Sub(m.at) < o.stall && m.at.After(sentAt[id]) {
				sentAt[id] = m.at
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		keep(err)
	}
	sentBuild := map[string]bool{}
	// The areas of builds on their way to a claim, which the store's
	// ready check can't see yet: a build whose areas overlap one waits.
	onWay := map[ticket.ID][]string{}
	areaHeld := func(t ticket.Ticket) bool {
		for id, as := range onWay {
			if _, _, ok := ticket.AnyAreaOverlaps(t.Areas, as); ok && id != t.ID {
				return true
			}
		}
		return false
	}
	// What goals still have work moving, for "the goal needs it next".
	moving := map[string]bool{}
	going, busyBoard := 0, false
	for _, t := range open {
		if t.Status == ticket.StatusTodo || t.Status == ticket.StatusInProgress || t.Status == ticket.StatusInReview {
			busyBoard = true
		}
		// A build run holds its slot before its session claims, too: the
		// busy check below skips its ticket, so build never counts it.
		claimed := t.Status == ticket.StatusInProgress && t.LiveClaim() != nil
		toBuild := t.Status == ticket.StatusTodo || t.Status == ticket.StatusInProgress
		if claimed || building[t.ID.String()] {
			going++
		} else if at, ok := sentAt[t.ID.String()]; ok && at.After(t.UpdatedAt) && toBuild {
			sentBuild[t.ID.String()] = true
			going++
		}
		if toBuild && !claimed && (building[t.ID.String()] || sentBuild[t.ID.String()]) {
			onWay[t.ID] = t.Areas
		}
		if t.Goal != "" && (t.Status == ticket.StatusTodo || t.Status == ticket.StatusInProgress || t.Status == ticket.StatusInReview) {
			moving[t.Goal] = true
		}
	}
	free := max(o.limit-going, 0)

	n := struct{ busy, build, review, blocked, sentUp, todo, ready, backlog, waiting int }{}
	var stuck, held, sentUp []string
	hand := func(bot, line string, id ticket.ID) {
		if idle || !o.handouts.claimHandout(id.String(), bot, line, o.stall, now) {
			return
		}
		out.Send = append(out.Send, send{bot, line})
	}
	// build hands a build out while a work slot is free; one handed out
	// lately still holds its slot on its way to a claim, and its areas.
	build := func(bot string, t ticket.Ticket) {
		id := t.ID
		if sentBuild[id.String()] {
			return // its handout is counted in going, and stands
		}
		if idle || free == 0 || areaHeld(t) {
			return
		}
		free-- // a build on its way to a claim holds its slot too
		onWay[id] = t.Areas
		hand(bot, "build "+id.String(), id)
	}
	// judgeIt sends a row to the judge, unless it judged the ticket in
	// this state lately: fp names the state.
	judgeIt := func(t ticket.Ticket, row, facts, fp string) {
		if idle {
			return
		}
		// Trimmed: the file it is checked against is read back trimmed.
		fp = strings.TrimSpace(fmt.Sprintf("%s v%d %d %s", row, t.Version, t.UpdatedAt.UnixMilli(), fp))
		key := t.ID.String()
		if row == "cause" {
			key = "cause" // apart from that ticket's own row
		}
		if o.handouts.judged(key, fp, now) {
			return
		}
		out.Judge = append(out.Judge, judgeRow{t.ID.String(), row, facts, fp, key})
	}
	start := func(kind string, id ticket.ID, text string) {
		// A planner job holds its ticket for hours: one that waits on the
		// chief of staff mustn't start again each look; one that failed
		// frees it in pickup.
		if idle || !o.handouts.claimHandout(id.String(), "planner-job", kind, 6*time.Hour, now) {
			return
		}
		k, _ := findJob(kind)
		req := jobReq{kind: k, text: text, inherit: true}
		if k.ticket {
			req.ids = []string{id.String()}
		} else {
			req.ticket = id.String()
		}
		r, _, _, err := c.startJob(ctx, req)
		var going alreadyGoing
		if errors.As(err, &going) {
			return // the planner's own job runs already
		}
		if err != nil {
			o.handouts.dropHandout(id.String(), "planner-job")
			keep(fmt.Errorf("start %s %s: %w", kind, id, err))
			return
		}
		out.Started = append(out.Started, fmt.Sprintf("%s %s %s", r.ID, kind, id))
	}
	detail := func(id ticket.ID) (ticket.Detail, bool) {
		d, err := s.Get(ctx, id)
		keep(err)
		return d, err == nil
	}
	engineer := func(t ticket.Ticket) string {
		eng, err := sameEngineer(ctx, s, t.ID)
		keep(err)
		if eng != "" {
			return eng
		}
		if t.Tier == ticket.TierJunior {
			return "junior-engineer"
		}
		return "senior-engineer"
	}
	var trouble []string // blocked or sent back: a shared cause?
	// byRole hands out a ticket no engineer builds: a Design: ticket to
	// the designer, a live run to the QA lead, any other role to the
	// judge. It reports whether the ticket was one.
	byRole := func(t ticket.Ticket) bool {
		switch t.Role {
		case "", ticket.RoleEngineer:
			return false
		case ticket.RoleDesigner:
			hand("designer", "design "+t.ID.String(), t.ID)
		case ticket.RoleQALead:
			hand("qa-lead", "live "+t.ID.String(), t.ID)
		default:
			judgeIt(t, "blocked", "a "+string(t.Status)+" ticket for role "+t.Role+": who takes it?", "")
		}
		return true
	}

	for _, t := range open {
		id, p := t.ID, prefix[t.ID]
		if p == "Sent up:" { // waits on the chief of staff, whatever its status
			if t.Status == ticket.StatusBlocked {
				n.blocked++
			}
			n.sentUp++
			sentUp = append(sentUp, id.String())
			continue
		}
		if t.LiveClaim() != nil || liveRun[id.String()] {
			n.busy++
			if t.Status == ticket.StatusBlocked {
				n.blocked++
			}
			continue
		}
		if p == "Split into" {
			start("finish", id, "")
			continue
		}
		switch t.Status {
		case ticket.StatusInReview:
			n.review++
			switch p {
			case "QA:", "Review:":
				hand("qa-lead", "qa "+id.String(), id)
			case "Rereview:":
				hand("reviewer", "review "+id.String(), id)
			case "Answer:":
				d, ok := detail(id)
				if !ok {
					continue
				}
				if e, ok := latestWith(d, "Escalation:"); ok && strings.HasPrefix(e.Body, "Escalation: QA") {
					hand("qa-lead", "qa "+id.String(), id)
				} else {
					judgeIt(t, "rerun", "answered, back in review: "+firstLine(latest(d).Body), fmt.Sprint(latest(d).ID))
				}
			default:
				d, ok := detail(id)
				if !ok {
					continue
				}
				if answerAfterKickback(d) {
					judgeIt(t, "rerun", "back in review after a ruling: "+firstLine(latest(d).Body), fmt.Sprint(latest(d).ID))
				} else {
					hand("reviewer", "review "+id.String(), id)
				}
			}
		case ticket.StatusInProgress:
			n.build++
			if p == "Kickback:" {
				if t.Tries >= 2 {
					trouble = append(trouble, id.String()) // a first send-back is routine
				}
				eng := engineer(t)
				if !readyGo[id] && t.Tries < 3 {
					n.waiting++ // a dep holds it, such as a shared cause's fix
					continue
				}
				switch {
				case t.Tries <= 1:
					build(eng, t)
				case t.Tries == 2 && eng == "junior-engineer":
					build("senior-engineer", t)
				case t.Tries == 2:
					start("plan", id, id.String())
				case o.refill:
					out.rest++
				default:
					c.sendUpTries(ctx, s, t, &out, keep, idle)
				}
				continue
			}
			if !readyGo[id] {
				n.waiting++
				continue
			}
			if p == "Escalation:" {
				judgeIt(t, "blocked", "in progress under an Escalation:, no run on it", "")
				continue
			}
			if t.LeaseExpiries >= 2 {
				judgeIt(t, "blocked", fmt.Sprintf("its runs died or hung %d times: find the cause before it runs again", t.LeaseExpiries), "")
				continue
			}
			if !byRole(t) {
				build(engineer(t), t)
			}
		case ticket.StatusBlocked:
			n.blocked++
			if p == "Answer:" {
				start("rule", id, "again")
				continue
			}
			d, ok := detail(id)
			if !ok {
				continue
			}
			if designReady(d) {
				// The owner's card on a design: up, not stuck.
				if o.refill {
					out.rest++
				} else if !idle {
					var rt routed
					if err := c.designUp(ctx, s, d, "look "+newSessionID()[:8], &rt); err != nil {
						keep(err)
					} else {
						out.Wrote = append(out.Wrote, rt.Wrote...)
						out.Send = append(out.Send, rt.Send...)
					}
				}
				continue
			}
			esc, _ := latestWith(d, "Escalation:")
			first := firstLine(esc.Body)
			stuck = append(stuck, id.String()+" "+trim(first, 80))
			trouble = append(trouble, id.String())
			if finishable(first) {
				start("finish", id, "")
				continue
			}
			if first == "" {
				first = "no Escalation: note; latest " + quoteFirst(latest(d).Body)
			}
			judgeIt(t, "blocked", first, fmt.Sprint(latest(d).ID))
		case ticket.StatusTodo:
			n.todo++
			if !readyTodo[id] || areaHeld(t) {
				n.waiting++
				continue
			}
			n.ready++
			if !byRole(t) {
				bot := "senior-engineer"
				if t.Tier == ticket.TierJunior && t.Tries < 2 { // a split keeps its tries
					bot = "junior-engineer"
				}
				build(bot, t)
			}
		case ticket.StatusBacklog:
			n.backlog++
			switch t.Parked {
			case ticket.ParkedPlanning:
				if stale, err := planStopped(ctx, s, id, now); err != nil {
					keep(err)
				} else if stale && !idle {
					if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: id, From: ticket.StatusBacklog, To: ticket.StatusTodo,
						ExpectVersion: t.Version, Actor: c.actor, Note: "look: its plan stopped"}); err != nil {
						keep(fmt.Errorf("requeue %s: %w", id, err))
					} else {
						out.Wrote = append(out.Wrote, id.String()+" requeued: its plan stopped")
					}
				}
			case ticket.ParkedHold:
				held = append(held, id.String())
				if strings.HasPrefix(t.Title, "Live run:") {
					// A failed live run waits for its fixes: plan them.
					if d, ok := detail(id); ok && strings.HasPrefix(latest(d).Body, "Hold: set by qa-lead: live run failed") {
						start("plan", id, "fix "+id.String())
						continue
					}
				}
				judgeIt(t, "hold", "held; is what lifts it true?", "")
			case ticket.ParkedUnready, ticket.ParkedDeferred:
				if strings.HasPrefix(t.Title, "Follow-ups: ") {
					if d, ok := detail(id); ok {
						if u, ok := urgentSince(d); ok {
							judgeIt(t, "urgent", "a follow-up marked urgent: "+trim(firstLine(u.Body), 100), fmt.Sprint(u.ID))
							continue
						}
					}
				}
				switch {
				case t.Goal != "" && !moving[t.Goal]:
					judgeIt(t, string(t.Parked), "goal "+t.Goal+" has nothing else in todo, build or review", "")
				case t.Goal == "" && !busyBoard && strings.HasPrefix(t.Title, "Follow-ups: "):
					judgeIt(t, string(t.Parked), "follow-ups, and nothing else in todo, build or review", "")
				}
			}
		}
	}
	if len(trouble) >= 3 {
		judgeIt(ticket.Ticket{ID: mustID(trouble[0])}, "cause", capped(trouble)+" are blocked or sent back: one cause?", strings.Join(trouble, " "))

	}
	out.Counts = fmt.Sprintf("open %d; building %d/%d; review %d; blocked %d (sent up %d); todo %d (ready %d); backlog %d; busy %d; waiting on deps or areas %d",
		len(open), going, o.limit, n.review, n.blocked, n.sentUp, n.todo, n.ready, n.backlog, n.busy, n.waiting)
	if o.report {
		rows, err := c.goalProgress(ctx, s)
		keep(err)
		out.Report = append(out.Report, "progress: "+rows, fmt.Sprintf("runs: %d/%d", going, o.limit),
			"stuck: "+capped(stuck), "held: "+capped(held), "sent up: "+capped(sentUp))
	}
	return out, nil
}

var openStatuses = []ticket.Status{ticket.StatusBacklog, ticket.StatusTodo, ticket.StatusInProgress, ticket.StatusInReview, ticket.StatusBlocked}

func idSet(ctx context.Context, s ticket.Store, st ticket.Status) (map[ticket.ID]bool, error) {
	l, err := s.List(ctx, ticket.ListFilter{Statuses: []ticket.Status{st}, Ready: true})
	m := map[ticket.ID]bool{}
	for _, t := range l {
		m[t.ID] = true
	}
	return m, err
}

// answerAfterKickback reports whether a ruling came after the latest
// send-back: the review that follows needs the flags it calls for.
func answerAfterKickback(d ticket.Detail) bool {
	for i := len(d.Comments) - 1; i >= 0; i-- {
		b := d.Comments[i].Body
		if strings.HasPrefix(b, "Kickback:") || strings.HasPrefix(b, "Handoff:") {
			return false
		}
		if strings.HasPrefix(b, "Answer:") {
			return true
		}
	}
	return false
}

// finishable: an Escalation that only finish settles: a PR merged outside
// QA, or a split or drop that waits for a dependent to let go.
func finishable(first string) bool {
	return strings.Contains(first, " merged as ") ||
		(strings.Contains(first, "cancel this once") && (strings.HasPrefix(first, "Escalation: split into") || strings.HasPrefix(first, "Escalation: drop")))
}

// planStopped: a ticket parked for a plan whose run is gone: no claim
// and its latest move over an hour old.
func planStopped(ctx context.Context, s ticket.Store, id ticket.ID, now time.Time) (bool, error) {
	evs, err := s.Events(ctx, ticket.EventFilter{TicketID: id, Type: ticket.EventTransitioned})
	if err != nil || len(evs) == 0 {
		return false, err
	}
	return now.Sub(evs[len(evs)-1].CreatedAt) > time.Hour, nil
}

// sendUpTries is the TRIES 3+ row: the ticket goes to blocked with an
// Escalation that lists each send-back, and up to the chief of staff as
// one card, marked Sent up so no later look sends it again.
func (c *call) sendUpTries(ctx context.Context, s ticket.Store, t ticket.Ticket, out *lookOut, keep func(error), stopped bool) {
	if stopped {
		return
	}
	d, err := s.Get(ctx, t.ID)
	if err != nil {
		keep(err)
		return
	}
	var kicks []string
	for _, cm := range d.Comments {
		if strings.HasPrefix(cm.Body, "Kickback:") {
			kicks = append(kicks, trim(firstLine(cm.Body), 120))
		}
	}
	id, key := t.ID.String(), fmt.Sprintf("look-tries-%s-%d", t.ID, t.Tries)
	q := "split, rewrite or drop " + id + "?"
	co, err := c.coord()
	hid := fmt.Sprintf("%s-tries%d", id, t.Tries)
	if err != nil || !co.claimHandout(hid, "chief-of-staff", key, 24*time.Hour, time.Now()) {
		return // a look beside this one sends it
	}
	failed := func(what string, err error) {
		co.dropHandout(hid, "chief-of-staff") // the next look tries again
		keep(fmt.Errorf("%s %s: %w", what, id, err))
	}
	// The note first, then the move: a stop between leaves an in_progress
	// ticket under an Escalation:, which look sends to the judge, not to
	// a build.
	body := fmt.Sprintf("Escalation: build at TRIES %d: sent back each time for:\n- %s\nQuestion: %s\nReturn-role: engineer", t.Tries, strings.Join(kicks, "\n- "), q)
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: t.ID, Body: body, Actor: c.actor, IdempotencyKey: key + "-note"}); err != nil {
		failed("escalate", err)
		return
	}
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: t.ID, From: ticket.StatusInProgress, To: ticket.StatusBlocked,
		ExpectVersion: t.Version, Actor: c.actor, Note: "sent back " + fmt.Sprint(t.Tries) + " times", IdempotencyKey: key + "-move"}); err != nil {
		failed("block", err)
		return
	}
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: t.ID, Body: "Sent up: chief of staff: " + q, Actor: c.actor, IdempotencyKey: key + "-up"}); err != nil {
		failed("send up", err)
		return
	}
	out.Wrote = append(out.Wrote, fmt.Sprintf("%s blocked at TRIES %d, sent up", id, t.Tries))
	out.Send = append(out.Send, send{"chief-of-staff", fmt.Sprintf("%s What: %q was sent back %d times (%s) | Why now: no engineer takes it until you answer | Options: split it into smaller tickets; rewrite its criteria; drop it | Default: split it, since %d tries on one ticket says it is too big | Question: %s",
		id, t.Title, t.Tries, strings.Join(kicks, "; "), t.Tries, q)})
}

// goalProgress is the report's progress line, from the goals rows.
func (c *call) goalProgress(ctx context.Context, s ticket.Store) (string, error) {
	list, err := s.List(ctx, ticket.ListFilter{})
	if err != nil {
		return "", err
	}
	var parts []string
	for _, r := range goalRows(list, false) {
		if r.Goal == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d%% (done %d, review %d, build %d, todo %d, blocked %d, parked %d)",
			r.Goal, r.PercentDone, r.Done, r.InReview, r.InProgress, r.Todo, r.Blocked, r.Backlog))
	}
	return orNone(parts), nil
}

// alertLines are alerts' lines as look prints them; stalled tickets are
// left out, since their rows hand them out. Revived claims share one
// capped line: each stays revived till its claim ends, so it comes back
// every look.
func alertLines(r alertReport, now time.Time) []string {
	var b strings.Builder
	stalled, revived := r.Stalled, r.Revived
	r.Stalled, r.Revived = nil, nil
	if !r.empty() || r.Restarted {
		printAlerts(&b, r, now)
	}
	r.Stalled, r.Revived = stalled, revived
	out := []string{}
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if l != "" && !strings.HasPrefix(l, "died:") && !strings.HasPrefix(l, "eta missed:") {
			out = append(out, l)
		}
	}
	if len(revived) > 0 {
		var ids []string
		for _, v := range revived {
			ids = append(ids, fmt.Sprintf("%s %dx", v.Ticket, v.Revivals))
		}
		out = append(out, "revived: "+capped(ids)+": leases ran out and came back, so a machine froze or a run hung; each limit still ends it, maestro-ticket stop sooner")
	}
	return out
}

func printLook(w io.Writer, o lookOut) {
	if o.quiet() {
		fmt.Fprintf(w, "nothing to do; %s\n", o.Counts)
		for _, l := range o.Report {
			fmt.Fprintln(w, l)
		}
		return
	}
	if o.Stopped {
		fmt.Fprintln(w, "stopped: maestro/STOP is set: look hands out and starts nothing")
	}
	if o.Paused != "" {
		fmt.Fprintf(w, "paused: %s: look hands out and starts nothing\n", o.Paused)
	}
	for _, l := range o.Alerts {
		fmt.Fprintf(w, "alert: %s\n", l)
	}
	for _, l := range o.Wrote {
		fmt.Fprintf(w, "wrote: %s\n", l)
	}
	for _, l := range o.Started {
		fmt.Fprintf(w, "started: %s\n", l)
	}
	for _, s := range o.Send {
		fmt.Fprintln(w, s.String())
	}
	for i, j := range o.Judge {
		if i == maxJudgeRows {
			fmt.Fprintf(w, "judge: and %d more\n", len(o.Judge)-i)
			break
		}
		fmt.Fprintln(w, j.String())
	}
	if o.Judged != "" {
		fmt.Fprintf(w, "judging: %s\n", o.Judged)
	}
	for _, e := range o.Errors {
		fmt.Fprintf(w, "error: %s\n", e)
	}
	for _, l := range o.Report {
		fmt.Fprintln(w, l)
	}
	fmt.Fprintf(w, "counts: %s\n", o.Counts)
}

// capped names up to 10, then how many more, so a line stays short on a
// board of hundreds.
func capped(xs []string) string {
	if len(xs) > 10 {
		return strings.Join(xs[:10], "; ") + fmt.Sprintf("; and %d more", len(xs)-10)
	}
	return orNone(xs)
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, "; ")
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

func mustID(s string) ticket.ID {
	id, _ := ticket.ParseID(s)
	return id
}
