// Package cli implements the maestro-ticket command line on top of ticket.Store.
package cli

import (
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
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/render"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/sqlitestore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// Env is everything the CLI touches outside its arguments, so tests can run
// it in-process.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	Dir    string           // working directory used for git lookups
	Now    func() time.Time // clock for the store; nil means the database's
	// StoreChecks checks the store's directory before opening it; nil
	// means envcheck.Store. Tests use it to fake an unsafe filesystem.
	StoreChecks func(dir string, allow bool) []ticket.Check
}

// OSEnv returns an Env wired to the real process.
func OSEnv() Env {
	dir, _ := os.Getwd()
	return Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Dir: dir}
}

// Main runs the CLI against the real process and returns the exit code.
// SIGINT and SIGTERM cancel the context, so long-running commands stop
// between steps.
func Main(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return Run(ctx, args, OSEnv())
}

const usage = `maestro-ticket: the maestro ticket store.

Usage: maestro-ticket <command> [flags]

Commands:
  create      --title T [--body B | --body-file F|-] [--type ship|scout] [--role R] [--priority 0-3]
              [--status backlog|todo|blocked (default todo)] [--owner NAME] [--deps T-1,T-2]
              [--areas path,path] [--split-from T-1]
  update      <id> [--add-deps T-1,..] [--remove-deps T-1,..] [--areas path,..] [--holder H --token N]
  get         <id>
  list        [--status S,...] [--role R] [--holder H] [--claimed | --unclaimed | --ready] [--limit N]
              [--latest-prefix P] [--not-latest-prefix P]   (ordered by priority, then id)
  claim       (--next | <id>) --holder <bot-id>/<session-id> [--status S,...] [--role R] [--ttl 30m]
              [--keepalive PID]
  heartbeat   <id> --holder H --token N [--ttl 30m]
  keepalive   <id> --holder H --token N --pid PID [--ttl 30m] [--interval D]
  release     <id> --holder H --token N
  reap
  transition  <id> --to S [--from S] [--expect-version N] [--holder H --token N] [--role R] [--note T] [--release]
              [--actor-role R]
  comment     <id> (--body B | --body-file F|-) [--holder H --token N]
  events      [--since SEQ] [--limit N] [--ticket ID] [--type T]
  backup      [--dir D] [--keep N]   (default dir: backups/ next to the store)
  export      [--out PATH|-]
  render      [--out PATH]     (default: Backlog.md next to the store)
  doctor

Flags on every command:
  --json                  machine-readable output (errors too, on stdout)
  --store PATH            ticket store (default $MAESTRO_TICKET_STORE, else
                          $(git rev-parse --git-common-dir)/maestro/tickets.db)
  --actor NAME            who is acting (default $MAESTRO_TICKET_ACTOR)
  --idempotency-key K     on create, update, claim, release, transition,
                          comment: a retry with the same key and request
                          replays the result

Fencing: every claim returns a lease token (claim.token), new on every claim,
including re-claiming your own lease. heartbeat and release always need
--holder and --token. On a claimed ticket, transition and comment need them
too. All fail with exit 4 once the lease is lost.
Heartbeats: claim --keepalive PID starts a background keepalive that
heartbeats every ttl/3 while process PID (the harness session) lives, and
stops when the lease is lost or released. claim and keepalive refuse a PID
in their own session when it has no terminal: it exits with the command.
Expired leases: reap frees them, logs lease_expired and checkpoints the
WAL; any write to the ticket frees its expired lease too. Until then, the
holder's heartbeat revives it (lease_revived), as after a machine sleeps.
Kill switch: while a file named STOP sits next to the store, claim refuses
(stopped, exit 4).

Roles: chief-of-staff planner engineer qa-lead reviewer
Statuses: backlog todo in_progress in_review blocked done cancelled
Ship:  backlog -> todo -> in_progress -> in_review -> done (by the reviewer,
       or the planner when the PR merged outside a review)
Scout: backlog -> todo -> in_progress -> in_review -> done (by the owner)
Only --actor-role reviewer or planner (default $MAESTRO_TICKET_ROLE) sets a
ship ticket done; only its owner (--actor) sets a scout ticket done.
In todo and in_progress, claim skips (--next) or refuses (by id) tickets
whose dependencies aren't all done, and tickets whose areas overlap (equal,
or one inside the other) an area of another todo or in_progress ticket under
a live claim.
Blocked returns only to the status it was blocked from. Send-backs from
in_review to in_progress add one to a ship ticket's tries.
Exit codes: 0 ok, 1 internal, 2 usage/invalid, 3 not found, 4 conflict or busy, 5 no work
`

type command struct {
	name string
	run  func(ctx context.Context, c *call) error
}

var commands = []command{
	{"create", runCreate},
	{"update", runUpdate},
	{"get", runGet},
	{"list", runList},
	{"claim", runClaim},
	{"heartbeat", runHeartbeat},
	{"keepalive", runKeepalive},
	{"release", runRelease},
	{"reap", runReap},
	{"transition", runTransition},
	{"comment", runComment},
	{"events", runEvents},
	{"backup", runBackup},
	{"export", runExport},
	{"render", runRender},
	{"doctor", runDoctor},
}

// call is one parsed invocation.
type call struct {
	env       Env
	fs        *flag.FlagSet
	jsonOut   bool
	storeFlag string
	actor     string
	idemKey   string
	args      []string // positional arguments
}

// usageError marks errors caused by bad command-line input.
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
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(env.Stdout, usage)
		if len(args) == 0 {
			return ticket.ExitUsage
		}
		return ticket.ExitOK
	}
	var cmd *command
	for i := range commands {
		if commands[i].name == args[0] {
			cmd = &commands[i]
		}
	}
	if cmd == nil {
		fmt.Fprintf(env.Stderr, "maestro-ticket: unknown command %q\n\n%s", args[0], usage)
		return ticket.ExitUsage
	}
	c := &call{env: env, fs: flag.NewFlagSet("maestro-ticket "+cmd.name, flag.ContinueOnError)}
	c.fs.SetOutput(env.Stderr)
	c.fs.BoolVar(&c.jsonOut, "json", false, "machine-readable output")
	c.fs.StringVar(&c.storeFlag, "store", "", "ticket store path")
	c.fs.StringVar(&c.actor, "actor", env.Getenv("MAESTRO_TICKET_ACTOR"), "who is acting")
	c.fs.StringVar(&c.idemKey, "idempotency-key", "", "idempotency key for writes")
	c.args = args[1:]
	err := cmd.run(ctx, c)
	if errors.Is(err, flag.ErrHelp) {
		return ticket.ExitOK
	}
	return c.fail(err)
}

// parse parses flags, allowing positional arguments anywhere. It refuses a
// flag given twice: the flag package keeps only the last, so
// --areas a --areas b would drop a without a word.
func (c *call) parse() error {
	orig := map[string]flag.Value{}
	set := map[string]bool{}
	c.fs.VisitAll(func(f *flag.Flag) {
		orig[f.Name] = f.Value
		f.Value = onceValue{f.Value, f.Name, set}
	})
	restore := func() { c.fs.VisitAll(func(f *flag.Flag) { f.Value = orig[f.Name] }) }
	defer restore()
	c.fs.Usage = func() {
		restore() // PrintDefaults reads each flag's own type for its default
		fmt.Fprintf(c.fs.Output(), "Usage of %s:\n", c.fs.Name())
		c.fs.PrintDefaults()
	}
	rest := c.args
	var pos []string
	for {
		if err := c.fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return err
			}
			return usageError{err.Error()}
		}
		if c.fs.NArg() == 0 {
			break
		}
		pos = append(pos, c.fs.Arg(0))
		rest = c.fs.Args()[1:]
	}
	c.args = pos
	if ticket.HasControl(c.actor) {
		return usagef("--actor (or $MAESTRO_TICKET_ACTOR) must be one line, with no control characters")
	}
	return nil
}

// onceValue refuses a second Set of the flag it wraps.
type onceValue struct {
	flag.Value
	name string
	set  map[string]bool
}

func (o onceValue) Set(s string) error {
	if o.set[o.name] {
		return errors.New("given twice; a list flag takes one comma-separated value")
	}
	o.set[o.name] = true
	return o.Value.Set(s)
}

func (o onceValue) IsBoolFlag() bool {
	b, ok := o.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

func (c *call) idArg() (ticket.ID, error) {
	if len(c.args) != 1 {
		return 0, usagef("%s: want exactly one ticket id", c.fs.Name())
	}
	return ticket.ParseID(c.args[0])
}

func (c *call) noArgs() error {
	if len(c.args) > 0 {
		return usagef("%s: unexpected argument %q", c.fs.Name(), c.args[0])
	}
	return nil
}

func (c *call) fail(err error) int {
	if err == nil {
		return ticket.ExitOK
	}
	var se silentExit
	if errors.As(err, &se) {
		return se.code
	}
	code, exit := ticket.Classify(err)
	var ue usageError
	if errors.As(err, &ue) {
		code, exit = "usage", ticket.ExitUsage
	}
	if c.jsonOut {
		c.writeJSON(map[string]any{"error": map[string]string{"code": code, "message": err.Error()}})
	} else {
		fmt.Fprintf(c.env.Stderr, "maestro-ticket: %v\n", err)
	}
	return exit
}

func (c *call) writeJSON(v any) error {
	enc := json.NewEncoder(c.env.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// out prints v as JSON, or calls human for the default output.
func (c *call) out(v any, human func(w io.Writer)) error {
	if c.jsonOut {
		return c.writeJSON(v)
	}
	human(c.env.Stdout)
	return nil
}

// storePath resolves the store: --store, then $MAESTRO_TICKET_STORE, then the git
// common dir, which every worktree of a repo shares. A relative --store is
// taken from the working directory; a relative $MAESTRO_TICKET_STORE is
// refused, since each worktree would read it as a store of its own. The git
// default refuses a bare repo (no work tree to run sessions in), a
// submodule (its store would be split from the superproject's) and
// Maestro's own copy in .maestro/src (a second, empty store); pass --store
// there.
func (c *call) storePath() (path, source string, err error) {
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(c.env.Dir, p)
	}
	if c.storeFlag != "" {
		return abs(c.storeFlag), "--store", nil
	}
	if p := c.env.Getenv("MAESTRO_TICKET_STORE"); p != "" {
		if !filepath.IsAbs(p) {
			return "", "", fmt.Errorf("%w: MAESTRO_TICKET_STORE %q must be an absolute path: a relative one names a different store in each worktree", ticket.ErrInvalid, p)
		}
		return p, "MAESTRO_TICKET_STORE", nil
	}
	out, err := c.git("rev-parse", "--path-format=absolute", "--git-common-dir", "--is-bare-repository", "--show-superproject-working-tree")
	if err != nil {
		return "", "", fmt.Errorf("%w: no --store or MAESTRO_TICKET_STORE, and not in a git repo: %v", ticket.ErrInvalid, err)
	}
	lines := strings.Split(out, "\n")
	common := lines[0]
	// Git before 2.31 doesn't know --path-format and prints it back.
	if strings.HasPrefix(common, "--path-format") {
		return "", "", fmt.Errorf("%w: maestro-ticket needs git 2.31 or later to find the store; upgrade git, or pass --store", ticket.ErrInvalid)
	}
	switch {
	case len(lines) > 1 && lines[1] == "true":
		return "", "", fmt.Errorf("%w: %s is a bare repo; run from a work tree, or pass --store", ticket.ErrInvalid, common)
	case len(lines) > 2 && lines[2] != "":
		return "", "", fmt.Errorf("%w: this is a submodule of %s; run maestro-ticket from the superproject, or pass --store", ticket.ErrInvalid, lines[2])
	case filepath.Base(common) == ".git" && filepath.Base(filepath.Dir(common)) == "src" &&
		filepath.Base(filepath.Dir(filepath.Dir(common))) == ".maestro":
		project := filepath.Dir(filepath.Dir(filepath.Dir(common)))
		return "", "", fmt.Errorf("%w: this is Maestro's own copy in %s; run maestro-ticket from %s, or pass --store", ticket.ErrInvalid, filepath.Dir(common), project)
	}
	return filepath.Join(common, "maestro", "tickets.db"), "git common dir", nil
}

// envChecks runs the filesystem and lock checks on the store's directory,
// creating it if needed.
func (c *call) envChecks(path string) []ticket.Check {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return []ticket.Check{{Name: "filesystem", OK: false, Detail: fmt.Sprintf("create store dir: %v", err)}}
	}
	checks := envcheck.Store
	if c.env.StoreChecks != nil {
		checks = c.env.StoreChecks
	}
	return checks(dir, c.env.Getenv(envcheck.AllowEnv) == "1")
}

func (c *call) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = c.env.Dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *call) open(ctx context.Context) (*sqlitestore.Store, error) {
	path, _, err := c.storePath()
	if err != nil {
		return nil, err
	}
	if err := envcheck.Err(c.envChecks(path)); err != nil {
		return nil, err
	}
	return sqlitestore.Open(ctx, path, sqlitestore.Options{Now: c.env.Now})
}

// withStore parses flags, opens the store and runs fn.
func (c *call) withStore(ctx context.Context, fn func(s ticket.Store) error) error {
	if err := c.parse(); err != nil {
		return err
	}
	s, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	return fn(s)
}

func (c *call) readBody(body, file string) (string, error) {
	if body != "" && file != "" {
		return "", usagef("use --body or --body-file, not both")
	}
	if file == "" {
		return body, checkBody(body)
	}
	var b []byte
	var err error
	if file == "-" {
		b, err = io.ReadAll(io.LimitReader(c.env.Stdin, maxBody+1))
	} else {
		var f *os.File
		if f, err = os.Open(file); err == nil {
			b, err = io.ReadAll(io.LimitReader(f, maxBody+1))
			f.Close()
		}
	}
	if err != nil {
		return "", usagef("read --body-file: %v", err)
	}
	return string(b), checkBody(string(b))
}

// maxBody caps a body or comment: every list --latest-prefix reads the
// latest comment of each ticket it lists.
const maxBody = 1 << 20

func checkBody(s string) error {
	switch {
	case len(s) > maxBody:
		return usagef("body is over %d bytes: put long output in a file and name its path", maxBody)
	case !utf8.ValidString(s):
		return usagef("body is not valid UTF-8")
	case strings.ContainsRune(s, 0):
		return usagef("body holds a NUL byte")
	}
	return nil
}

func runCreate(ctx context.Context, c *call) error {
	title := c.fs.String("title", "", "one-line title (required)")
	body := c.fs.String("body", "", "ticket body")
	bodyFile := c.fs.String("body-file", "", "read the body from a file, or - for stdin")
	typ := c.fs.String("type", string(ticket.TypeShip), "ship or scout")
	role := c.fs.String("role", "", "role that should pick it up")
	prio := c.fs.Int("priority", ticket.DefaultPriority, "0 (most urgent) to 3")
	status := c.fs.String("status", string(ticket.StatusTodo), "backlog, todo or blocked")
	owner := c.fs.String("owner", "", "who asked for the work (default --actor); closes scout tickets")
	deps := c.fs.String("deps", "", "comma-separated tickets that must be done first")
	areas := c.fs.String("areas", "", "comma-separated code areas the ticket touches (repo-relative paths)")
	splitFrom := c.fs.String("split-from", "", "the ticket this splits or rewrites; the new one keeps its tries")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		var from ticket.ID
		if *splitFrom != "" {
			var err error
			if from, err = ticket.ParseID(*splitFrom); err != nil {
				return err
			}
		}
		b, err := c.readBody(*body, *bodyFile)
		if err != nil {
			return err
		}
		st, err := ticket.ParseStatus(*status)
		if err != nil {
			return err
		}
		ty, err := ticket.ParseType(*typ)
		if err != nil {
			return err
		}
		dl, err := ticket.ParseIDs(*deps)
		if err != nil {
			return err
		}
		t, err := s.Create(ctx, ticket.CreateRequest{
			Title: *title, Body: b, Type: ty, Role: *role, Priority: prio, Status: st,
			Owner: *owner, DependsOn: dl, Areas: ticket.SplitList(*areas), SplitFrom: from, Actor: c.actor, IdempotencyKey: c.idemKey,
		})
		if err != nil {
			return err
		}
		return c.out(t, func(w io.Writer) { fmt.Fprintf(w, "created %s\n", oneLine(t)) })
	})
}

func runUpdate(ctx context.Context, c *call) error {
	add := c.fs.String("add-deps", "", "comma-separated tickets this one now depends on")
	remove := c.fs.String("remove-deps", "", "comma-separated dependencies to drop")
	areas := c.fs.String("areas", "", "replace the code areas (comma-separated; \"\" clears them)")
	holder := c.fs.String("holder", "", "your <bot-id>/<session-id>, required if the ticket is claimed")
	token := c.fs.Int64("token", 0, "lease token from your claim, required if the ticket is claimed")
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		req := ticket.UpdateRequest{ID: id, Holder: *holder, Token: *token, Actor: c.actor, IdempotencyKey: c.idemKey}
		if req.AddDeps, err = ticket.ParseIDs(*add); err != nil {
			return err
		}
		if req.RemoveDeps, err = ticket.ParseIDs(*remove); err != nil {
			return err
		}
		c.fs.Visit(func(f *flag.Flag) {
			if f.Name == "areas" {
				a := ticket.SplitList(*areas)
				req.Areas = &a
			}
		})
		t, err := s.Update(ctx, req)
		if err != nil {
			return err
		}
		return c.out(t, func(w io.Writer) { fmt.Fprintf(w, "updated %s\n", oneLine(t)) })
	})
}

func runGet(ctx context.Context, c *call) error {
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		d, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		return c.out(d, func(w io.Writer) { printDetail(w, d) })
	})
}

func runList(ctx context.Context, c *call) error {
	status := c.fs.String("status", "", "comma-separated statuses")
	role := c.fs.String("role", "", "only this role")
	holder := c.fs.String("holder", "", "only tickets with a live claim by this holder")
	claimed := c.fs.Bool("claimed", false, "only tickets with a live claim")
	unclaimed := c.fs.Bool("unclaimed", false, "only tickets without a live claim")
	ready := c.fs.Bool("ready", false, "only tickets a claim could start now: unclaimed, deps done, areas free (default --status todo)")
	limit := c.fs.Int("limit", 0, "max results (0 = all)")
	latest := c.fs.String("latest-prefix", "", "only tickets whose latest comment starts with this")
	notLatest := c.fs.String("not-latest-prefix", "", "only tickets with no comment, or whose latest comment doesn't start with this")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		if err := ticket.ValidateRole(*role); err != nil {
			return err
		}
		f := ticket.ListFilter{Role: *role, Holder: *holder, Ready: *ready, Limit: *limit}
		byComment := *latest != "" || *notLatest != ""
		if byComment {
			f.Limit = 0 // the limit applies after the comment filter
		}
		var err error
		if f.Statuses, err = ticket.ParseStatuses(*status); err != nil {
			return err
		}
		if *ready && len(f.Statuses) == 0 {
			f.Statuses = []ticket.Status{ticket.StatusTodo} // as claim --next
		}
		switch {
		case *claimed && *unclaimed:
			return usagef("use --claimed or --unclaimed, not both")
		case *claimed && *ready:
			return usagef("use --claimed or --ready, not both: a ready ticket has no live claim")
		case *claimed:
			f.Claimed = claimed
		case *unclaimed:
			no := false
			f.Claimed = &no
		}
		list, err := s.List(ctx, f)
		if err != nil {
			return err
		}
		if byComment {
			if list, err = filterLatest(ctx, s, list, *latest, *notLatest, *limit); err != nil {
				return err
			}
		}
		return c.out(map[string]any{"tickets": list}, func(w io.Writer) { printList(w, list) })
	})
}

// filterLatest keeps the tickets whose latest comment starts with prefix
// (when set) and doesn't start with notPrefix (when set), in list order, up
// to limit (0 = all). A ticket with no comments never matches prefix and
// always passes notPrefix. Comments are read after the list, so one added in
// between counts.
func filterLatest(ctx context.Context, s ticket.Store, list []ticket.Ticket, prefix, notPrefix string, limit int) ([]ticket.Ticket, error) {
	out := []ticket.Ticket{}
	for _, t := range list {
		if limit > 0 && len(out) == limit {
			break
		}
		d, err := s.Get(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		body, has := "", len(d.Comments) > 0
		if has {
			body = d.Comments[len(d.Comments)-1].Body
		}
		if prefix != "" && !(has && strings.HasPrefix(body, prefix)) {
			continue
		}
		if notPrefix != "" && has && strings.HasPrefix(body, notPrefix) {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func runClaim(ctx context.Context, c *call) error {
	next := c.fs.Bool("next", false, "claim the most urgent matching ticket")
	holder := c.fs.String("holder", "", "<bot-id>/<session-id> (required)")
	status := c.fs.String("status", "", "statuses to claim from (default todo for --next)")
	role := c.fs.String("role", "", "only tickets for this role")
	ttl := c.fs.Duration("ttl", ticket.DefaultTTL, "lease length")
	keepPID := c.fs.Int("keepalive", 0, "start a background keepalive for this claim, alive while this process id lives")
	return c.withStore(ctx, func(s ticket.Store) error {
		if *keepPID != 0 {
			if why := unwatchable(*keepPID); why != "" {
				return usagef("claim: --keepalive %d: %s", *keepPID, why)
			}
		}
		req := ticket.ClaimRequest{Holder: *holder, Role: *role, TTL: *ttl, Actor: c.actor, IdempotencyKey: c.idemKey}
		switch {
		case *next && len(c.args) > 0:
			return usagef("claim: use --next or an id, not both")
		case !*next:
			id, err := c.idArg()
			if err != nil {
				return usagef("claim: pass --next or one ticket id")
			}
			req.ID = id
		}
		var err error
		if req.Statuses, err = ticket.ParseStatuses(*status); err != nil {
			return err
		}
		t, err := s.ClaimNext(ctx, req)
		if err != nil {
			return err
		}
		if *keepPID > 0 {
			// The store applied the TTL's default; the keepalive needs it too.
			ttl, err := ticket.ValidateTTL(req.TTL)
			if err != nil {
				return err
			}
			if err := c.startKeepalive(t, *keepPID, ttl); err != nil {
				// A claim nobody keeps alive would expire mid-step: undo it.
				s.Release(ctx, ticket.ReleaseRequest{ID: t.ID, Holder: t.Claim.Holder, Token: t.Claim.Token, Actor: req.Actor})
				return fmt.Errorf("claim: start keepalive (claim released): %w", err)
			}
		}
		return c.out(t, func(w io.Writer) { fmt.Fprintf(w, "claimed %s\n", oneLine(t)) })
	})
}

func runHeartbeat(ctx context.Context, c *call) error {
	holder := c.fs.String("holder", "", "<bot-id>/<session-id> (required)")
	token := c.fs.Int64("token", 0, "lease token from your claim (required)")
	ttl := c.fs.Duration("ttl", ticket.DefaultTTL, "new lease length from now")
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		if c.idemKey != "" {
			return usagef("heartbeat: --idempotency-key not supported; a heartbeat must always extend from now")
		}
		t, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: id, Holder: *holder, Token: *token, TTL: *ttl})
		if err != nil {
			return err
		}
		return c.out(t, func(w io.Writer) { fmt.Fprintf(w, "extended %s\n", oneLine(t)) })
	})
}

// unwatchable says why a keepalive can't watch pid, or "" if it can.
func unwatchable(pid int) string {
	switch {
	case !alive(pid):
		return "no such process of ours"
	case pid == os.Getpid() || endsWithCommand(pid):
		return "exits with this command; pass the harness process, your shell's parent"
	}
	return ""
}

// startKeepalive runs "maestro-ticket keepalive" for t's claim as a
// detached process, with the same store and environment.
func (c *call) startKeepalive(t ticket.Ticket, pid int, ttl time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	store, _, err := c.storePath()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "keepalive", t.ID.String(), "--holder", t.Claim.Holder,
		"--token", fmt.Sprint(t.Claim.Token), "--pid", fmt.Sprint(pid), "--ttl", ttl.String(), "--store", store)
	cmd.Dir = c.env.Dir
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// runKeepalive heartbeats one lease from a background process, so a long
// harness step can't outlast it. It exits 0 when the watched process is
// gone or on SIGINT/SIGTERM, leaving the lease to expire, and exit 4 once
// the lease is lost or released.
func runKeepalive(ctx context.Context, c *call) error {
	holder := c.fs.String("holder", "", "<bot-id>/<session-id> (required)")
	token := c.fs.Int64("token", 0, "lease token from your claim (required)")
	pid := c.fs.Int("pid", 0, "keep the lease while this process lives (required)")
	ttl := c.fs.Duration("ttl", ticket.DefaultTTL, "lease length from each heartbeat")
	every := c.fs.Duration("interval", 0, "heartbeat interval (default ttl/3)")
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		if *pid <= 0 {
			return usagef("keepalive: --pid is required")
		}
		if why := unwatchable(*pid); why != "" {
			return usagef("keepalive: --pid %d: %s", *pid, why)
		}
		interval := *every
		if interval == 0 {
			interval = *ttl / 3
		}
		if interval <= 0 || interval >= *ttl {
			return usagef("keepalive: --interval must be shorter than --ttl")
		}
		// Check the process often, so a dead session stops heartbeating
		// within a second, not a whole interval. next carries no monotonic
		// reading, so it is compared on the wall clock: after the machine
		// sleeps, the next check heartbeats at once, as a lease's expiry
		// runs on the wall clock too.
		check := min(interval, time.Second)
		next := time.Now().Round(0)
		for {
			if !alive(*pid) {
				return nil
			}
			if !time.Now().Before(next) {
				_, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: id, Holder: *holder, Token: *token, TTL: *ttl})
				switch {
				case ctx.Err() != nil:
					return nil
				case errors.Is(err, ticket.ErrLeaseLost), errors.Is(err, ticket.ErrInvalid), errors.Is(err, ticket.ErrNotFound):
					return err
				case err != nil:
					// Transient (a busy store, a full disk): try again soon,
					// while the lease still has time left.
					fmt.Fprintf(c.env.Stderr, "maestro-ticket keepalive: %v; retrying\n", err)
					next = time.Now().Add(min(interval, 5*time.Second)).Round(0)
				default:
					next = time.Now().Add(interval).Round(0)
				}
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(check):
			}
		}
	})
}

func runRelease(ctx context.Context, c *call) error {
	holder := c.fs.String("holder", "", "<bot-id>/<session-id> (required)")
	token := c.fs.Int64("token", 0, "lease token from your claim (required): release only that lease")
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		t, err := s.Release(ctx, ticket.ReleaseRequest{ID: id, Holder: *holder, Token: *token, Actor: c.actor, IdempotencyKey: c.idemKey})
		if err != nil {
			return err
		}
		return c.out(t, func(w io.Writer) { fmt.Fprintf(w, "released %s\n", oneLine(t)) })
	})
}

func runReap(ctx context.Context, c *call) error {
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		res, err := s.Reap(ctx)
		if err != nil {
			return err
		}
		// Reap is the store's upkeep: it also checkpoints the WAL on drivers
		// that have one.
		if cp, ok := s.(interface {
			Checkpoint(context.Context) (int, int, error)
		}); ok {
			if _, _, err := cp.Checkpoint(ctx); err != nil {
				fmt.Fprintf(c.env.Stderr, "maestro-ticket reap: checkpoint: %v\n", err)
			}
		}
		return c.out(res, func(w io.Writer) { printReap(w, res) })
	})
}

func runTransition(ctx context.Context, c *call) error {
	to := c.fs.String("to", "", "new status (required)")
	from := c.fs.String("from", "", "fail unless the ticket is in this status")
	expect := c.fs.Int64("expect-version", 0, "fail unless the ticket is at this version")
	holder := c.fs.String("holder", "", "your <bot-id>/<session-id>, required if the ticket is claimed")
	token := c.fs.Int64("token", 0, "lease token from your claim, required if the ticket is claimed")
	role := c.fs.String("role", "", "reassign to this role")
	note := c.fs.String("note", "", "why, recorded on the event")
	release := c.fs.Bool("release", false, "drop the claim in the same write")
	actorRole := c.fs.String("actor-role", c.env.Getenv("MAESTRO_TICKET_ROLE"), "the role acting")
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		if *to == "" {
			return usagef("transition: --to is required")
		}
		req := ticket.TransitionRequest{
			ID: id, To: ticket.Status(*to), From: ticket.Status(*from), ExpectVersion: *expect,
			Holder: *holder, Token: *token, Role: *role, ActorRole: *actorRole, Note: *note, Release: *release,
			Actor: c.actor, IdempotencyKey: c.idemKey,
		}
		t, err := s.Transition(ctx, req)
		if err != nil {
			return err
		}
		return c.out(t, func(w io.Writer) { fmt.Fprintf(w, "moved %s\n", oneLine(t)) })
	})
}

func runComment(ctx context.Context, c *call) error {
	body := c.fs.String("body", "", "comment text")
	bodyFile := c.fs.String("body-file", "", "read the comment from a file, or - for stdin")
	holder := c.fs.String("holder", "", "your <bot-id>/<session-id>, required if the ticket is claimed")
	token := c.fs.Int64("token", 0, "lease token from your claim, required if the ticket is claimed")
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		b, err := c.readBody(*body, *bodyFile)
		if err != nil {
			return err
		}
		cm, err := s.Comment(ctx, ticket.CommentRequest{
			ID: id, Body: b, Holder: *holder, Token: *token, Actor: c.actor, IdempotencyKey: c.idemKey,
		})
		if err != nil {
			return err
		}
		return c.out(cm, func(w io.Writer) { fmt.Fprintf(w, "commented on %s (comment %d)\n", cm.TicketID, cm.ID) })
	})
}

func runEvents(ctx context.Context, c *call) error {
	since := c.fs.Int64("since", 0, "only events after this seq")
	limit := c.fs.Int("limit", 0, "max events (0 = all)")
	tid := c.fs.String("ticket", "", "only this ticket")
	typ := c.fs.String("type", "", "only this event type")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		if *since < 0 {
			return usagef("events: --since must be >= 0")
		}
		f := ticket.EventFilter{Since: *since, Limit: *limit, Type: *typ}
		if *tid != "" {
			id, err := ticket.ParseID(*tid)
			if err != nil {
				return err
			}
			f.TicketID = id
		}
		evs, err := s.Events(ctx, f)
		if err != nil {
			return err
		}
		next := *since
		if len(evs) > 0 {
			next = evs[len(evs)-1].Seq
		}
		return c.out(map[string]any{"events": evs, "next_seq": next}, func(w io.Writer) { printEvents(w, evs, next) })
	})
}

// backupPrefix and backupSuffix frame snapshot names, and backupName
// matches them whole, so pruning only ever touches files backup wrote.
const backupPrefix, backupSuffix = "tickets-", ".db"

var backupName = regexp.MustCompile(`^tickets-\d{8}T\d{6}\.\d{3}Z\.db$`)

func runBackup(ctx context.Context, c *call) error {
	dir := c.fs.String("dir", "", "snapshot directory (default backups/ next to the store)")
	keep := c.fs.Int("keep", 24, "keep this many newest snapshots, delete older ones (0 = keep all)")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		if *keep < 0 {
			return usagef("backup: --keep must be >= 0")
		}
		store, _, err := c.storePath()
		if err != nil {
			return err
		}
		d := *dir
		if d == "" {
			d = filepath.Join(filepath.Dir(store), "backups")
		} else if !filepath.IsAbs(d) {
			d = filepath.Join(c.env.Dir, d)
		}
		now := c.env.Now
		if now == nil {
			now = time.Now
		}
		name := backupPrefix + now().UTC().Format("20060102T150405.000Z") + backupSuffix
		path := filepath.Join(d, name)
		if err := s.Backup(ctx, path); err != nil {
			return err
		}
		pruned, err := pruneBackups(d, *keep, store)
		if err != nil {
			return err
		}
		res := map[string]any{"path": path, "pruned": pruned}
		return c.out(res, func(w io.Writer) { fmt.Fprintf(w, "wrote %s (pruned %d)\n", path, len(pruned)) })
	})
}

// pruneBackups deletes all but the newest keep snapshots in dir, never the
// store itself, however its path is spelled. Names sort by time, so the
// newest sort last.
func pruneBackups(dir string, keep int, store string) ([]string, error) {
	pruned := []string{}
	if keep == 0 {
		return pruned, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	storeInfo, _ := os.Stat(store)
	var names []string
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		// A killed backup leaves <name>.partial. A live one keeps writing
		// to its own, so only an hour-old one is a leftover.
		if base, ok := strings.CutSuffix(e.Name(), ".partial"); ok && backupName.MatchString(base) {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
				p := filepath.Join(dir, e.Name())
				if err := os.Remove(p); err == nil {
					pruned = append(pruned, p)
				}
			}
			continue
		}
		if !backupName.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue // another backup pruned it first
		}
		if err != nil {
			return pruned, err
		}
		if storeInfo != nil && os.SameFile(info, storeInfo) {
			continue
		}
		names = append(names, filepath.Join(dir, e.Name()))
	}
	sort.Strings(names)
	for len(names) > keep {
		// Another backup may have pruned it first.
		if err := os.Remove(names[0]); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return pruned, err
		}
		pruned = append(pruned, names[0])
		names = names[1:]
	}
	return pruned, nil
}

func runExport(ctx context.Context, c *call) error {
	outPath := c.fs.String("out", "-", "output file, or - for stdout")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		ex, err := s.Export(ctx)
		if err != nil {
			return err
		}
		b, err := json.MarshalIndent(ex, "", "  ")
		if err != nil {
			return err
		}
		b = append(b, '\n')
		if *outPath == "-" {
			_, err := c.env.Stdout.Write(b)
			return err
		}
		path := *outPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.env.Dir, path)
		}
		if err := render.WriteAtomic(path, b); err != nil {
			return err
		}
		res := map[string]any{"path": path, "tickets": len(ex.Tickets), "last_seq": ex.LastSeq}
		return c.out(res, func(w io.Writer) {
			fmt.Fprintf(w, "wrote %s (%d tickets, seq %d)\n", path, len(ex.Tickets), ex.LastSeq)
		})
	})
}

func runRender(ctx context.Context, c *call) error {
	outPath := c.fs.String("out", "", "output file (default Backlog.md next to the store)")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		// One place per store, outside every work tree, so no branch can
		// commit it or conflict on it.
		path := *outPath
		if path == "" {
			store, _, err := c.storePath()
			if err != nil {
				return err
			}
			path = filepath.Join(filepath.Dir(store), "Backlog.md")
		} else if !filepath.IsAbs(path) {
			path = filepath.Join(c.env.Dir, path)
		}
		// Read the cursor first: the view reflects at least this seq.
		last, err := s.LastSeq(ctx)
		if err != nil {
			return err
		}
		list, err := s.List(ctx, ticket.ListFilter{})
		if err != nil {
			return err
		}
		if err := render.WriteAtomic(path, render.Backlog(list, last)); err != nil {
			return err
		}
		res := map[string]any{"path": path, "tickets": len(list), "last_seq": last}
		return c.out(res, func(w io.Writer) { fmt.Fprintf(w, "wrote %s (%d tickets, seq %d)\n", path, len(list), last) })
	})
}

// errDoctorFailed makes doctor exit non-zero after printing its report.
var errDoctorFailed = errors.New("doctor found problems")

func runDoctor(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	path, source, err := c.storePath()
	if err != nil {
		return err
	}
	rep := ticket.DoctorReport{Checks: []ticket.Check{{Name: "store", OK: true, Detail: fmt.Sprintf("%s (from %s)", path, source)}}}
	checks, err := c.doctorChecks(ctx, path)
	if err != nil {
		return err
	}
	rep.Checks = append(rep.Checks, checks...)
	res := map[string]any{"ok": rep.OK(), "store": path, "checks": rep.Checks}
	if err := c.out(res, func(w io.Writer) { printDoctor(w, rep) }); err != nil {
		return err
	}
	if !rep.OK() {
		if c.jsonOut {
			return silentExit{ticket.ExitInternal}
		}
		return errDoctorFailed
	}
	return nil
}

// doctorChecks runs the environment checks and, if they pass, the store's
// own. It never opens a store on a filesystem that can lose its writes.
func (c *call) doctorChecks(ctx context.Context, path string) ([]ticket.Check, error) {
	checks := c.envChecks(path)
	if envcheck.Err(checks) != nil {
		return checks, nil
	}
	s, err := sqlitestore.Open(ctx, path, sqlitestore.Options{Now: c.env.Now})
	if err != nil {
		return append(checks, ticket.Check{Name: "open", OK: false, Detail: err.Error()}), nil
	}
	defer s.Close()
	r, err := s.Doctor(ctx)
	return append(checks, r.Checks...), err
}

// silentExit carries an exit code without printing anything more.
type silentExit struct{ code int }

func (e silentExit) Error() string { return fmt.Sprintf("exit %d", e.code) }
