package cli

import (
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
	"github.com/ProlesDei/maestro/tools/maestro-ticket/sqlitestore"
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

func handoutPath(dir, id, bot string) string { return filepath.Join(dir, id+"."+bot) }

// recordHandouts keeps each send that names a ticket.
func recordHandouts(dir string, sends []send) {
	if len(sends) == 0 {
		return
	}
	os.MkdirAll(dir, 0o755)
	for _, s := range sends {
		id := ticketInLine.FindString(s.Line)
		if id == "" || !runs.BotPattern.MatchString(s.Bot) {
			continue
		}
		p := handoutPath(dir, id, s.Bot)
		tmp := p + ".tmp" + newSessionID()[:8]
		if os.WriteFile(tmp, []byte(s.Line+"\n"), 0o644) == nil {
			os.Rename(tmp, p)
		}
	}
}

// handedOut reports whether bot was sent a job on the ticket within d.
func handedOut(dir, id, bot string, d time.Duration, now time.Time) bool {
	fi, err := os.Stat(handoutPath(dir, id, bot))
	return err == nil && now.Sub(fi.ModTime()) < d
}

// pruneHandouts drops records over a day old.
func pruneHandouts(dir string, now time.Time) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range es {
		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > 24*time.Hour {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// judgeRow is a ticket the planner's look can't settle by rule: the judge
// session reads it and decides.
type judgeRow struct {
	ID    string `json:"id"`
	Row   string `json:"row"`
	Facts string `json:"facts"`
}

func (j judgeRow) String() string { return "judge: " + j.ID + " " + j.Row + ": " + j.Facts }

type lookOut struct {
	Stopped bool       `json:"stopped"`
	Alerts  []string   `json:"alerts"`
	Wrote   []string   `json:"wrote"`
	Started []string   `json:"started"`
	Send    []send     `json:"send"`
	Judge   []judgeRow `json:"judge"`
	Judged  string     `json:"judged,omitempty"`
	Report  []string   `json:"report,omitempty"`
	Counts  string     `json:"counts"`
	Errors  []string   `json:"errors"`
}

func (o lookOut) quiet() bool {
	return len(o.Alerts)+len(o.Wrote)+len(o.Started)+len(o.Send)+len(o.Judge)+len(o.Errors) == 0 && o.Judged == "" && !o.Stopped
}

// lookPrefixes are the note starts the planner's rows turn on; look reads
// each ticket's latest one from the store in one query per prefix.
var lookPrefixes = []string{"Split into", "Answer:", "QA:", "Review:", "Rereview:", "Kickback:", "Sent up:", "Escalation:", "Hold:"}

// runLook is the planner's look in one call: alerts, every board query,
// the fixed writes, and one line per message to send. What needs reading
// comes out as judge rows, for --judge to hand to one Opus session.
func runLook(ctx context.Context, c *call) error {
	limit := c.fs.Int("limit", -1, "work runs at once, as your memories set (required; 0 hands out no build)")
	stall := c.fs.Duration("stall", ticket.DefaultStall, "how long a handout stands before look sends the job again")
	judge := c.fs.Bool("judge", false, "start one judge session on the judge rows, the standing rules on stdin")
	report := c.fs.Bool("report", false, "add the fixed lines of the report to the chief of staff")
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	if *limit < 0 {
		return usagef("look: pass --limit <n>, the work runs at once your memories set")
	}
	if *stall <= 0 {
		return usagef("look: --stall must be more than 0")
	}
	if err := c.needActor("look"); err != nil {
		return err
	}
	rules := ""
	if *judge {
		b, err := c.readStdin()
		if err != nil {
			return err
		}
		rules = strings.TrimSpace(string(b))
	}
	store, _, err := c.storePath()
	if err != nil {
		return err
	}
	hdir, err := c.besideStore("handouts")
	if err != nil {
		return err
	}
	rdir := filepath.Join(filepath.Dir(hdir), "runs")
	var out lookOut
	err = c.withStore(ctx, func(s ticket.Store) error {
		var err error
		out, err = c.look(ctx, s, lookOpts{limit: *limit, stall: *stall, report: *report,
			stopped: fileExists(filepath.Join(filepath.Dir(store), sqlitestore.StopFile)), handouts: hdir, runs: rdir})
		return err
	})
	if err != nil {
		return err
	}
	if *judge && len(out.Judge) > 0 && !out.Stopped {
		c.startJudge(ctx, &out, rules, hdir)
	}
	recordHandouts(hdir, out.Send)
	pruneHandouts(hdir, time.Now())
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

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return !errors.Is(err, fs.ErrNotExist)
}

// startJudge starts one judge session on every judge row, and keeps each
// as handed out, so the next look doesn't judge it again while it runs.
func (c *call) startJudge(ctx context.Context, out *lookOut, rules, hdir string) {
	k, _ := findJob("judge")
	ids, rows := []string{}, []string{"Rows:"}
	seen := map[string]bool{}
	for _, j := range out.Judge {
		if !seen[j.ID] {
			ids = append(ids, j.ID)
			seen[j.ID] = true
		}
		rows = append(rows, j.String())
	}
	if rules == "" {
		rules = "none"
	}
	text := strings.Join(rows, "\n") + "\nStanding rules:\n" + rules
	r, _, _, err := c.startJob(ctx, jobReq{kind: k, ids: ids, text: text})
	if err != nil {
		out.Errors = append(out.Errors, "judge: "+err.Error())
		return
	}
	out.Judged = r.ID
	sends := []send{}
	for _, id := range ids {
		sends = append(sends, send{"judge", id})
	}
	recordHandouts(hdir, sends)
}

type lookOpts struct {
	limit    int
	stall    time.Duration
	report   bool
	stopped  bool
	handouts string
	runs     string
}

// look reads the board once and sorts every open ticket into the
// planner's rows. The fixed rows become sends, starts or writes; the
// rest become judge rows. A ticket with a live claim or run is busy.
func (c *call) look(ctx context.Context, s ticket.Store, o lookOpts) (lookOut, error) {
	now := c.now()
	out := lookOut{Stopped: o.stopped, Alerts: []string{}, Wrote: []string{}, Started: []string{}, Send: []send{}, Judge: []judgeRow{}, Errors: []string{}}
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
	for _, d := range rep.Died {
		out.Send = append(out.Send, send{"chief-of-staff", fmt.Sprintf("died: run %d, %s on %s, never gave a result", d.ID, d.Actor, ticketOrNone(d.Ticket))})
	}
	for _, e := range rep.ETAMissed {
		out.Send = append(out.Send, send{"chief-of-staff", fmt.Sprintf("eta missed: %s due %s; open: %s", e.Goal, e.Due.Local().Format("Jan 2 15:04"), openText(e.Open))})
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
	if list, err := runs.List(o.runs, now, false); err == nil {
		for _, r := range list {
			if !r.Ended() && r.Ticket != "" {
				liveRun[r.Ticket] = true
			}
		}
	}
	// What goals still have work moving, for "the goal needs it next".
	moving := map[string]bool{}
	going := 0
	for _, t := range open {
		if t.Status == ticket.StatusInProgress && t.LiveClaim() != nil {
			going++
		}
		if t.Goal != "" && (t.Status == ticket.StatusTodo || t.Status == ticket.StatusInProgress || t.Status == ticket.StatusInReview) {
			moving[t.Goal] = true
		}
	}
	free := max(o.limit-going, 0)

	n := struct{ busy, build, review, blocked, sentUp, todo, ready, backlog, waiting int }{}
	var stuck, held, sentUp []string
	hand := func(bot, line string, id ticket.ID) {
		if o.stopped || handedOut(o.handouts, id.String(), bot, o.stall, now) {
			return
		}
		out.Send = append(out.Send, send{bot, line})
	}
	// build hands a build out while a work slot is free; one handed out
	// lately still holds its slot on its way to a claim.
	build := func(bot string, id ticket.ID) {
		if o.stopped || free == 0 {
			return
		}
		free--
		hand(bot, "build "+id.String(), id)
	}
	judgeIt := func(id ticket.ID, row, facts string) {
		if o.stopped || handedOut(o.handouts, id.String(), "judge", o.stall, now) {
			return
		}
		out.Judge = append(out.Judge, judgeRow{id.String(), row, facts})
	}
	start := func(kind string, id ticket.ID, text string) {
		if o.stopped || handedOut(o.handouts, id.String(), "planner", o.stall, now) {
			return
		}
		k, _ := findJob(kind)
		req := jobReq{kind: k, text: text}
		if k.ticket {
			req.ids = []string{id.String()}
		} else {
			req.ticket = id.String()
		}
		r, _, _, err := c.startJob(ctx, req)
		if err != nil {
			keep(fmt.Errorf("start %s %s: %w", kind, id, err))
			return
		}
		out.Started = append(out.Started, fmt.Sprintf("%s %s %s", r.ID, kind, id))
		recordHandouts(o.handouts, []send{{"planner", id.String()}})
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

	for _, t := range open {
		id, p := t.ID, prefix[t.ID]
		if t.Status == ticket.StatusBlocked && p == "Sent up:" {
			n.blocked++
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
					judgeIt(id, "blocked", "answered, back to review: "+firstLine(latest(d).Body))
				}
			default:
				d, ok := detail(id)
				if !ok {
					continue
				}
				if answerAfterKickback(d) {
					judgeIt(id, "blocked", "back to review after a ruling: "+firstLine(latest(d).Body))
				} else {
					hand("reviewer", "review "+id.String(), id)
				}
			}
		case ticket.StatusInProgress:
			n.build++
			if p == "Kickback:" {
				trouble = append(trouble, id.String())
				eng := engineer(t)
				switch {
				case t.Tries <= 1:
					build(eng, id)
				case t.Tries == 2 && eng == "junior-engineer":
					build("senior-engineer", id)
				case t.Tries == 2:
					start("plan", id, id.String())
				default:
					c.sendUpTries(ctx, s, t, &out, keep, o.stopped)
				}
				continue
			}
			if !readyGo[id] {
				n.waiting++
				continue
			}
			if t.LeaseExpiries >= 2 && p != "Escalation:" {
				judgeIt(id, "blocked", fmt.Sprintf("its runs died or hung %d times: find the cause before it runs again", t.LeaseExpiries))
				continue
			}
			build(engineer(t), id)
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
			judgeIt(id, "blocked", first)
		case ticket.StatusTodo:
			n.todo++
			if !readyTodo[id] {
				n.waiting++
				continue
			}
			n.ready++
			bot := "senior-engineer"
			if t.Tier == ticket.TierJunior {
				bot = "junior-engineer"
			}
			build(bot, id)
		case ticket.StatusBacklog:
			n.backlog++
			switch t.Parked {
			case ticket.ParkedPlanning:
				if stale, err := planStopped(ctx, s, id, now); err != nil {
					keep(err)
				} else if stale && !o.stopped {
					if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: id, From: ticket.StatusBacklog, To: ticket.StatusTodo,
						ExpectVersion: t.Version, Actor: c.actor, Note: "look: its plan stopped"}); err != nil {
						keep(fmt.Errorf("requeue %s: %w", id, err))
					} else {
						out.Wrote = append(out.Wrote, id.String()+" requeued: its plan stopped")
					}
				}
			case ticket.ParkedHold:
				held = append(held, id.String())
				judgeIt(id, "hold", "held; is what lifts it true?")
			case ticket.ParkedUnready, ticket.ParkedDeferred:
				if strings.HasPrefix(t.Title, "Follow-ups: ") {
					if d, ok := detail(id); ok && urgentText.MatchString(latest(d).Body) && !strings.HasPrefix(latest(d).Body, "Taken:") {
						judgeIt(id, "urgent", "a follow-up marked urgent: "+trim(firstLine(latest(d).Body), 100))
						continue
					}
				}
				if t.Goal != "" && !moving[t.Goal] {
					judgeIt(id, string(t.Parked), "goal "+t.Goal+" has nothing else in todo, build or review")
				}
			}
		}
	}
	if len(trouble) >= 3 {
		judgeIt(mustID(trouble[0]), "cause", strings.Join(trouble, " ")+" are blocked or sent back: one cause?")
	}
	out.Counts = fmt.Sprintf("open %d; building %d/%d; review %d; blocked %d (sent up %d); todo %d (ready %d); backlog %d; busy %d; waiting on deps or areas %d",
		len(open), going, o.limit, n.review, n.blocked, n.sentUp, n.todo, n.ready, n.backlog, n.busy, n.waiting)
	if o.report {
		rows, err := c.goalProgress(ctx, s)
		keep(err)
		out.Report = append(out.Report, "progress: "+rows, fmt.Sprintf("runs: %d/%d", going, o.limit),
			"stuck: "+orNone(stuck), "held: "+orNone(held), "sent up: "+orNone(sentUp))
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
	if _, err := s.Transition(ctx, ticket.TransitionRequest{ID: t.ID, From: ticket.StatusInProgress, To: ticket.StatusBlocked,
		ExpectVersion: t.Version, Actor: c.actor, Note: "sent back " + fmt.Sprint(t.Tries) + " times", IdempotencyKey: key + "-move"}); err != nil {
		keep(fmt.Errorf("block %s: %w", id, err))
		return
	}
	body := fmt.Sprintf("Escalation: build at TRIES %d: sent back each time for:\n- %s\nQuestion: %s\nReturn-role: engineer", t.Tries, strings.Join(kicks, "\n- "), q)
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: t.ID, Body: body, Actor: c.actor, IdempotencyKey: key + "-note"}); err != nil {
		keep(fmt.Errorf("escalate %s: %w", id, err))
		return
	}
	if _, err := s.Comment(ctx, ticket.CommentRequest{ID: t.ID, Body: "Sent up: chief of staff: " + q, Actor: c.actor, IdempotencyKey: key + "-up"}); err != nil {
		keep(fmt.Errorf("send up %s: %w", id, err))
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
// left out, since their rows hand them out.
func alertLines(r alertReport, now time.Time) []string {
	var b strings.Builder
	stalled := r.Stalled
	r.Stalled = nil
	if !r.empty() || r.Restarted {
		printAlerts(&b, r, now)
	}
	r.Stalled = stalled
	out := []string{}
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if l != "" && !strings.HasPrefix(l, "died:") && !strings.HasPrefix(l, "eta missed:") {
			out = append(out, l)
		}
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
		fmt.Fprintln(w, "stopped: maestro/STOP is set: look hands out, starts and writes nothing")
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
	for _, j := range o.Judge {
		fmt.Fprintln(w, j.String())
	}
	if o.Judged != "" {
		fmt.Fprintf(w, "judging: %s, a judge session on the judge rows\n", o.Judged)
	}
	for _, e := range o.Errors {
		fmt.Fprintf(w, "error: %s\n", e)
	}
	for _, l := range o.Report {
		fmt.Fprintln(w, l)
	}
	fmt.Fprintf(w, "counts: %s\n", o.Counts)
}

func ticketOrNone(id ticket.ID) string {
	if id == 0 {
		return "no ticket"
	}
	return id.String()
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
