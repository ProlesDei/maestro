package cli

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// unackedAfter is how long an ended run may wait for its owner's ack
// before doctor flags it.
const unackedAfter = 15 * time.Minute

// keepAcked is how long a run's files stay after its result was acked.
const keepAcked = 7 * 24 * time.Hour

// besideStore returns the folder name next to the ticket store, which every
// worktree shares, refusing where open would.
func (c *call) besideStore(name string) (string, error) {
	loc, err := c.location()
	if err != nil {
		return "", err
	}
	if loc.missing() {
		return "", fmt.Errorf("%w: no ticket store at %s: run maestro-ticket in the checkout Maestro is installed in, or a worktree of it", ticket.ErrInvalid, loc.sqlitePath)
	}
	c.storeChecks = c.envChecks(loc.sqlitePath)
	if err := envcheck.Err(c.storeChecks); err != nil {
		return "", err
	}
	return filepath.Join(loc.localDir, name), nil
}

func (c *call) needActor(cmd string) error {
	if c.actor == "" {
		return usagef("%s: set MAESTRO_TICKET_ACTOR (or --actor) to your bot id: a run's result goes back to whoever launched it", cmd)
	}
	return nil
}

func parseRings(s string) ([]time.Duration, error) {
	var out []time.Duration
	for _, p := range strings.Split(s, ",") {
		d, err := time.ParseDuration(strings.TrimSpace(p))
		if err != nil || d < 0 || d > 24*time.Hour || (len(out) > 0 && d <= out[len(out)-1]) {
			return nil, usagef("--rings %q: want rising durations from 0s to 24h, comma-separated", s)
		}
		out = append(out, d)
	}
	if len(out) > 20 {
		return nil, usagef("--rings: at most 20")
	}
	return out, nil
}

func ringsText(ds []time.Duration) string {
	s := make([]string, len(ds))
	for i, d := range ds {
		s[i] = short(d)
	}
	return strings.Join(s, ",")
}

// short prints a duration as 2h, 1h5m or 400ms, exactly, so it parses
// back to the same value.
func short(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// ago prints an elapsed time to the second.
func ago(d time.Duration) string { return short(d.Round(time.Second)) }

// stdinQuiet is how long launch waits for a first byte on a stdin that
// stays open before it reads it as empty.
const stdinQuiet = 3 * time.Second

// readStdin reads the prompt for the command, unless stdin is a terminal.
// A heredoc arrives at once and ends; a pipe nobody writes to or closes,
// as some harnesses give a command with no stdin of its own, would block
// forever, so one that sends nothing for stdinQuiet counts as empty. Once
// text comes, it reads to the end.
func (c *call) readStdin() ([]byte, error) {
	if f, ok := c.env.Stdin.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return nil, nil
		}
	}
	chunks, end, done := make(chan []byte), make(chan error, 1), make(chan struct{})
	defer close(done)
	go func() {
		for {
			b := make([]byte, 64<<10)
			n, err := c.env.Stdin.Read(b)
			if n > 0 {
				select {
				case chunks <- b[:n]:
				case <-done:
					return
				}
			}
			if err != nil {
				end <- err
				return
			}
		}
	}()
	var out []byte
	quiet := time.NewTimer(stdinQuiet)
	defer quiet.Stop()
	for {
		select {
		case b := <-chunks:
			if out = append(out, b...); len(out) > maxBody {
				return nil, usagef("launch: stdin is over %d bytes: put long text in a file and name its path", maxBody)
			}
			quiet.Stop()
		case err := <-end:
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return out, err
		case <-quiet.C:
			return out, nil
		}
	}
}

// fromTimeout reads a leading "timeout DURATION", as a bot that follows
// an older time-limit rule writes it, as the run's limit. GNU timeout
// takes a number of seconds, or one with an s, m, h or d after it.
func fromTimeout(command []string) ([]string, time.Duration, bool) {
	if len(command) < 3 {
		return nil, 0, false
	}
	m := timeoutArg.FindStringSubmatch(command[1])
	if m == nil {
		return nil, 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	unit := map[string]time.Duration{"": time.Second, "s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
	if err != nil || n*unit.Seconds() > (24*time.Hour).Seconds() {
		return nil, 0, false
	}
	return command[2:], time.Duration(n * float64(unit)), true
}

var timeoutArg = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)([smhd]?)$`)

// envArg is a NAME=value word, as a shell reads one before a command.
var envArg = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// withEnv runs a command that starts with NAME=value words, as a shell
// would, through env, since launch runs no shell.
func withEnv(command []string) []string {
	n := 0
	for n < len(command) && envArg.MatchString(command[n]) {
		n++
	}
	if n == 0 {
		return command
	}
	return append([]string{"env"}, command...)
}

// runLaunch starts a command, Claude Code as a rule, in the background
// under a supervisor that records it, keeps it to its time limit, writes
// its result and rings its launcher's webhook. It returns once the command
// runs, with the run's id.
func runLaunch(ctx context.Context, c *call) error {
	var command []string
	if i := slices.Index(c.args, "--"); i >= 0 {
		c.args, command = c.args[:i], c.args[i+1:]
	}
	tk := c.fs.String("ticket", "", "the ticket the run works on")
	limit := c.fs.Duration("limit", 2*time.Hour, "stop the run after this long (1s to 24h)")
	ringsFlag := c.fs.String("rings", ringsText(runs.DefaultRings), "when to ring your webhook after the run ends, while you haven't acked it")
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	if !runs.Supported {
		return usagef("launch needs a unix system")
	}
	if len(command) == 0 {
		return usagef("launch: name the command after --, such as -- claude -p --output-format json")
	}
	command = withEnv(command)
	if filepath.Base(command[0]) == "timeout" {
		limitSet := false
		c.fs.Visit(func(f *flag.Flag) { limitSet = limitSet || f.Name == "limit" })
		rest, d, ok := fromTimeout(command)
		if !ok || limitSet {
			return usagef("launch: give the time limit as --limit, not as timeout before the command")
		}
		command, *limit = rest, d
	}
	if err := c.needActor("launch"); err != nil {
		return err
	}
	ticketID := ""
	if *tk != "" {
		id, err := ticket.ParseID(*tk)
		if err != nil {
			return err
		}
		ticketID = id.String()
	}
	if *limit < time.Second || *limit > 24*time.Hour {
		return usagef("launch: --limit %s: want 1s to 24h", *limit)
	}
	rings, err := parseRings(*ringsFlag)
	if err != nil {
		return err
	}
	stdin, err := c.readStdin()
	if err != nil {
		return err
	}
	r, err := c.startRun(runs.Record{Ticket: ticketID, Limit: short(*limit), Command: command}, stdin, rings, nil, nil)
	if err != nil {
		return err
	}
	return c.out(r, func(w io.Writer) {
		on := ""
		if r.Ticket != "" {
			on = " on " + r.Ticket
		}
		fmt.Fprintf(w, "%s %s%s, limit %s. maestro-ticket pickup shows it once it ends.\n", r.ID, r.State, on, r.Limit)
	})
}

// startSupervisor starts a run's supervisor and lets it go. Unit tests,
// which start no process, stand in for it.
var startSupervisor = func(sup *exec.Cmd) error {
	if err := sup.Start(); err != nil {
		return err
	}
	return sup.Process.Release()
}

// startRun records a run for the actor and hands it to a detached
// supervisor, and returns once its command runs. launch, job and resume
// all start runs here; job, again and resume give side, its facts for
// stats, and launch none: its side file is empty, and never streams.

// mark, if set, writes the run's id in its marks before it can run.
func (c *call) startRun(rec runs.Record, stdin []byte, rings []time.Duration, side *sideFacts, mark func(runID string) error) (runs.Record, error) {
	dir, err := c.besideStore("runs")
	if err != nil {
		return runs.Record{}, err
	}
	store, _, err := c.storePath()
	if err != nil {
		return runs.Record{}, err
	}
	rec.Launcher, rec.Dir, rec.StartedAt = c.actor, c.env.Dir, time.Now().UTC().Round(time.Millisecond)
	r, err := runs.Create(dir, rec, stdin)
	if err != nil {
		return runs.Record{}, err
	}
	// Before the supervisor, which may end the run at once; stats off
	// writes none.
	if stats.Off(filepath.Dir(dir), c.env.Getenv, c.statsUnsafe()) == "" {
		if side == nil {
			side = &sideFacts{}
		} else {
			side.Stream = c.streamOK(store)
		}
		writeSide(dir, r.ID, side)
	}
	// A run going under an empty mark is any start's to take over once
	// activeGrace passes, so a mark that can't name it stops it here.
	if mark != nil {
		if err := mark(r.ID); err != nil {
			r.State, r.Error = runs.Failed, "mark the run: "+err.Error()
			runs.Save(dir, r)
			runs.Ack(dir, r.ID, c.actor, time.Now())
			return runs.Record{}, fmt.Errorf("launch %s: mark the run: %w", r.ID, err)
		}
	}
	// The supervisor outlives this command, in a session of its own: a
	// harness ends the command's session when it returns.
	exe, err := os.Executable()
	if err == nil {
		sup := exec.Command(exe, "supervise", r.ID, "--store", store, "--rings", ringsText(rings))
		sup.Dir = c.env.Dir
		// Not tagged with the run this launch runs in, if any: when that
		// run ends, what carries its id is killed, and this run isn't its.
		sup.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, runs.RunIDEnv+"=") })
		sup.SysProcAttr = runs.Detach()
		err = startSupervisor(sup)
	}
	if err != nil {
		r.State, r.Error = runs.Failed, "start the supervisor: "+err.Error()
		runs.Save(dir, r)
		runs.Ack(dir, r.ID, c.actor, time.Now())
		return runs.Record{}, fmt.Errorf("%w: launch: %s", ticket.ErrInvalid, r.Error)
	}
	// Wait until the command runs, so a command that can't start fails
	// here and not in a later pickup.
	deadline := time.Now().Add(10 * time.Second)
	for r.State == runs.Starting && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if got, err := runs.Load(dir, r.ID); err == nil {
			r = got
		}
	}
	if r.Ended() && r.PID == 0 {
		if ok, _ := runs.Ack(dir, r.ID, c.actor, time.Now()); ok {
			return runs.Record{}, fmt.Errorf("%w: launch %s: %s", ticket.ErrInvalid, r.ID, r.Error)
		}
	}
	return r, nil
}

// runSupervise is the background half of launch. launch starts it; nothing
// else should.
func runSupervise(ctx context.Context, c *call) error {
	ringsFlag := c.fs.String("rings", ringsText(runs.DefaultRings), "when to ring after the run ends")
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) != 1 || !runs.ValidID(c.args[0]) {
		return usagef("supervise: want one run id")
	}
	rings, err := parseRings(*ringsFlag)
	if err != nil {
		return err
	}
	dir, err := c.besideStore("runs")
	if err != nil {
		return err
	}
	notify := filepath.Join(filepath.Dir(dir), "notify")
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGHUP)
	defer stop()
	c.startStats(filepath.Dir(dir))
	defer c.waitStats() // the row of a run whose first ring never came
	co := c.runsCoord(dir)
	return runs.Supervise(ctx, dir, c.args[0], runs.Options{
		Env:    os.Environ(),
		Rings:  rings,
		Stream: c.streamNames(dir, c.args[0]),
		Hook:   func(bot string) (runs.Hook, error) { return runs.Webhook(notify, bot) },
		Gate:   notify,
		Digest: func(bot string, now time.Time) (runs.Notification, []string) {
			return ringDigest(co, dir, bot, now)
		},
		// The supervisor stands in for log-run: it lives as long as the
		// run, so alerts reads its pid as the run's.
		OnStart: func(r runs.Record) int64 {
			c.actor = r.Launcher
			return c.startLaunch(ctx, runTicket(r), r)
		},
		OnEnd: func(r runs.Record) {
			c.endRun(context.WithoutCancel(ctx), dir, r)
			// Bounded: a hung gh in a review's start must not hold the ring.
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), chainLimit)
			defer cancel()
			c.chain(cctx, dir, r)
		},
		// A run a usage limit stopped rings once more as the limit lifts:
		// nothing else wakes its bot then.
		RingAgain: func(r runs.Record) time.Time {
			if out, reset := routeOutcome(r); out == outUsageLimit {
				return limitLifts(r, reset).Add(30 * time.Second)
			}
			return time.Time{}
		},
		// Stats wait for the first ring: none may hold it up.
		AfterRing: func(r runs.Record, err error) { c.afterRing(dir, r, err) },
	})
}

// endRun does what log-run did once a run ended: record its end and its
// cost, and, for a run that gave no result, free the claim its bot held
// on its ticket, unless another run of that bot on that ticket still
// goes. Neither may hold up the ring, so failures only warn on stderr.
func (c *call) endRun(ctx context.Context, dir string, r runs.Record) {
	if co, err := c.coord(); err == nil {
		co.dropActive(r)
	}
	warn := func(err error) {
		if err != nil {
			fmt.Fprintf(c.env.Stderr, "maestro-ticket: run %s: %v\n", r.ID, err)
		}
	}
	id := runTicket(r)
	out, _ := runs.ReadTail(filepath.Join(dir, r.ID+".out"), runs.OutputWindow)
	if len(out) == runs.OutputWindow {
		if i := bytes.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:] // a cut first line isn't JSON
		}
	}
	reply, replyErr := lastResult(out)
	// Last, after the claim is freed, and silent whatever fails. A died
	// run's record holds no reply: its row reads the one its output does.
	sr := r
	if sr.State == runs.Died && sr.Reply == "" && replyErr == nil {
		var session string
		sr.Reply, session, sr.IsError = runs.ParseOutput(out)
		sr.SessionID = cmp.Or(sr.SessionID, session)
	}
	defer c.writeStats(dir, sr, reply, replyErr)
	s, err := c.open(ctx)
	if err != nil {
		warn(err)
		return
	}
	defer s.Close()
	end := ticket.EndLaunchRequest{ID: r.LaunchID, Outcome: ticket.LaunchResult, Session: reply.SessionID}
	switch {
	case r.State == runs.Died:
		end.Outcome = ticket.LaunchDied
	case replyErr != nil:
		end.Outcome = ticket.LaunchNoResult
	}
	// Another machine holds the ticket for it until the limit lifts, as
	// this machine's look does from its record.
	if out, reset := outcomeOf(r); out == outUsageLimit {
		end.Lifts = limitLifts(r, reset)
	}
	c.endLaunch(ctx, s, end)
	if replyErr == nil {
		warn(logRun(ctx, s, reply, id, r.Launcher, c.env.Stderr))
	}
	if r.State == runs.Done || (r.State == runs.Failed && r.SessionID != "") {
		return
	}
	host, _ := c.hostID()
	_, err = freeRunClaim(ctx, s, logActor(r.Launcher), id, logActor(r.Launcher), host)
	warn(err)
}

// runTicket is the ticket a run worked on, or 0.
func runTicket(r runs.Record) ticket.ID {
	id, _ := ticket.ParseID(r.Ticket)
	return id
}

// ringOrphans rings for each run whose supervisor went away before a
// ring reached its launcher (the machine restarted, or it was killed):
// it saves a run that had no result as died, kills what is left of it,
// frees its ticket as the supervisor would have, routes it, and rings.
// pickup and doctor call it, whoever runs them, so the first look after
// a restart wakes every bot that lost a run: one ring per bot, through
// its gate. A ring that fails, or that the gate held back, is tried again
// on a later look. It rings none for own, the bot whose pickup shows its
// orphans in this same call; it returns those, unrung, for that pickup
// to mark once it shows them.
func (c *call) ringOrphans(ctx context.Context, dir, own string) []string {
	ids, err := runs.Orphans(dir, time.Now())
	if err != nil || len(ids) == 0 {
		return nil
	}
	notify := filepath.Join(filepath.Dir(dir), "notify")
	co := c.runsCoord(dir) // once: the rings below run at once
	type adopted struct {
		r       runs.Record
		release func()
	}
	var mu sync.Mutex
	byOwner := map[string][]adopted{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			r, release, ok := runs.Adopt(dir, id, time.Now().UTC().Round(time.Millisecond))
			if !ok {
				return
			}
			// Each works as the run's own supervisor would: as its
			// launcher, on its own copy of the call, whose caches no other
			// goroutine shares.
			cc := *c
			cc.actor, cc.runCache = r.Launcher, nil
			switch {
			case r.State == runs.Died && r.OrphanTries == 0:
				runs.KillRun(r.ID)
				r.Settling = true
				runs.Save(dir, r)
				// What its supervisor would have read of its stream, so
				// a result Claude printed still logs its cost.
				r.Stream = runs.RecoverStream(dir, r.ID, scanNames())
				cc.endRun(ctx, dir, r)
				fallthrough
			case r.Settling:
				// Its end was never routed, or its supervisor died while it
				// routed it: route it now, so its ring carries its lines.
				// Every start the chain makes is safe to make twice.
				cctx, cancel := context.WithTimeout(ctx, chainLimit)
				cc.chain(cctx, dir, r)
				cancel()
				if got, err := runs.Load(dir, r.ID); err == nil {
					r = got
				}
				r.Settling = false
				runs.Save(dir, r)
			}
			// One killed after it saved the end leaves the file too.
			runs.DropStream(dir, r.ID)
			mu.Lock()
			byOwner[r.Owner()] = append(byOwner[r.Owner()], adopted{r, release})
			mu.Unlock()
		}()
	}
	wg.Wait()
	var mine []string
	for owner, as := range byOwner {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				for _, a := range as {
					a.release()
				}
			}()
			if owner == own {
				for _, a := range as {
					mine = append(mine, a.r.ID)
				}
				return
			}
			h, err := runs.Webhook(notify, owner)
			if err != nil || h.URL == "" {
				msg := ""
				if err != nil {
					msg = err.Error()
				}
				// Nothing to ring: write the record only when this
				// changes it, not on every look.
				for _, a := range as {
					if a.r.OrphanTries < 1 || a.r.RingError != msg {
						a.r.RingError, a.r.OrphanTries = msg, max(a.r.OrphanTries, 1)
						runs.Save(dir, a.r)
					}
				}
				return
			}
			ids := make([]string, len(as))
			for i, a := range as {
				ids[i] = a.r.ID
			}
			now := time.Now().UTC().Round(time.Millisecond)
			g, err := runs.RingBot(ctx, dir, notify, owner, ids, h, now, func(now time.Time) (runs.Notification, []string) {
				return ringDigest(co, dir, owner, now)
			})
			for _, a := range as {
				r := a.r
				if g.Tried {
					r.Rings++
				}
				switch {
				case err != nil:
					// Only a ring that failed counts as a try: one the gate
					// held back, or that only counted it, is no failure.
					r.OrphanTries++
					r.RingError = err.Error()
				case g.Covers(r.ID):
					r.Rang, r.RingError = true, ""
				case !g.Tried:
					continue // the gate held it back: a later look rings it
				}
				runs.Save(dir, r)
			}
		}()
	}
	wg.Wait()
	return mine
}

// markRang marks own orphans pickup just showed as rung: its bot read
// them in this call, so no ring need wake it for them.
func markRang(dir string, ids []string) {
	for _, id := range ids {
		if r, release, ok := runs.Adopt(dir, id, time.Now().UTC().Round(time.Millisecond)); ok {
			r.Rang = true
			runs.Save(dir, r)
			release()
		}
	}
}

// runPickup shows each run the actor launched that ended and that it
// hasn't acked, and lists its runs still going. Bots run it before they
// end a turn. It shows a result again until the bot acks it, so a bot that
// stopped before it acted on one loses nothing.
func runPickup(ctx context.Context, c *call) error {
	full := c.fs.Bool("full", false, "print every ended job's whole reply")
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	if err := c.needActor("pickup"); err != nil {
		return err
	}
	dir, err := c.besideStore("runs")
	if err != nil {
		return err
	}
	own := c.ringOrphans(ctx, dir, c.actor)
	now := time.Now().UTC().Round(time.Millisecond)
	list, err := runs.List(dir, now, false)
	if err != nil {
		return err
	}
	ended, running := []runs.Record{}, []runs.Record{}
	for _, r := range list {
		switch {
		case r.Owner() != c.actor:
		case !r.Ended() || r.Settling: // a settling run's end is still being routed
			running = append(running, r)
		default:
			ended = append(ended, r)
		}
	}
	runs.Prune(dir, keepAcked, now)
	// A job's result is sorted and routed, so the bot reads a few lines
	// and sends what they say; a plain launch prints as it ran.
	routes, routeErr := map[string]routed{}, map[string]error{}
	if slices.ContainsFunc(ended, func(r runs.Record) bool { return r.Job != "" }) {
		s, err := c.open(ctx)
		for _, r := range ended {
			switch {
			case r.Job == "":
			case len(r.Routed) > 0:
				var rt routed
				if err := json.Unmarshal(r.Routed, &rt); err != nil {
					routeErr[r.ID] = err
					break
				}
				if err == nil {
					c.refileFollowUps(ctx, s, r, &rt)
				}
				routes[r.ID] = rt
			case err != nil:
				routeErr[r.ID] = err
			default:
				if rt, err := c.route(ctx, s, r); err != nil {
					routeErr[r.ID] = err
				} else {
					routes[r.ID] = rt
				}
			}
		}
		if err == nil {
			s.Close()
		}
	}
	// While a usage limit holds, the runs it stopped wait: no do: line
	// to retry, no ack, one line for all; they ring again as it lifts.
	// The rest come first, 20 at most a call.
	co, err := c.coord()
	if err != nil {
		return err
	}
	pause, paused := co.readPause(now)
	why, held := co.startsHeld(now)
	waiting, stuck, shown := []string{}, []string{}, []runs.Record{}
	more := 0
	for _, r := range ended {
		rt, ok := routes[r.ID]
		switch {
		// The run that set the pause shows whole, so its bot tells the
		// planner; the rest of those it stopped wait.
		case ok && paused && rt.Outcome == outUsageLimit && !slices.ContainsFunc(rt.Wrote, func(w string) bool { return strings.HasPrefix(w, "paused every job") }):
			waiting = append(waiting, r.ID)
		// While STOP or a pause holds, a do: line is only refused: the
		// run waits, one line for all, so no pickup reruns it in a loop.
		case ok && held && doWaits(rt):
			stuck = append(stuck, r.ID)
		case len(shown) == maxPickup:
			more++ // the next pickup, after these are acked, shows them
		default:
			shown = append(shown, r)
		}
	}
	ended = shown
	markRang(dir, slices.DeleteFunc(own, func(id string) bool {
		return !slices.ContainsFunc(shown, func(r runs.Record) bool { return r.ID == id })
	}))
	routeList := []routed{}
	for _, r := range ended {
		if rt, ok := routes[r.ID]; ok {
			// A job a look handed out lately goes out once: pickup drops
			// its own line for it, and holds the rest from the next look.
			if rt.Send, err = co.pickupSends(r.ID, r.StartedAt, rt.Send, now); err != nil {
				routeErr[r.ID] = fmt.Errorf("handouts not checked, so a send may go twice: %w", err)
			}
			routes[r.ID] = rt
			routeList = append(routeList, rt)
		}
	}
	return c.out(map[string]any{"ended": ended, "running": running, "routes": routeList, "waiting": waiting, "held": stuck}, func(w io.Writer) {
		ids := []string{}
		for _, r := range ended {
			// A run with a do: line is acked by that command when it
			// starts; one it refused stays for the next pickup.
			if rt, ok := routes[r.ID]; !ok || len(rt.Do) == 0 {
				ids = append(ids, r.ID)
			}
			rt, ok := routes[r.ID]
			if !ok || *full {
				printRun(w, r, now)
			}
			if err := routeErr[r.ID]; err != nil {
				fmt.Fprintf(w, "route: %v\n", err)
			}
			if ok {
				printRoute(w, rt, *full)
			}
		}
		defer func() {
			if len(waiting) > 0 {
				fmt.Fprintln(w, waitingLine(len(waiting), pause.Until))
			}
			if len(stuck) > 0 {
				fmt.Fprintln(w, heldLine(len(stuck), why))
			}
			if more > 0 {
				fmt.Fprintf(w, "and %d more ended: run pickup again once these are acked\n", more)
			}
			if len(ids) > 0 { // last, as the team rules say
				fmt.Fprintf(w, "ack each once you have acted on it: maestro-ticket ack %s\n", strings.Join(ids, " "))
			}
		}()
		switch {
		case len(ended) == 0 && len(running) == 0:
			fmt.Fprintln(w, "no run of yours ended or running")
		case len(running) == 0:
		default:
			if len(ended) == 0 {
				fmt.Fprint(w, "no run of yours ended; ")
			}
			s := []string{}
			for i, r := range running {
				if i == 10 { // a bot reads this every turn: keep it short
					s = append(s, fmt.Sprintf("and %d more", len(running)-10))
					break
				}
				since := ago(now.Sub(r.StartedAt))
				if r.Settling {
					since = "ending"
				}
				s = append(s, fmt.Sprintf("%s (%s, %s)", r.ID, dash(r.Ticket), since))
			}
			fmt.Fprintf(w, "running: %s\n", strings.Join(s, ", "))
		}
	})
}

// runAck records that the actor acted on each named run's result: pickup
// stops showing it and its rings stop. Only the first ack of a run counts.
func runAck(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) == 0 {
		return usagef("ack: name the run ids pickup showed")
	}
	if err := c.needActor("ack"); err != nil {
		return err
	}
	dir, err := c.besideStore("runs")
	if err != nil {
		return err
	}
	now := time.Now().UTC().Round(time.Millisecond)
	type result struct {
		Run     string     `json:"run"`
		Acked   bool       `json:"acked"`
		AckedBy string     `json:"acked_by"`
		AckedAt *time.Time `json:"acked_at"`
		LeftTo  string     `json:"left_to,omitempty"`
	}
	var res []result
	for _, id := range c.args {
		if !runs.ValidID(id) {
			return usagef("ack: %q is not a run id, such as R-abcdefgh", id)
		}
		r, err := runs.Get(dir, id, now)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: run %s", ticket.ErrNotFound, id)
		}
		if err != nil {
			return err
		}
		if !r.Ended() {
			return fmt.Errorf("%w: ack: run %s is %s: ack it once pickup shows it ended", ticket.ErrStatusConflict, id, r.State)
		}
		if r.Owner() != c.actor && r.Launcher == c.actor {
			// Its end left it to another since this bot's pickup listed
			// it: that bot acts on it, and the rest of the batch goes on.
			res = append(res, result{Run: id, LeftTo: r.Owner()})
			continue
		}
		if r.Owner() != c.actor {
			return fmt.Errorf("%w: ack: run %s is %s's: only the bot it was left to acks it", ticket.ErrForbidden, id, dash(r.Owner()))
		}
		ok, err := runs.Ack(dir, id, c.actor, now)
		if err != nil {
			return err
		}
		if ok {
			r.AckedAt, r.AckedBy = &now, c.actor
		} else if r, err = runs.Get(dir, id, now); err != nil {
			return err
		}
		res = append(res, result{Run: id, Acked: ok, AckedBy: r.AckedBy, AckedAt: r.AckedAt})
	}
	more := c.moreUnacked(dir, now)
	return c.out(map[string]any{"acks": res, "more": more}, func(w io.Writer) {
		defer func() {
			if more > 0 { // last: the bot's routine reads it
				fmt.Fprintf(w, "%d more unacked for you: run pickup\n", more)
			}
		}()
		for _, a := range res {
			switch {
			case a.LeftTo != "":
				fmt.Fprintf(w, "%s is left to %s: nothing to do\n", a.Run, a.LeftTo)
			case a.Acked:
				fmt.Fprintf(w, "acked %s\n", a.Run)
			default:
				fmt.Fprintf(w, "%s was already acked by %s\n", a.Run, dash(a.AckedBy))
			}
		}
	})
}

// moreUnacked counts the actor's settled results still to ack, past the
// ones just acked: those a ring only counted, or held for pickup, or that
// ended since. Runs a usage limit holds while its pause holds don't
// count: they ring again as it lifts. Nor, while STOP or a pause holds,
// does any run with a do: line: its command is refused till then, so a
// pickup for it would only run it to be refused again.
func (c *call) moreUnacked(dir string, now time.Time) int {
	list, _ := runs.Unacked(dir, c.actor, now)
	co := c.runsCoord(dir)
	_, paused := co.readPause(now)
	_, held := co.startsHeld(now)
	n := 0
	for _, r := range list {
		if slices.Contains(c.args, r.ID) || runs.Acked(dir, r.ID) {
			continue
		}
		var rt routed
		if len(r.Routed) > 0 && json.Unmarshal(r.Routed, &rt) == nil {
			if paused && rt.Outcome == outUsageLimit && !slices.ContainsFunc(rt.Wrote, func(w string) bool { return strings.HasPrefix(w, "paused every job") }) {
				continue
			}
			if held && len(rt.Do) > 0 {
				continue
			}
		}
		n++
	}
	return n
}

// stopWait is how long stop waits for the supervisor to record the end:
// its look for the ask, then the kill grace.
const stopWait = runs.KillGrace + 10*time.Second

// runStop asks a live run's supervisor to stop its command, as its time
// limit would, and waits for the end it records: failed, "stopped by"
// the asker, rung to its launcher like any end, so job again and resume
// work once pickup shows it. Its launcher or the planner may ask.
func runStop(ctx context.Context, c *call) error {
	all := c.fs.Bool("all", false, "halt every claim and start, on every machine on the store")
	off := c.fs.Bool("off", false, "with --all: lift the halt")
	if err := c.parse(); err != nil {
		return err
	}
	if *all {
		if len(c.args) > 0 {
			return usagef("stop --all takes no run id")
		}
		return c.halt(ctx, !*off)
	}
	if *off {
		return usagef("stop: --off goes with --all")
	}
	if len(c.args) != 1 || !runs.ValidID(c.args[0]) {
		return usagef("stop: want one run id, such as R-abcdefgh, or --all")
	}
	if !runs.Supported {
		return usagef("stop needs a unix system")
	}
	if err := c.needActor("stop"); err != nil {
		return err
	}
	dir, err := c.besideStore("runs")
	if err != nil {
		return err
	}
	id := c.args[0]
	r, err := runs.Get(dir, id, time.Now())
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: run %s", ticket.ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	switch {
	case r.Launcher != c.actor && c.actor != "planner":
		return fmt.Errorf("%w: stop: run %s is %s's: only the bot that launched it, or the planner, stops it", ticket.ErrForbidden, id, dash(r.Launcher))
	case r.State == runs.Died:
		return fmt.Errorf("%w: stop: run %s died: nothing runs; pickup shows it", ticket.ErrStatusConflict, id)
	case r.Ended():
		return fmt.Errorf("%w: stop: run %s is %s already", ticket.ErrStatusConflict, id, r.State)
	}
	if _, err := runs.AskStop(dir, id, c.actor, time.Now().UTC().Round(time.Millisecond)); err != nil {
		return err
	}
	deadline := time.Now().Add(stopWait)
	for !r.Ended() && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		if r, err = runs.Get(dir, id, time.Now()); err != nil {
			return err
		}
	}
	if !r.Ended() {
		return fmt.Errorf("%w: stop: run %s asked to stop, still %s after %s: runs %s shows when it ends", ticket.ErrStatusConflict, id, r.State, stopWait, id)
	}
	return c.out(r, func(w io.Writer) {
		fmt.Fprintf(w, "%s %s: %s; its launcher's pickup shows it\n", id, r.State, firstLine(r.Error))
	})
}

// maxPickup is how many ended runs one pickup shows: after a reboot a bot
// can have a hundred, and it reads every line.
const maxPickup = 20

// pickupSends keeps the sends a look hasn't made lately. Each it keeps is
// marked as this run's, so a second pickup of the run shows it again and
// the next look sends none of them. A handout made before the run began
// was for the step before it, such as the review a kickback answered, so
// this run's send takes its place. A send whose mark the store fails to
// make or read is kept, and the first such error returned: a job sent
// twice is refused at its start, and one dropped is lost. Lines to the
// planner and cards to the chief of staff start no job, so they pass: two
// runs' cards on one ticket both go up, and one that keys its own card,
// such as designUp, dedupes it there.
func (co coord) pickupSends(runID string, since time.Time, sends []send, now time.Time) ([]send, error) {
	out := []send{}
	var failed error
	for _, s := range sends {
		id := ticketInLine.FindString(s.Line)
		if id == "" || s.Bot == "planner" || s.Bot == "chief-of-staff" || !runs.BotPattern.MatchString(s.Bot) {
			out = append(out, s)
			continue
		}
		mine := "pickup " + runID
		name := handoutName(id, s.Bot)
		took, err := co.tryHandout(id, s.Bot, mine, ticket.DefaultStall, now)
		if !took && err == nil {
			if m, rerr := co.s.read(handoutMarks, name); rerr == nil && m.at.Before(since) && co.moveStale(handoutMarks, name, m) {
				took, err = co.tryHandout(id, s.Bot, mine, ticket.DefaultStall, now)
			}
		}
		if !took && err == nil {
			var m mark
			if m, err = co.s.read(handoutMarks, name); errors.Is(err, fs.ErrNotExist) {
				err = nil
			}
			took = strings.TrimSpace(m.body) == mine
		}
		if took || err != nil {
			out = append(out, s)
		}
		failed = cmp.Or(failed, err)
	}
	return out, failed
}

// printRoute prints a routed job: one line for what happened, then each
// message to send, command to run and write made, and the reply only
// when the bot has to read it.
func printRoute(w io.Writer, rt routed, full bool) {
	for _, l := range routeLines(rt) {
		fmt.Fprintln(w, l)
	}
	if rt.Reply != "" && !full {
		fmt.Fprintf(w, "reply:\n%s\n", indent(rt.Reply))
	}
}

// printRun prints one run's result: a header line, then the reply and any
// error, set in as get sets a body, since they are data.
func printRun(w io.Writer, r runs.Record, now time.Time) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", r.ID, r.State)
	if r.Exit != nil {
		fmt.Fprintf(&b, " (exit %d)", *r.Exit)
	}
	if r.Ticket != "" {
		fmt.Fprintf(&b, " on %s", r.Ticket)
	}
	fmt.Fprintf(&b, " by %s", r.Launcher)
	if r.EndedAt != nil {
		fmt.Fprintf(&b, " after %s", ago(r.EndedAt.Sub(r.StartedAt)))
	} else if !r.Ended() {
		fmt.Fprintf(&b, " for %s", ago(now.Sub(r.StartedAt)))
	}
	if r.SessionID != "" {
		fmt.Fprintf(&b, ", session %s", r.SessionID)
	}
	if r.IsError {
		b.WriteString(", reply is an error")
	}
	fmt.Fprintln(w, b.String())
	if r.State == runs.Died {
		fmt.Fprintln(w, "error: its supervisor went away with no result (the machine restarted, or it was killed)")
	}
	if r.Reply != "" {
		fmt.Fprintf(w, "reply:\n%s\n", indent(r.Reply))
	}
	if r.Error != "" {
		fmt.Fprintf(w, "error:\n%s\n", indent(r.Error))
	}
}

// runRuns lists runs, and claude -p sessions in this repo that launch
// didn't start, without acking anything. With a run id, it prints that run
// in full.
func runRuns(ctx context.Context, c *call) error {
	all := c.fs.Bool("all", false, "include runs whose result was acked")
	launcher := c.fs.String("launcher", "", "only runs this bot launched")
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) > 1 {
		return usagef("runs: want at most one run id")
	}
	dir, err := c.besideStore("runs")
	if err != nil {
		return err
	}
	now := time.Now()
	if len(c.args) == 1 {
		id := c.args[0]
		if !runs.ValidID(id) {
			return usagef("runs: %q is not a run id, such as R-abcdefgh", id)
		}
		r, err := runs.Get(dir, id, now)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: run %s", ticket.ErrNotFound, id)
		}
		if err != nil {
			return err
		}
		return c.out(r, func(w io.Writer) { printRun(w, r, now) })
	}
	list, err := runs.List(dir, now, *all)
	if err != nil {
		return err
	}
	shown := []runs.Record{}
	for _, r := range list {
		if (*launcher == "" || r.Launcher == *launcher) && (*all || r.AckedAt == nil) {
			shown = append(shown, r)
		}
	}
	untracked, scanErr := c.untracked()
	res := map[string]any{"runs": shown, "untracked": untracked}
	if scanErr != nil {
		res["untracked_error"] = scanErr.Error()
	}
	return c.out(res, func(w io.Writer) {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "RUN\tTICKET\tLAUNCHER\tSTATE\tFOR\tACKED")
		for _, r := range shown {
			took, acked := ago(now.Sub(r.StartedAt)), "-"
			if r.EndedAt != nil {
				took = ago(r.EndedAt.Sub(r.StartedAt))
			}
			if r.Ended() {
				acked = "no"
				if r.AckedAt != nil {
					acked = "by " + r.AckedBy
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, dash(r.Ticket), dash(r.Launcher), r.State, took, acked)
		}
		tw.Flush()
		for _, s := range untracked {
			fmt.Fprintf(w, "untracked: pid %d in %s: a claude -p session started outside maestro-ticket launch\n", s.PID, s.Dir)
		}
	})
}

// untracked finds claude -p sessions in a worktree of this repo that
// launch didn't start. Outside a git repo there is nothing to look in.
func (c *call) untracked() ([]runs.Session, error) {
	if !runs.CanScan {
		_, err := runs.Untracked(nil)
		return []runs.Session{}, err
	}
	out, err := c.git("worktree", "list", "--porcelain")
	if err != nil {
		return []runs.Session{}, nil
	}
	var roots []string
	for _, l := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			roots = append(roots, p)
		}
	}
	s, err := runs.Untracked(roots)
	if s == nil {
		s = []runs.Session{}
	}
	return s, err
}

// runsCheck is doctor's view of the runs. First it rings for runs whose
// supervisor went away, as pickup does. It warns, without failing doctor,
// while a claude -p session in this repo runs outside launch, or a run's
// result has waited over unackedAfter for its owner's ack: neither
// harms the store, and a doctor that failed would stop an install.
func (c *call) runsCheck(ctx context.Context, store string) ticket.Check {
	dir := filepath.Join(filepath.Dir(store), "runs")
	c.ringOrphans(ctx, dir, "")
	now := time.Now()
	list, err := runs.List(dir, now, false)
	if err != nil {
		return ticket.Check{Name: "runs", OK: false, Detail: err.Error()}
	}
	live, waiting := 0, []string{}
	for _, r := range list {
		switch {
		case !r.Ended():
			live++
		case r.State == runs.Died || r.EndedAt == nil || now.Sub(*r.EndedAt) > unackedAfter:
			waiting = append(waiting, fmt.Sprintf("%s (%s, %s)", r.ID, dash(r.Owner()), r.State))
		}
	}
	untracked, scanErr := c.untracked()
	var bad []string
	if len(untracked) > 0 {
		s := make([]string, len(untracked))
		for i, u := range untracked {
			s[i] = fmt.Sprintf("pid %d in %s", u.PID, u.Dir)
		}
		bad = append(bad, fmt.Sprintf("%d claude -p sessions started outside maestro-ticket launch: %s", len(untracked), strings.Join(s, ", ")))
	}
	if len(waiting) > 0 {
		bad = append(bad, fmt.Sprintf("%d ended runs not acked by the bot they were left to (died, or ended over %s ago): %s", len(waiting), short(unackedAfter), strings.Join(waiting, ", ")))
	}
	if len(bad) > 0 {
		return ticket.Check{Name: "runs", OK: true, Warn: true, Detail: strings.Join(bad, "; ")}
	}
	detail := fmt.Sprintf("%d running, none waiting for an ack", live)
	if scanErr != nil {
		detail += "; can't look for untracked sessions: " + scanErr.Error()
	}
	return ticket.Check{Name: "runs", OK: true, Detail: detail}
}

// runNotify sets, clears, tests or shows the webhook launch rings when a
// bot's run ends. It never prints the URL, which often holds a secret.
func runNotify(ctx context.Context, c *call) error {
	urlFile := c.fs.String("url-file", "", "read the webhook URL from this file, or - for stdin")
	keyFile := c.fs.String("key-file", "", "read the key the webhook wants as Authorization: Bearer from this file, or - for stdin")
	clr := c.fs.Bool("clear", false, "remove the bot's webhook")
	test := c.fs.Bool("test", false, "ring the webhook once now")
	if err := c.parse(); err != nil {
		return err
	}
	dir, err := c.besideStore("notify")
	if err != nil {
		return err
	}
	if len(c.args) == 0 {
		if *urlFile != "" || *keyFile != "" || *clr || *test {
			return usagef("notify: name the bot")
		}
		bots, err := runs.Webhooks(dir)
		if err != nil {
			return err
		}
		if bots == nil {
			bots = []string{}
		}
		return c.out(map[string]any{"bots": bots}, func(w io.Writer) {
			if len(bots) == 0 {
				fmt.Fprintln(w, "no bot has a webhook")
			}
			for _, b := range bots {
				fmt.Fprintln(w, b)
			}
		})
	}
	if len(c.args) > 1 {
		return usagef("notify: want one bot id")
	}
	bot := c.args[0]
	if !runs.BotPattern.MatchString(bot) {
		return usagef("notify: bot id %q: use lowercase letters, digits and single hyphens", bot)
	}
	switch {
	case *clr && (*urlFile != "" || *keyFile != ""):
		return usagef("notify: use --url-file and --key-file, or --clear, not both")
	case *urlFile == "-" && *keyFile == "-":
		return usagef("notify: only one of --url-file and --key-file can read stdin")
	}
	if *urlFile != "" {
		u, err := c.readBody("", *urlFile)
		if err != nil {
			return err
		}
		// An empty file is a mistake, not a clear: SetWebhook would drop
		// the webhook and its key.
		if u = strings.TrimSpace(u); u == "" {
			return usagef("notify: the URL file is empty; --clear removes a webhook")
		}
		if err := runs.SetWebhook(dir, bot, u); err != nil {
			return usagef("notify: %v", err)
		}
	}
	if *keyFile != "" {
		if h, err := runs.Webhook(dir, bot); err != nil || h.URL == "" {
			return usagef("notify: give %s a webhook with --url-file before its key", bot)
		}
		k, err := c.readBody("", *keyFile)
		if err != nil {
			return err
		}
		if k = strings.TrimSpace(k); k == "" {
			return usagef("notify: the key file is empty")
		}
		if err := runs.SetKey(dir, bot, k); err != nil {
			return usagef("notify: %v", err)
		}
	}
	if *clr {
		if err := runs.SetWebhook(dir, bot, ""); err != nil {
			return err
		}
	}
	h, err := runs.Webhook(dir, bot)
	if err != nil {
		return err
	}
	u := h.URL
	if *test {
		if u == "" {
			return fmt.Errorf("%w: notify: %s has no webhook", ticket.ErrNotFound, bot)
		}
		// Straight to the webhook: a test ring skips the bot's gate.
		n := runs.Notification{Text: "Maestro test ring: launch rings here when a run " + bot + " launched ends.", Bot: bot, Launcher: bot, State: "test"}
		if err := runs.Ring(ctx, h, n); err != nil {
			return fmt.Errorf("%w: notify: %v", ticket.ErrInvalid, err)
		}
	}
	res := map[string]any{"bot": bot, "webhook": u != "", "key": h.Key != "", "rang": *test}
	if u != "" {
		res["host"] = runs.Host(u)
	}
	return c.out(res, func(w io.Writer) {
		key := ""
		if h.Key != "" {
			key = " with its key"
		}
		switch {
		case u == "":
			fmt.Fprintf(w, "%s: no webhook; it learns a run ended only when it runs maestro-ticket pickup\n", bot)
		case *test:
			fmt.Fprintf(w, "%s: rang %s%s\n", bot, runs.Host(u), key)
		default:
			fmt.Fprintf(w, "%s: rings %s%s\n", bot, runs.Host(u), key)
		}
	})
}
