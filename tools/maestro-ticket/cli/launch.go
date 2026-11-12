package cli

import (
	"context"
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
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// unpickedAfter is how long an ended run may wait for its launcher's
// pickup before doctor flags it.
const unpickedAfter = 15 * time.Minute

// keepPicked is how long a run's files stay after its result was picked
// up.
const keepPicked = 7 * 24 * time.Hour

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
	ringsFlag := c.fs.String("rings", ringsText(runs.DefaultRings), "when to ring your webhook after the run ends, while you haven't picked it up")
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
	dir, err := c.besideStore("runs")
	if err != nil {
		return err
	}
	store, _, err := c.storePath()
	if err != nil {
		return err
	}
	stdin, err := c.readStdin()
	if err != nil {
		return err
	}
	r, err := runs.Create(dir, runs.Record{
		Launcher: c.actor, Ticket: ticketID, Limit: short(*limit),
		Command: command, Dir: c.env.Dir, StartedAt: time.Now().UTC().Round(time.Millisecond),
	}, stdin)
	if err != nil {
		return err
	}
	// The supervisor outlives this command, in a session of its own: a
	// harness ends the command's session when it returns.
	exe, err := os.Executable()
	if err == nil {
		sup := exec.Command(exe, "supervise", r.ID, "--store", store, "--rings", ringsText(rings))
		sup.Dir = c.env.Dir
		sup.SysProcAttr = runs.Detach()
		if err = sup.Start(); err == nil {
			sup.Process.Release()
		}
	}
	if err != nil {
		r.State, r.Error = runs.Failed, "start the supervisor: "+err.Error()
		runs.Save(dir, r)
		runs.Pick(dir, r.ID, c.actor, time.Now())
		return fmt.Errorf("%w: launch: %s", ticket.ErrInvalid, r.Error)
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
		if ok, _ := runs.Pick(dir, r.ID, c.actor, time.Now()); ok {
			return fmt.Errorf("%w: launch %s: %s", ticket.ErrInvalid, r.ID, r.Error)
		}
	}
	return c.out(r, func(w io.Writer) {
		on := ""
		if r.Ticket != "" {
			on = " on " + r.Ticket
		}
		fmt.Fprintf(w, "%s %s%s, limit %s. maestro-ticket pickup reports it once it ends.\n", r.ID, r.State, on, r.Limit)
	})
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
		Env:       os.Environ(),
		Rings:     rings,
		NotifyURL: func(bot string) (string, error) { return runs.Webhook(notify, bot) },
	})
}

// runPickup reports, once, each run the actor launched that has ended,
// and lists the actor's runs still going. Bots run it before they end a
// turn.
func runPickup(ctx context.Context, c *call) error {
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
	now := time.Now().UTC().Round(time.Millisecond)
	list, err := runs.List(dir, now, false)
	if err != nil {
		return err
	}
	ended, running := []runs.Record{}, []runs.Record{}
	for _, r := range list {
		switch {
		case r.Launcher != c.actor:
		case !r.Ended():
			running = append(running, r)
		case r.PickedAt == nil:
			ok, err := runs.Pick(dir, r.ID, c.actor, now)
			if err != nil {
				return err
			}
			if ok {
				r.PickedAt, r.PickedBy = &now, c.actor
				ended = append(ended, r)
			}
		}
	}
	runs.Prune(dir, keepPicked, now)
	return c.out(map[string]any{"ended": ended, "running": running}, func(w io.Writer) {
		for _, r := range ended {
			printRun(w, r, now)
		}
		switch {
		case len(ended) == 0 && len(running) == 0:
			fmt.Fprintln(w, "no run of yours ended or running")
		case len(running) == 0:
		default:
			if len(ended) == 0 {
				fmt.Fprint(w, "no run of yours ended; ")
			}
			s := make([]string, len(running))
			for i, r := range running {
				s[i] = fmt.Sprintf("%s (%s, %s)", r.ID, dash(r.Ticket), ago(now.Sub(r.StartedAt)))
			}
			fmt.Fprintf(w, "running: %s\n", strings.Join(s, ", "))
		}
	})
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
// didn't start, without picking anything up. With a run id, it prints that
// run in full.
func runRuns(ctx context.Context, c *call) error {
	all := c.fs.Bool("all", false, "include runs whose result was picked up")
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
		if (*launcher == "" || r.Launcher == *launcher) && (*all || r.PickedAt == nil) {
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
		fmt.Fprintln(tw, "RUN\tTICKET\tLAUNCHER\tSTATE\tFOR\tPICKED UP")
		for _, r := range shown {
			took, picked := ago(now.Sub(r.StartedAt)), "-"
			if r.EndedAt != nil {
				took = ago(r.EndedAt.Sub(r.StartedAt))
			}
			if r.Ended() {
				picked = "no"
				if r.PickedAt != nil {
					picked = "by " + r.PickedBy
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, dash(r.Ticket), r.Launcher, r.State, took, picked)
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

// runsCheck is doctor's view of the runs: it fails while a claude -p
// session in this repo runs outside launch, or a run's result has waited
// over unpickedAfter for its launcher.
func (c *call) runsCheck(store string) ticket.Check {
	now := time.Now()
	list, err := runs.List(filepath.Join(filepath.Dir(store), "runs"), now, false)
	if err != nil {
		return ticket.Check{Name: "runs", OK: false, Detail: err.Error()}
	}
	live, waiting := 0, []string{}
	for _, r := range list {
		switch {
		case !r.Ended():
			live++
		case r.PickedAt == nil && (r.EndedAt == nil || now.Sub(*r.EndedAt) > unpickedAfter):
			waiting = append(waiting, fmt.Sprintf("%s (%s, %s)", r.ID, dash(r.Launcher), r.State))
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
		bad = append(bad, fmt.Sprintf("%d ended runs waiting over %s for their launcher's pickup: %s", len(waiting), short(unpickedAfter), strings.Join(waiting, ", ")))
	}
	if len(bad) > 0 {
		return ticket.Check{Name: "runs", OK: false, Detail: strings.Join(bad, "; ")}
	}
	detail := fmt.Sprintf("%d running, none waiting for pickup", live)
	if scanErr != nil {
		detail += "; can't look for untracked sessions: " + scanErr.Error()
	}
	return ticket.Check{Name: "runs", OK: true, Detail: detail}
}

// runNotify sets, clears, tests or shows the webhook launch rings when a
// bot's run ends. It never prints the URL, which often holds a secret.
func runNotify(ctx context.Context, c *call) error {
	urlFile := c.fs.String("url-file", "", "read the webhook URL from this file, or - for stdin")
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
		if *urlFile != "" || *clr || *test {
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
	if *urlFile != "" && *clr {
		return usagef("notify: use --url-file or --clear, not both")
	}
	if *urlFile != "" {
		u, err := c.readBody("", *urlFile)
		if err != nil {
			return err
		}
		if err := runs.SetWebhook(dir, bot, strings.TrimSpace(u)); err != nil {
			return usagef("notify: %v", err)
		}
	}
	if *clr {
		if err := runs.SetWebhook(dir, bot, ""); err != nil {
			return err
		}
	}
	u, err := runs.Webhook(dir, bot)
	if err != nil {
		return err
	}
	if *test {
		if u == "" {
			return fmt.Errorf("%w: notify: %s has no webhook", ticket.ErrNotFound, bot)
		}
		n := runs.Notification{Text: "Maestro test ring: launch rings here when a run " + bot + " launched ends.", Launcher: bot, State: "test"}
		if err := runs.Ring(ctx, u, n); err != nil {
			return fmt.Errorf("%w: notify: %v", ticket.ErrInvalid, err)
		}
	}
	res := map[string]any{"bot": bot, "webhook": u != "", "rang": *test}
	if u != "" {
		res["host"] = runs.Host(u)
	}
	return c.out(res, func(w io.Writer) {
		switch {
		case u == "":
			fmt.Fprintf(w, "%s: no webhook; it learns a run ended only when it runs maestro-ticket pickup\n", bot)
		case *test:
			fmt.Fprintf(w, "%s: rang %s\n", bot, runs.Host(u))
		default:
			fmt.Fprintf(w, "%s: rings %s\n", bot, runs.Host(u))
		}
	})
}
