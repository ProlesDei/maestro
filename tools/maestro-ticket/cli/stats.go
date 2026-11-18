package cli

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// statsHelp is what stats and stats --help print. The top usage text
// leaves stats out: no bot reads it.
const statsHelp = `maestro-ticket stats: run data from stats.db beside the store, for people and
Claude sessions, never a bot's own shell. Nothing here sends it anywhere.

  stats case <ticket|run>  each run of a ticket, or one run, oldest first: a
                           line each with its job, model, why, outcome, time,
                           spend, verdict and findings, and the call it hung on
  stats health             on or off and why, ended runs with no row, disk
                           free, schema
  stats costs [--by model|job|why|lean] [--since DATE|DURATION]
                           each run's own spend, by group; a resume counts
                           only what it added to its session
  stats bundle --case T [--cost] [--out FILE|DIR]
                           write a ticket's runs to a file for maintainers:
                           labels for ids, offsets for times, no text; --cost
                           adds spend, in bundles/ beside the store unless
                           --out says. For a person only. It sends nothing
  stats on|off [--maestro-sha S]
                           start or stop writes for this store, as install.sh
                           apply does; --maestro-sha S alone records the sha
                           and leaves writes as they are. A stats: line in
                           install.yaml sets them again at each apply
`

// statsViews are stats' views and bundle, by name.
var statsViews = map[string]func(context.Context, *call) error{
	"case":   runStatsCase,
	"health": runStatsHealth,
	"costs":  runStatsCosts,
	"bundle": runStatsBundle,
}

// runStats lists the views, runs one, or turns stats on or off.
func runStats(ctx context.Context, c *call) error {
	if len(c.args) == 0 || slices.Contains([]string{"help", "-h", "-help", "--help"}, c.args[0]) {
		fmt.Fprint(c.env.Stdout, statsHelp)
		return nil
	}
	// A view's name may come after a flag, as in stats --json case T-1.
	// No flag's value can name a view.
	for i, a := range c.args {
		if a == "--" {
			break
		}
		if view := statsViews[a]; view != nil {
			c.args = slices.Delete(slices.Clone(c.args), i, i+1)
			return view(ctx, c)
		}
	}
	return runStatsSwitch(c)
}

// runStatsSwitch turns the local run stats on or off for this store, as
// install.sh apply does from install.yaml's stats: key, and records the
// maestro-sha apply pinned; with no stats: key, apply records only the
// sha, so a person's stats off stays. It prints nothing on success. Off
// stops every write and leaves stats.db as it is.
func runStatsSwitch(c *call) error {
	sha := c.fs.String("maestro-sha", "", "the Maestro commit install.yaml pins, recorded with each run")
	if err := c.parse(); err != nil {
		return err
	}
	shaOnly := len(c.args) == 0 && *sha != ""
	if !shaOnly && (len(c.args) != 1 || (c.args[0] != "on" && c.args[0] != "off")) {
		return usagef("stats: want on or off, bundle, or a view: case, health, costs (stats --help lists them)")
	}
	if *sha != "" && !stats.ValidSha(*sha) {
		return usagef("stats: --maestro-sha %q: want a commit sha of 7 to 40 lowercase hex", *sha)
	}
	marker, err := c.besideStore(stats.OffFile)
	if err != nil {
		return err
	}
	dir := filepath.Dir(marker)
	if *sha != "" {
		if err := stats.SetSha(dir, *sha); err != nil {
			return err
		}
	}
	if shaOnly {
		return nil
	}
	return stats.SetOff(dir, c.args[0] == "off")
}

// refuseBot keeps a view from a bot in its own shell, where whatever it
// reads reaches Grok (decision 24). A Claude session a bot started runs
// with MAESTRO_RUN_ID set, and may read them.
func (c *call) refuseBot(view string) error {
	if c.env.Getenv(runs.RunIDEnv) != "" {
		return nil
	}
	if bot := c.botActor(); bot != "" {
		return fmt.Errorf("%w: stats %s: %s is a bot, and stats are for people and Claude sessions", ticket.ErrForbidden, view, bot)
	}
	return nil
}

// botActor is the bot --actor or MAESTRO_TICKET_ACTOR names, or "".
func (c *call) botActor() string {
	if b := c.botActors(); len(b) > 0 {
		return b[0]
	}
	return ""
}

// botActors is every bot --actor and MAESTRO_TICKET_ACTOR name.
func (c *call) botActors() []string {
	var out []string
	for _, a := range []string{c.actor, c.env.Getenv("MAESTRO_TICKET_ACTOR")} {
		if slices.Contains(botProfiles, a) {
			out = append(out, a)
		}
	}
	return out
}

// refuseBundle keeps stats bundle to a person: not a bot, and not any
// run, a Claude session a bot started included. A person reads the file
// and decides whether it leaves (decision 24).
func (c *call) refuseBundle() error {
	if c.env.Getenv(runs.RunIDEnv) != "" {
		return fmt.Errorf("%w: stats bundle: %s is set, and a bundle is for a person to write and read, never a run", ticket.ErrForbidden, runs.RunIDEnv)
	}
	if bot := c.botActor(); bot != "" {
		return fmt.Errorf("%w: stats bundle: %s is a bot, and a bundle is for a person to write and read", ticket.ErrForbidden, bot)
	}
	return nil
}

// offWhy says in words why stats are off, from stats.Off's reason.
func offWhy(reason string) string {
	switch reason {
	case "marker":
		return stats.OffFile + " sits beside the store"
	case "env":
		return stats.OffEnv + "=off"
	}
	return "the store's filesystem failed its checks"
}

// statsRead reads the runs f picks for a view.
// note is the one line to print instead when there is nothing to read:
// stats are off, or no run has ended with them on.
func (c *call) statsRead(ctx context.Context, f stats.Filter) (facts []stats.Fact, note string, err error) {
	note, err = c.withStats(func(db *sql.DB) (err error) {
		facts, err = stats.Read(ctx, db, f)
		return err
	})
	return facts, note, err
}

// withStats runs read on stats.db, opened read-only, or returns the one
// line to print instead.
func (c *call) withStats(read func(*sql.DB) error) (note string, err error) {
	path, err := c.besideStore(stats.DBName)
	if err != nil {
		return "", err
	}
	if why := stats.Off(filepath.Dir(path), c.env.Getenv, c.statsUnsafe()); why != "" {
		return "stats are off (" + offWhy(why) + "); stats health says more", nil
	}
	const none = "no stats yet: a run's supervisor starts stats.db at the first run's end"
	if fi, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) || (err == nil && fi.Size() == 0) {
		return none, nil
	}
	db, err := stats.OpenRead(path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	err = read(db)
	switch {
	case errors.Is(err, stats.ErrNoSchema):
		return none, nil
	case errors.Is(err, stats.ErrNewer):
		return "", fmt.Errorf("%w: a newer maestro-ticket wrote it; rebuild this one", err)
	case err != nil:
		return "", fmt.Errorf("read stats.db: %w", err)
	}
	return "", nil
}

// printNote prints a view's one line, as {"note": ...} with --json.
func (c *call) printNote(note string) error {
	return c.out(map[string]string{"note": note}, func(w io.Writer) { fmt.Fprintln(w, note) })
}

// runSpend is a run's own spend in dollars and tokens, and whether the
// dollars are known: a run with no model rows, or one whose dollars
// can't be worked out, has none.
func runSpend(f stats.Fact) (usd float64, tokens int64, ok bool) {
	for _, s := range f.Spend {
		if s.USD == nil {
			return 0, 0, false
		}
		usd += *s.USD
		if s.Tokens != nil {
			tokens += *s.Tokens
		}
	}
	return usd, tokens, len(f.Spend) > 0
}

// runStatsCase prints each run of a ticket, or one run.
func runStatsCase(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.refuseBot("case"); err != nil {
		return err
	}
	if len(c.args) != 1 {
		return usagef("stats case: want one ticket id (T-12) or run id (R-...)")
	}
	var f stats.Filter
	if runs.ValidID(c.args[0]) {
		f.Run = c.args[0]
	} else {
		id, err := ticket.ParseID(c.args[0])
		if err != nil {
			return usagef("stats case: %q is no ticket id (T-12) or run id (R-...)", c.args[0])
		}
		f.Ticket = int64(id)
	}
	facts, note, err := c.statsRead(ctx, f)
	if err != nil {
		return err
	}
	if note != "" {
		return c.printNote(note)
	}
	if facts == nil {
		facts = []stats.Fact{}
	}
	return c.out(map[string]any{"runs": facts}, func(w io.Writer) { printCase(w, c.args[0], facts) })
}

func printCase(w io.Writer, name string, facts []stats.Fact) {
	if len(facts) == 0 {
		fmt.Fprintf(w, "%s: no runs in stats.db\n", name)
		return
	}
	var usd float64
	unknown := 0
	for _, f := range facts {
		if u, _, ok := runSpend(f); ok {
			usd += u
		} else {
			unknown++
		}
	}
	word := "runs"
	if len(facts) == 1 {
		word = "run"
	}
	head := fmt.Sprintf("%s: %d %s, $%.2f", name, len(facts), word, usd)
	if unknown > 0 {
		head += fmt.Sprintf(" (%d with no delta)", unknown)
	}
	fmt.Fprintln(w, head)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, f := range facts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", f.Run, f.StartedAt.Local().Format("Jan 2 15:04"),
			dash(f.Job), dash(strings.TrimSpace(f.Model+" "+f.Effort)), dash(f.Why), dash(f.Outcome),
			msText(f.WallMs), caseUSD(f), caseMore(f))
	}
	tw.Flush()
}

func msText(ms *int64) string {
	if ms == nil {
		return "-"
	}
	return ago(time.Duration(*ms) * time.Millisecond)
}

func caseUSD(f stats.Fact) string {
	if u, _, ok := runSpend(f); ok {
		return fmt.Sprintf("$%.2f", u)
	}
	return "$?"
}

// caseMore is a run's line past its fixed columns: only what it has.
func caseMore(f stats.Fact) string {
	var b []string
	add := func(format string, a ...any) { b = append(b, fmt.Sprintf(format, a...)) }
	if f.Reason != "" {
		add("reason %s", f.Reason)
	}
	if f.Lean {
		add("lean")
	}
	switch {
	case f.ResumeOf != "" && f.Nudge != "" && f.Nudge != "none":
		add("resume of %s (%s)", f.ResumeOf, f.Nudge)
	case f.ResumeOf != "":
		add("resume of %s", f.ResumeOf)
	}
	if f.AgainOf != "" {
		add("again of %s", f.AgainOf)
	}
	if f.Tries != nil && *f.Tries > 0 {
		add("tries %d", *f.Tries)
	}
	if f.Turns != nil {
		add("turns %d", *f.Turns)
	}
	if f.ToolCalls != nil {
		add("tools %d, %d failed", *f.ToolCalls, derefOr(f.ToolFails))
	}
	if f.Verdict != "" {
		add("verdict %s", f.Verdict)
	}
	var finds []string
	for _, x := range f.Findings {
		switch x.Kind {
		case "finding":
			finds = append(finds, strings.Join(slices.DeleteFunc([]string{dash(x.Fid), x.Sev, x.Prior}, func(s string) bool { return s == "" }), " "))
		case "dropped":
			finds = append(finds, dash(x.Fid)+" dropped")
		default:
			finds = append(finds, fmt.Sprintf("%d %s", x.N, x.Kind))
		}
	}
	if len(finds) > 0 {
		add("%s", strings.Join(finds, ", "))
	}
	if f.OpenCode != "" {
		add("open %s %s", f.OpenCode, msText(f.OpenMs))
	}
	if len(f.Tail) > 0 {
		var t []string
		for _, x := range f.Tail {
			s := dash(x.Code)
			if x.Fail {
				s += "!"
			}
			t = append(t, s)
		}
		add("tail %s", strings.Join(t, " "))
	}
	if f.Stream != "" && f.Stream != "ok" && f.Stream != "off" {
		add("stream %s", f.Stream)
	}
	return strings.Join(b, "  ")
}

func derefOr(n *int64) int64 {
	if n == nil {
		return 0
	}
	return *n
}

// statsCostKeys name each run's group for stats costs, but for model,
// which groups each run's spend per model.
var statsCostKeys = map[string]func(stats.Fact) string{
	"model": nil,
	"job":   func(f stats.Fact) string { return groupOrNone(f.Job) },
	"why":   func(f stats.Fact) string { return groupOrNone(f.Why) },
	"lean": func(f stats.Fact) string {
		if f.Lean {
			return "lean"
		}
		return "not lean"
	},
}

func groupOrNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// runStatsCosts groups each run's own spend from stats.db, as costs
// groups the logged runs, with the runs whose spend can't be worked out
// counted apart.
func runStatsCosts(ctx context.Context, c *call) error {
	by := c.fs.String("by", "model", "group by model, job, why or lean")
	since := c.fs.String("since", "", "only runs started on or after this date (2006-01-02), or within this long (such as 72h)")
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.refuseBot("costs"); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	key, ok := statsCostKeys[*by]
	if !ok {
		return usagef("stats costs: --by wants model, job, why or lean, not %q", *by)
	}
	var f stats.Filter
	var err error
	if f.Since, err = c.parseSince(*since); err != nil {
		return err
	}
	facts, note, err := c.statsRead(ctx, f)
	if err != nil {
		return err
	}
	if note != "" {
		return c.printNote(note)
	}
	// Each run is a RunRow whose Actor holds its group, so costRows
	// groups and sums it as costs does; per model, a run is a row per
	// model it used, and the total row counts each run once.
	var groups, perRun []ticket.RunRow
	missing := 0
	var tokens int64
	for _, x := range facts {
		usd, tok, ok := runSpend(x)
		if !ok {
			missing++
			continue
		}
		tokens += tok
		row := func(group string, usd float64) ticket.RunRow {
			return ticket.RunRow{Run: ticket.Run{Actor: group, Ticket: ticket.ID(x.Ticket), CostUSD: usd}}
		}
		perRun = append(perRun, row("all", usd))
		if key != nil {
			groups = append(groups, row(key(x), usd))
			continue
		}
		for _, s := range x.Spend {
			groups = append(groups, row(groupOrNone(s.Model), *s.USD))
		}
	}
	byGroup := func(r ticket.RunRow) string { return r.Actor }
	rows, _ := costRows(groups, byGroup)
	_, total := costRows(perRun, byGroup)
	head := fmt.Sprintf("per run, from stats.db; %d runs with no delta", missing)
	return c.out(map[string]any{"by": *by, "rows": rows, "total": total, "no_delta": missing, "tokens": tokens},
		func(w io.Writer) {
			fmt.Fprintln(w, head)
			if printCostTable(w, *by, rows, total) {
				fmt.Fprintf(w, "tokens: %d\n", tokens)
			}
		})
}

// statsHealth is what stats health prints.
type statsHealth struct {
	Stats string `json:"stats"` // on or off
	Why   string `json:"why,omitempty"`
	// File is none, or the schema it holds and how it compares.
	File      string `json:"file"`
	Schema    int    `json:"schema"`
	Writes    int    `json:"writes"` // the schema this binary writes
	Bytes     int64  `json:"bytes"`  // with its WAL
	Rows      int64  `json:"rows"`
	LastPrune string `json:"last_prune,omitempty"`
	// Ended counts ended runs on disk since the first row (all of them
	// with no row yet); Missing those with no row. The text says no row
	// yet in place of a missing count when Rows is 0.
	Ended   int    `json:"ended"`
	Missing int    `json:"missing"`
	Space   *int64 `json:"space_free_pct,omitempty"`
	Inodes  *int64 `json:"inodes_free_pct,omitempty"`
	Error   string `json:"error,omitempty"`
}

// runStatsHealth says whether stats are on, and how well they keep up.
func runStatsHealth(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.refuseBot("health"); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	path, err := c.besideStore(stats.DBName)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	h := statsHealth{Stats: "on", File: "none", Writes: stats.Version()}
	if why := stats.Off(dir, c.env.Getenv, c.statsUnsafe()); why != "" {
		h.Stats, h.Why = "off", offWhy(why)
	}
	var st stats.State
	if fi, err := os.Lstat(path); err == nil && fi.Size() > 0 {
		h.Bytes = fi.Size()
		if wal, err := os.Lstat(path + "-wal"); err == nil {
			h.Bytes += wal.Size()
		}
		if db, err := stats.OpenRead(path); err != nil {
			h.Error = err.Error()
		} else {
			st, err = stats.ReadState(ctx, db)
			db.Close()
			if err != nil {
				h.Error = err.Error()
			}
		}
		h.Schema, h.Rows = st.Version, st.Runs
		switch {
		case h.Error != "":
			h.File = "unreadable"
		case st.Version == stats.Version():
			h.File = "ok"
		case st.Version > stats.Version():
			h.File = "newer"
		default:
			h.File = "no schema"
		}
		if !st.LastPrune.IsZero() {
			h.LastPrune = ago(c.now().Sub(st.LastPrune)) + " ago"
		}
	}
	if h.File == "ok" || h.File == "none" {
		runsDir := filepath.Join(dir, "runs")
		list, _ := runs.List(runsDir, c.now(), true)
		for _, r := range list {
			if !r.Ended() || r.State == runs.Unreadable || r.EndedAt == nil || r.EndedAt.Before(st.First) {
				continue
			}
			// A command that never started, and a run started with stats
			// off, never get a row: no write was dropped.
			if (r.PID == 0 && r.Error != "") || startedOff(runsDir, r) {
				continue
			}
			h.Ended++
			if !st.Stored[r.ID] {
				h.Missing++
			}
		}
	}
	if f, ok, err := c.diskFree()(dir); err == nil && ok {
		h.Space, h.Inodes = ptr(int64(f.Space)), ptr(int64(f.Inodes))
	}
	return c.out(h, func(w io.Writer) { printHealth(w, h) })
}

// printHealth prints health as text. install.sh apply reads its first
// line: "stats: on", or "stats: off (why): no run is written".
func printHealth(w io.Writer, h statsHealth) {
	if h.Why != "" {
		fmt.Fprintf(w, "stats: off (%s): no run is written\n", h.Why)
	} else {
		fmt.Fprintln(w, "stats: on")
	}
	switch h.File {
	case "none":
		fmt.Fprintln(w, "stats.db: none yet; a run's supervisor starts it at the first run's end")
	case "unreadable":
		fmt.Fprintf(w, "stats.db: unreadable (%s); rm stats.db* starts it again, and leaves tickets alone\n", h.Error)
	case "newer":
		fmt.Fprintf(w, "stats.db: schema %d, newer than this maestro-ticket's %d: rebuild it\n", h.Schema, h.Writes)
	case "no schema":
		fmt.Fprintf(w, "stats.db: schema %d of %d; the next run's supervisor brings it up\n", h.Schema, h.Writes)
	default:
		line := fmt.Sprintf("stats.db: schema %d, %d runs, %d KB", h.Schema, h.Rows, (h.Bytes+1023)/1024)
		if h.LastPrune != "" {
			line += ", last prune " + h.LastPrune
		}
		fmt.Fprintln(w, line)
	}
	switch {
	case (h.File == "ok" || h.File == "none") && h.Rows == 0:
		fmt.Fprintf(w, "missing rows: no row yet; %d ended runs on disk\n", h.Ended)
	case h.File == "ok":
		fmt.Fprintf(w, "missing rows: %d of %d ended runs on disk since the first row\n", h.Missing, h.Ended)
	}
	if h.Space != nil {
		fmt.Fprintf(w, "disk free: %d%% space, %d%% inodes\n", *h.Space, *h.Inodes)
	}
}

// bundleOut is what stats bundle prints: where the file went and what it
// holds by count, never its body.
type bundleOut struct {
	Path   string     `json:"path"`
	Bytes  int        `json:"bytes"`
	SHA256 string     `json:"sha256"`
	Rows   bundleRows `json:"rows"`
	Cost   bool       `json:"cost"`
	Sent   bool       `json:"sent"`
}

type bundleRows struct {
	Runs     int `json:"runs"`
	Findings int `json:"findings"`
	Steps    int `json:"steps"`
}

// runStatsBundle writes one ticket's runs to a case bundle file for a
// person to read and, once the owner says yes, send.
func runStatsBundle(ctx context.Context, c *call) error {
	caseID := c.fs.String("case", "", "the ticket whose runs go in the bundle (T-12)")
	cost := c.fs.Bool("cost", false, "add each run's spend and tokens")
	out := c.fs.String("out", "", "the file to write, or a folder for it (default: bundles/ beside the ticket store, outside the work tree)")
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.refuseBundle(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	if *caseID == "" {
		return usagef("stats bundle: want --case and a ticket id (T-12)")
	}
	id, err := ticket.ParseID(*caseID)
	if err != nil {
		return usagef("stats bundle: --case %q is no ticket id (T-12)", *caseID)
	}
	lists := statsLists()
	var b *stats.Bundle
	note, err := c.withStats(func(db *sql.DB) (err error) {
		b, err = stats.CaseBundle(ctx, db, int64(id), lists, *cost)
		return err
	})
	switch {
	case errors.Is(err, stats.ErrNoRuns):
		return fmt.Errorf("%w: %s: no runs in stats.db; nothing was written", ticket.ErrNotFound, id)
	case err != nil:
		return err
	case note != "":
		return c.printNote(note + "; nothing was written")
	}
	body, err := b.Encode(lists)
	if err != nil {
		return err
	}
	dest := *out
	switch {
	case dest == "":
		// Beside the store, outside the work tree: in the current folder
		// a session's git add -A would take it, and a push send it.
		if dest, err = c.besideStore("bundles"); err != nil {
			return err
		}
		if err := os.Mkdir(dest, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("stats bundle: %w", err)
		}
	case !filepath.IsAbs(dest):
		dest = filepath.Join(c.env.Dir, dest)
	}
	path, err := stats.SaveBundle(dest, body)
	if err != nil {
		return fmt.Errorf("stats bundle: %w", err)
	}
	sum := sha256.Sum256(body)
	o := bundleOut{Path: path, Bytes: len(body), SHA256: hex.EncodeToString(sum[:]), Cost: *cost}
	for _, r := range b.Runs {
		o.Rows.Runs++
		o.Rows.Findings += len(r.Findings)
		o.Rows.Steps += len(r.Steps)
	}
	return c.out(o, func(w io.Writer) {
		fmt.Fprintf(w, "wrote %s: %d bytes, sha256 %s\n", path, o.Bytes, o.SHA256)
		spend := "no spend or tokens (--cost adds them)"
		if o.Cost {
			spend = "with spend and tokens"
		}
		fmt.Fprintf(w, "rows: %d runs, %d findings, %d steps; %s\n", o.Rows.Runs, o.Rows.Findings, o.Rows.Steps, spend)
		fmt.Fprintf(w, "nothing was sent: read the file first; it leaves this machine only by a person's hand, after the owner says yes. stats case %s shows the same runs\n", id)
	})
}
