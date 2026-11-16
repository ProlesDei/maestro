package cli

import (
	"bytes"
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
	path, source, err := c.storePath()
	if err != nil {
		return "", err
	}
	if source == "git common dir" {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: no ticket store at %s: run maestro-ticket in the checkout Maestro is installed in, or a worktree of it", ticket.ErrInvalid, path)
		}
	}
	if err := envcheck.Err(c.envChecks(path)); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), name), nil
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
	r, err := c.startRun(runs.Record{Ticket: ticketID, Limit: short(*limit), Command: command}, stdin, rings)
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

// startRun records a run for the actor and hands it to a detached
// supervisor, and returns once its command runs. launch, job and resume
// all start runs here.
func (c *call) startRun(rec runs.Record, stdin []byte, rings []time.Duration) (runs.Record, error) {
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
		if err = sup.Start(); err == nil {
			sup.Process.Release()
		}
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
	return runs.Supervise(ctx, dir, c.args[0], runs.Options{
		Env:   os.Environ(),
		Rings: rings,
		Hook:  func(bot string) (runs.Hook, error) { return runs.Webhook(notify, bot) },
		// The supervisor stands in for log-run: it lives as long as the
		// run, so alerts reads its pid as the run's.
		OnStart: func(r runs.Record) int64 {
			c.actor = r.Launcher
			return c.startLaunch(ctx, runTicket(r))
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
			if out, reset := outcomeOf(r); out == outUsageLimit {
				return limitLifts(r, reset).Add(30 * time.Second)
			}
			return time.Time{}
		},
	})
}

// endRun does what log-run did once a run ended: record its end and its
// cost, and, for a run that gave no result, free the claim its bot held
// on its ticket, unless another run of that bot on that ticket still
// goes. Neither may hold up the ring, so failures only warn on stderr.
func (c *call) endRun(ctx context.Context, dir string, r runs.Record) {
	dropActive(dir, r)
	warn := func(err error) {
		if err != nil {
			fmt.Fprintf(c.env.Stderr, "maestro-ticket: run %s: %v\n", r.ID, err)
		}
	}
	id := runTicket(r)
	s, err := c.open(ctx)
	if err != nil {
		warn(err)
		return
	}
	defer s.Close()
	out, _ := runs.ReadTail(filepath.Join(dir, r.ID+".out"), runs.OutputWindow)
	if len(out) == runs.OutputWindow {
		if i := bytes.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:] // a cut first line isn't JSON
		}
	}
	reply, err := lastResult(out)
	outcome := ticket.LaunchResult
	switch {
	case r.State == runs.Died:
		outcome = ticket.LaunchDied
	case err != nil:
		outcome = ticket.LaunchNoResult
	}
	c.endLaunch(ctx, s, r.LaunchID, outcome, reply.SessionID)
	if err == nil {
		warn(logRun(ctx, s, reply, id, r.Launcher, c.env.Stderr))
	}
	if r.State == runs.Done || (r.State == runs.Failed && r.SessionID != "") {
		return
	}
	_, err = freeRunClaim(ctx, s, logActor(r.Launcher), id, logActor(r.Launcher))
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
// frees its ticket as the supervisor would have, and rings. pickup and
// doctor call it, whoever runs them, so the first look after a restart
// wakes every bot that lost a run. A ring that fails is tried again on a
// later look, a few times.
func (c *call) ringOrphans(ctx context.Context, dir string) {
	ids, err := runs.Orphans(dir, time.Now())
	if err != nil || len(ids) == 0 {
		return
	}
	notify := filepath.Join(filepath.Dir(dir), "notify")
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
			defer release()
			if r.State == runs.Died && r.OrphanTries == 0 {
				runs.KillRun(r.ID)
				runs.Save(dir, r)
				c.endRun(ctx, dir, r)
			}
			h, err := runs.Webhook(notify, r.Owner())
			if err != nil || h.URL == "" {
				msg := ""
				if err != nil {
					msg = err.Error()
				}
				// Nothing to ring: write the record only when this
				// changes it, not on every look.
				if r.OrphanTries < 1 || r.RingError != msg {
					r.RingError, r.OrphanTries = msg, max(r.OrphanTries, 1)
					runs.Save(dir, r)
				}
				return
			}
			r.OrphanTries++
			r.Rings++
			r.RingError = ""
			if err := runs.Ring(ctx, h, runs.Notice(r)); err != nil {
				r.RingError = err.Error()
			} else {
				r.Rang = true
			}
			runs.Save(dir, r)
		}()
	}
	wg.Wait()
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
	c.ringOrphans(ctx, dir)
	now := time.Now().UTC().Round(time.Millisecond)
	list, err := runs.List(dir, now, false)
	if err != nil {
		return err
	}
	ended, running := []runs.Record{}, []runs.Record{}
	for _, r := range list {
		switch {
		case r.Owner() != c.actor:
		case !r.Ended():
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
				if s != nil {
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
	pause, paused := readPause(filepath.Dir(dir), now)
	waiting, shown := []string{}, []runs.Record{}
	more := 0
	for _, r := range ended {
		rt, ok := routes[r.ID]
		switch {
		// The run that set the pause shows whole, so its bot tells the
		// planner; the rest of those it stopped wait.
		case ok && paused && rt.Outcome == outUsageLimit && !slices.ContainsFunc(rt.Wrote, func(w string) bool { return strings.HasPrefix(w, "paused every job") }):
			waiting = append(waiting, r.ID)
		case len(shown) == maxPickup:
			more++ // the next pickup, after these are acked, shows them
		default:
			shown = append(shown, r)
		}
	}
	ended = shown
	routeList := []routed{}
	for _, r := range ended {
		if rt, ok := routes[r.ID]; ok {
			// A job a look handed out lately goes out once: pickup drops
			// its own line for it, and holds the rest from the next look.
			rt.Send = pickupSends(filepath.Join(filepath.Dir(dir), "handouts"), r.ID, r.StartedAt, rt.Send, now)
			routes[r.ID] = rt
			routeList = append(routeList, rt)
		}
	}
	return c.out(map[string]any{"ended": ended, "running": running, "routes": routeList, "waiting": waiting}, func(w io.Writer) {
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
				fmt.Fprintf(w, "%d runs wait for the usage limit to lift at %s: nothing to do till then; they ring you as it lifts\n", len(waiting), pause.Until.Format(time.RFC3339))
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
				s = append(s, fmt.Sprintf("%s (%s, %s)", r.ID, dash(r.Ticket), ago(now.Sub(r.StartedAt))))
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
	return c.out(map[string]any{"acks": res}, func(w io.Writer) {
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

// stopWait is how long stop waits for the supervisor to record the end:
// its look for the ask, then the kill grace.
const stopWait = runs.KillGrace + 10*time.Second

// runStop asks a live run's supervisor to stop its command, as its time
// limit would, and waits for the end it records: failed, "stopped by"
// the asker, rung to its launcher like any end, so job again and resume
// work once pickup shows it. Its launcher or the planner may ask.
func runStop(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) != 1 || !runs.ValidID(c.args[0]) {
		return usagef("stop: want one run id, such as R-abcdefgh")
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
// this run's send takes its place.
func pickupSends(dir, runID string, since time.Time, sends []send, now time.Time) []send {
	out := []send{}
	for _, s := range sends {
		id := ticketInLine.FindString(s.Line)
		if id == "" || s.Bot == "planner" || !runs.BotPattern.MatchString(s.Bot) {
			out = append(out, s)
			continue
		}
		mine := "pickup " + runID
		if claimHandout(dir, id, s.Bot, mine, ticket.DefaultStall, now) {
			out = append(out, s)
			continue
		}
		p := handoutPath(dir, id, s.Bot)
		if fi, err := os.Stat(p); err == nil && fi.ModTime().Before(since) && moveStale(p, fi) &&
			claimHandout(dir, id, s.Bot, mine, ticket.DefaultStall, now) {
			out = append(out, s)
			continue
		}
		if b, _ := os.ReadFile(p); strings.TrimSpace(string(b)) == mine {
			out = append(out, s)
		}
	}
	return out
}

// printRoute prints a routed job: one line for what happened, then each
// message to send, command to run and write made, and the reply only
// when the bot has to read it.
func printRoute(w io.Writer, rt routed, full bool) {
	on, st := "", ""
	if rt.Ticket != "" {
		on = " on " + rt.Ticket
	}
	if rt.Status != "" {
		st = " (" + rt.Status + ")"
	}
	reset := ""
	if rt.Reset != "" {
		reset = ", resets " + rt.Reset
	}
	fmt.Fprintf(w, "%s %s %s%s%s%s: %s\n", rt.Run, rt.Job, rt.Outcome, on, st, reset, dash(rt.First))
	if rt.Mismatch != "" {
		fmt.Fprintf(w, "mismatch: want %s\n", rt.Mismatch)
	}
	for _, x := range rt.Wrote {
		fmt.Fprintf(w, "wrote: %s\n", x)
	}
	for _, x := range rt.Do {
		fmt.Fprintf(w, "do: %s\n", x)
	}
	for _, x := range rt.Send {
		fmt.Fprintln(w, x.String())
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
	c.ringOrphans(ctx, dir)
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
		n := runs.Notification{Text: "Maestro test ring: launch rings here when a run " + bot + " launched ends.", Launcher: bot, State: "test"}
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
