// Package cli implements the maestro-usage command line on top of usage.Store.
package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-usage/sqlitestore"
	"github.com/ProlesDei/maestro/tools/maestro-usage/usage"
)

// Exit codes. check uses 0-2, breaker 0 and 2, and run passes the harness's
// own code through. Usage and internal errors stay clear of 1 and 2 so a
// typo is never read as "over budget".
const (
	ExitOK       = 0
	ExitWarn     = 1
	ExitOver     = 2 // over budget, or breaker tripped
	ExitUsage    = 64
	ExitInternal = 70
)

// Env is everything the CLI touches outside its arguments, so tests can run
// it in-process.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	Dir    string           // working directory for git lookups and run
	Now    func() time.Time // nil means time.Now
}

// OSEnv returns an Env wired to the real process.
func OSEnv() Env {
	dir, _ := os.Getwd()
	return Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Dir: dir}
}

// Main runs the CLI against the real process and returns the exit code.
func Main(args []string) int { return Run(context.Background(), args, OSEnv()) }

const usageText = `maestro-usage: record harness usage, check budgets, trip breakers. No tokens.

Usage: maestro-usage <command> [flags]

Commands:
  run      [context] [--harness H] -- <harness command...>
           run the harness, pass its output through, record one row
  record   [context] [--harness H]       record usage from harness output on stdin
  record   [context] --manual [--harness H] [--session-id S] [--input-tokens N]
           [--cached-tokens N] [--output-tokens N] [--cost-usd X] [--status success|error]
  check    [--ticket T] [--workstream W] [--role R] [--verbose]
  breaker  --role R
  report   [--by ticket|workstream|role|model|harness] [--since 24h] [--json]

Context flags: --ticket T --workstream W --role R --bot B
  --model M         model name; needed to price Codex from the price table
  --record-key K    idempotency key: saving the same key again replaces the row
  --harness H       claude, codex or grok; default detects from the output

Flags on every command:
  --db PATH         usage db (default $MAESTRO_USAGE_DB, else
                    $(git rev-parse --git-common-dir)/maestro/usage.db)
  --budgets PATH    budgets (default $MAESTRO_BUDGETS, else
                    <repo root>/.maestro/project/budgets.json if it exists)

Exit codes: check 0 ok, 1 warn, 2 over. breaker 0 ok, 2 tripped.
run exits with the harness's code. 64 bad usage or config, 70 internal error.
`

type command struct {
	name string
	run  func(ctx context.Context, c *call) (int, error)
}

var commands = []command{
	{"run", runRun},
	{"record", runRecord},
	{"check", runCheck},
	{"breaker", runBreaker},
	{"report", runReport},
}

// call is one parsed invocation.
type call struct {
	env         Env
	fs          *flag.FlagSet
	dbFlag      string
	budgetsFlag string
	args        []string
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

// Run executes one command and returns the process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	if env.Getenv == nil {
		env.Getenv = func(string) string { return "" }
	}
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(env.Stdout, usageText)
		if len(args) == 0 {
			return ExitUsage
		}
		return ExitOK
	}
	i := slices.IndexFunc(commands, func(c command) bool { return c.name == args[0] })
	if i < 0 {
		fmt.Fprintf(env.Stderr, "maestro-usage: unknown command %q\n\n%s", args[0], usageText)
		return ExitUsage
	}
	c := &call{env: env, fs: flag.NewFlagSet("maestro-usage "+args[0], flag.ContinueOnError), args: args[1:]}
	c.fs.SetOutput(env.Stderr)
	c.fs.StringVar(&c.dbFlag, "db", "", "usage db path")
	c.fs.StringVar(&c.budgetsFlag, "budgets", "", "budgets.json path")
	code, err := commands[i].run(ctx, c)
	if err == nil {
		return code
	}
	fmt.Fprintf(env.Stderr, "maestro-usage %s: %v\n", args[0], err)
	var ue usageError
	if errors.As(err, &ue) || errors.Is(err, usage.ErrInvalid) || errors.Is(err, flag.ErrHelp) {
		return ExitUsage
	}
	return ExitInternal
}

// parse parses flags. Only run takes positional arguments.
func (c *call) parse(positional bool) error {
	if err := c.fs.Parse(c.args); err != nil {
		return usageError{err.Error()}
	}
	c.args = c.fs.Args()
	if !positional && len(c.args) > 0 {
		return usagef("unexpected argument %q", c.args[0])
	}
	return nil
}

func (c *call) printJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.env.Stdout, "%s\n", b)
	return err
}

func (c *call) abs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.env.Dir, p)
}

// dbPath resolves the db: --db, then $MAESTRO_USAGE_DB, then the git common
// dir, which every worktree of a repo shares, then .maestro/local outside git.
func (c *call) dbPath() string {
	if c.dbFlag != "" {
		return c.abs(c.dbFlag)
	}
	if p := c.env.Getenv("MAESTRO_USAGE_DB"); p != "" {
		return c.abs(p)
	}
	if common, err := c.git("rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		return filepath.Join(common, "maestro", "usage.db")
	}
	return filepath.Join(c.env.Dir, ".maestro", "local", "usage.db")
}

func (c *call) open(ctx context.Context) (usage.Store, error) {
	return sqlitestore.Open(ctx, c.dbPath())
}

// budgets loads --budgets, then $MAESTRO_BUDGETS, then the project file. A
// path given explicitly must exist; a missing project file means no limits.
func (c *call) budgets() (usage.Budgets, error) {
	path := c.budgetsFlag
	if path == "" {
		path = c.env.Getenv("MAESTRO_BUDGETS")
	}
	explicit := path != ""
	if explicit {
		path = c.abs(path)
	} else {
		root, err := c.git("rev-parse", "--show-toplevel")
		if err != nil {
			root = c.env.Dir
		}
		path = filepath.Join(root, ".maestro", "project", "budgets.json")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return usage.DefaultBudgets(), nil
	}
	if err != nil {
		return usage.Budgets{}, fmt.Errorf("%w: budgets: %v", usage.ErrInvalid, err)
	}
	return usage.ParseBudgets(data)
}

func (c *call) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = c.env.Dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// recordFlags are the flags run and record share.
type recordFlags struct {
	ctx       usage.Context
	model     string
	recordKey string
	harness   string
}

func (c *call) recordFlags() *recordFlags {
	f := &recordFlags{}
	c.fs.StringVar(&f.ctx.Ticket, "ticket", "", "ticket id")
	c.fs.StringVar(&f.ctx.Workstream, "workstream", "", "workstream")
	c.fs.StringVar(&f.ctx.Role, "role", "", "role")
	c.fs.StringVar(&f.ctx.Bot, "bot", "", "bot")
	c.fs.StringVar(&f.model, "model", "", "model name")
	c.fs.StringVar(&f.recordKey, "record-key", "", "idempotency key")
	c.fs.StringVar(&f.harness, "harness", "", "claude, codex or grok")
	return f
}

func checkHarness(h string) error {
	if h != "" && !slices.Contains(usage.Harnesses, h) {
		return usagef("--harness %q: want one of %s", h, strings.Join(usage.Harnesses, ", "))
	}
	return nil
}

// finish applies the model override and prices, then saves the record.
func (c *call) finish(ctx context.Context, s usage.Store, b usage.Budgets, f *recordFlags, harness, key string, u usage.Usage) (usage.Record, error) {
	if f.model != "" {
		u.Model = f.model
	}
	usage.ApplyPrices(&u, b.Prices)
	if f.recordKey != "" {
		key = f.recordKey
	}
	r := usage.Record{Key: key, RecordedAt: c.env.Now(), Harness: harness, Context: f.ctx, Usage: u}
	return r, s.Save(ctx, r)
}

func randomKey(harness string) string {
	b := make([]byte, 16)
	rand.Read(b)
	return harness + ":" + hex.EncodeToString(b)
}

func runRun(ctx context.Context, c *call) (int, error) {
	f := c.recordFlags()
	if err := c.parse(true); err != nil {
		return 0, err
	}
	if err := checkHarness(f.harness); err != nil {
		return 0, err
	}
	if len(c.args) == 0 {
		return 0, usagef("give the harness command after --")
	}
	// Open the store and budgets first, so a broken setup fails before any
	// tokens are spent.
	b, err := c.budgets()
	if err != nil {
		return 0, err
	}
	s, err := c.open(ctx)
	if err != nil {
		return 0, err
	}
	defer s.Close()

	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, c.args[0], c.args[1:]...)
	cmd.Dir = c.env.Dir
	cmd.Stdin = c.env.Stdin
	// Pass output through as it arrives, so the caller sees exactly what the
	// harness printed. If the caller stops reading, keep capturing anyway.
	cmd.Stdout = io.MultiWriter(&out, &passThrough{w: c.env.Stdout})
	cmd.Stderr = c.env.Stderr

	// Forward interrupts to the harness and stay alive to record its usage.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	// Without this, a closed stdout pipe (run ... | head) kills us with
	// SIGPIPE before the row is saved. The harness still gets its own.
	pipe := make(chan os.Signal, 1)
	signal.Notify(pipe, syscall.SIGPIPE)
	defer signal.Stop(pipe)
	start := time.Now()
	code := 0
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(c.env.Stderr, "maestro-usage run: %v\n", err)
		code = 126
		if errors.Is(err, exec.ErrNotFound) {
			code = 127
		}
	} else {
		done := make(chan struct{})
		go func() {
			for {
				select {
				case s := <-sig:
					cmd.Process.Signal(s)
				case <-done:
					return
				}
			}
		}()
		err := cmd.Wait()
		close(done)
		code = cmd.ProcessState.ExitCode()
		if err != nil && code == 0 {
			code = 1
		}
		if code < 0 { // killed by a signal
			code = 128 + int(cmd.ProcessState.Sys().(syscall.WaitStatus).Signal())
		}
	}
	elapsed := time.Since(start).Milliseconds()

	harness, u := usage.Parse(out.String(), f.harness)
	if code != 0 && u.Status == usage.StatusSuccess {
		u.Status = usage.StatusError
	}
	if u.DurationMS == nil {
		u.DurationMS = &elapsed
	}
	// Every run is a new invocation, so it gets its own row.
	if _, err := c.finish(ctx, s, b, f, harness, randomKey(harness), u); err != nil {
		// Don't mask the harness's result; say the row is missing.
		fmt.Fprintf(c.env.Stderr, "maestro-usage run: usage not recorded: %v\n", err)
	}
	return code, nil
}

// passThrough writes to w until the first error, then drops the rest, so a
// reader that goes away doesn't stop the harness or the capture.
type passThrough struct {
	w   io.Writer
	err error
}

func (p *passThrough) Write(b []byte) (int, error) {
	if p.err == nil {
		_, p.err = p.w.Write(b)
	}
	return len(b), nil
}

func runRecord(ctx context.Context, c *call) (int, error) {
	f := c.recordFlags()
	manual := c.fs.Bool("manual", false, "record from flags, not stdin")
	sessionID := c.fs.String("session-id", "", "session id")
	in := c.fs.Int64("input-tokens", 0, "uncached input tokens")
	cached := c.fs.Int64("cached-tokens", 0, "cache-read tokens")
	outTok := c.fs.Int64("output-tokens", 0, "output tokens")
	var cost *float64
	c.fs.Func("cost-usd", "cost in dollars", func(s string) error {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("want a non-negative number of dollars")
		}
		cost = &v
		return nil
	})
	status := c.fs.String("status", usage.StatusSuccess, "success or error")
	if err := c.parse(false); err != nil {
		return 0, err
	}
	if !*manual {
		var stray []string
		c.fs.Visit(func(fl *flag.Flag) {
			switch fl.Name {
			case "session-id", "input-tokens", "cached-tokens", "output-tokens", "cost-usd", "status":
				stray = append(stray, "--"+fl.Name)
			}
		})
		if len(stray) > 0 {
			return 0, usagef("%s need --manual", strings.Join(stray, ", "))
		}
		if err := checkHarness(f.harness); err != nil {
			return 0, err
		}
	}
	if *status != usage.StatusSuccess && *status != usage.StatusError {
		return 0, usagef("--status %q: want success or error", *status)
	}
	// Negative counts would cancel out real spending.
	if *in < 0 || *cached < 0 || *outTok < 0 {
		return 0, usagef("token counts can't be negative")
	}
	b, err := c.budgets()
	if err != nil {
		return 0, err
	}
	s, err := c.open(ctx)
	if err != nil {
		return 0, err
	}
	defer s.Close()

	var harness, key string
	var u usage.Usage
	if *manual {
		// For usage no harness reports, such as the bot runtime's own tokens.
		harness = f.harness
		if harness == "" {
			harness = usage.HarnessManual
		}
		u = usage.Usage{SessionID: *sessionID, InputTokens: *in, CachedTokens: *cached,
			OutputTokens: *outTok, Status: *status}
		if cost != nil {
			u.CostUSD, u.CostSource = cost, usage.CostManual
		}
		key = randomKey(harness)
	} else {
		raw, err := io.ReadAll(c.env.Stdin)
		if err != nil {
			return 0, err
		}
		harness, u = usage.Parse(string(raw), f.harness)
		// Re-recording the same output (a retried pipe) replaces its row
		// instead of counting it twice.
		sum := sha256.Sum256(raw)
		sess := u.SessionID
		if sess == "" {
			sess = "nosession"
		}
		key = harness + ":" + sess + ":" + hex.EncodeToString(sum[:8])
	}
	r, err := c.finish(ctx, s, b, f, harness, key, u)
	if err != nil {
		return 0, err
	}
	return ExitOK, c.printJSON(map[string]any{"recorded": r.Key, "harness": r.Harness,
		"status": r.Status, "cost_usd": r.CostUSD})
}

func runCheck(ctx context.Context, c *call) (int, error) {
	var sc usage.Scopes
	c.fs.StringVar(&sc.Ticket, "ticket", "", "ticket id")
	c.fs.StringVar(&sc.Workstream, "workstream", "", "workstream")
	c.fs.StringVar(&sc.Role, "role", "", "role")
	verbose := c.fs.Bool("verbose", false, "show every limit, not just problems")
	if err := c.parse(false); err != nil {
		return 0, err
	}
	b, err := c.budgets()
	if err != nil {
		return 0, err
	}
	s, err := c.open(ctx)
	if err != nil {
		return 0, err
	}
	defer s.Close()
	res, err := usage.Check(ctx, s, c.env.Now(), b, sc)
	if err != nil {
		return 0, err
	}
	if !*verbose {
		res.Findings = slices.DeleteFunc(res.Findings, func(f usage.Finding) bool { return f.Status == usage.CheckOK })
	}
	if err := c.printJSON(res); err != nil {
		return 0, err
	}
	return map[string]int{usage.CheckOK: ExitOK, usage.CheckWarn: ExitWarn, usage.CheckOver: ExitOver}[res.Status], nil
}

func runBreaker(ctx context.Context, c *call) (int, error) {
	role := c.fs.String("role", "", "role pool")
	if err := c.parse(false); err != nil {
		return 0, err
	}
	if *role == "" {
		return 0, usagef("--role is required")
	}
	b, err := c.budgets()
	if err != nil {
		return 0, err
	}
	s, err := c.open(ctx)
	if err != nil {
		return 0, err
	}
	defer s.Close()
	res, err := usage.Breaker(ctx, s, *role, b.Breaker)
	if err != nil {
		return 0, err
	}
	if err := c.printJSON(res); err != nil {
		return 0, err
	}
	if res.Status == "tripped" {
		return ExitOver, nil
	}
	return ExitOK, nil
}

func runReport(ctx context.Context, c *call) (int, error) {
	by := c.fs.String("by", "workstream", "ticket, workstream, role, model or harness")
	since := c.fs.String("since", "", "window such as 30m, 24h or 7d")
	jsonOut := c.fs.Bool("json", false, "machine-readable output")
	if err := c.parse(false); err != nil {
		return 0, err
	}
	field := usage.Field(*by)
	if !field.Valid() {
		return 0, usagef("--by %q: want ticket, workstream, role, model or harness", *by)
	}
	var from time.Time
	if *since != "" {
		d, err := usage.ParseWindow(*since)
		if err != nil {
			return 0, err
		}
		from = c.env.Now().Add(-d)
	}
	s, err := c.open(ctx)
	if err != nil {
		return 0, err
	}
	defer s.Close()
	rows, err := s.Report(ctx, field, from)
	if err != nil {
		return 0, err
	}
	if *jsonOut {
		out := make([]map[string]any, len(rows))
		for i, r := range rows {
			out[i] = map[string]any{*by: r.Group, "sessions": r.Sessions, "usd": r.USD,
				"input": r.Input, "cached": r.Cached, "output": r.Output,
				"failures": r.Failures, "unpriced": r.Unpriced}
		}
		return ExitOK, c.printJSON(out)
	}
	w := c.env.Stdout
	fmt.Fprintf(w, "%-24s %8s %10s %12s %12s %10s %7s\n", *by, "sessions", "usd", "input", "cached", "output", "failed")
	unpriced := false
	for _, r := range rows {
		mark := ""
		if r.Unpriced > 0 {
			mark, unpriced = " *", true
		}
		name := r.Group
		if len(name) > 24 {
			name = name[:24]
		}
		fmt.Fprintf(w, "%-24s %8d %10.4f %12d %12d %10d %7d%s\n", name, r.Sessions, r.USD, r.Input, r.Cached, r.Output, r.Failures, mark)
	}
	if unpriced {
		fmt.Fprintln(w, "* some sessions have no cost: add their model to prices_per_million_tokens")
	}
	return ExitOK, nil
}
