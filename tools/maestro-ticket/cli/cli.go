// Package cli implements the maestro-ticket command line on top of ticket.Store.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	Now    func() time.Time // clock for the store; nil means time.Now
}

// OSEnv returns an Env wired to the real process.
func OSEnv() Env {
	dir, _ := os.Getwd()
	return Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Dir: dir}
}

// Main runs the CLI against the real process and returns the exit code.
func Main(args []string) int { return Run(context.Background(), args, OSEnv()) }

const usage = `maestro-ticket: the maestro ticket store.

Usage: maestro-ticket <command> [flags]

Commands:
  create      --title T [--body B | --body-file F|-] [--role R] [--priority 0-3] [--status todo|blocked]
  get         <id>
  list        [--status S,...] [--role R] [--holder H] [--claimed | --unclaimed] [--limit N]
  claim       (--next | <id>) --holder <bot-id>/<session-id> [--status S,...] [--role R] [--ttl 30m]
  heartbeat   <id> --holder H [--ttl 30m]
  release     <id> --holder H
  transition  <id> --to S [--from S] [--expect-version N] [--holder H] [--role R] [--note T] [--release]
  comment     <id> (--body B | --body-file F|-)
  events      [--since SEQ] [--limit N] [--ticket ID] [--type T]
  render      [--out PATH]
  doctor

Flags on every command:
  --json                  machine-readable output (errors too, on stdout)
  --store PATH            ticket store (default $MAESTRO_TICKET_STORE, else
                          $(git rev-parse --git-common-dir)/maestro/tickets.db)
  --actor NAME            who is acting (default $MAESTRO_TICKET_ACTOR)
  --idempotency-key K     on create, claim, release, transition, comment:
                          a retry with the same key and request replays the result

Statuses: todo in_progress in_review in_qa ready_to_release blocked done cancelled
Exit codes: 0 ok, 1 internal, 2 usage/invalid, 3 not found, 4 conflict, 5 no work
`

type command struct {
	name string
	run  func(ctx context.Context, c *call) error
}

var commands = []command{
	{"create", runCreate},
	{"get", runGet},
	{"list", runList},
	{"claim", runClaim},
	{"heartbeat", runHeartbeat},
	{"release", runRelease},
	{"transition", runTransition},
	{"comment", runComment},
	{"events", runEvents},
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

// parse parses flags, allowing positional arguments anywhere.
func (c *call) parse() error {
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
	return nil
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
// common dir, which every worktree of a repo shares.
func (c *call) storePath() (path, source string, err error) {
	if c.storeFlag != "" {
		return c.storeFlag, "--store", nil
	}
	if p := c.env.Getenv("MAESTRO_TICKET_STORE"); p != "" {
		return p, "MAESTRO_TICKET_STORE", nil
	}
	common, err := c.git("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", fmt.Errorf("%w: no --store or MAESTRO_TICKET_STORE, and not in a git repo: %v", ticket.ErrInvalid, err)
	}
	return filepath.Join(common, "maestro", "tickets.db"), "git common dir", nil
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
		return body, nil
	}
	var b []byte
	var err error
	if file == "-" {
		b, err = io.ReadAll(c.env.Stdin)
	} else {
		b, err = os.ReadFile(file)
	}
	return string(b), err
}

func runCreate(ctx context.Context, c *call) error {
	title := c.fs.String("title", "", "one-line title (required)")
	body := c.fs.String("body", "", "ticket body")
	bodyFile := c.fs.String("body-file", "", "read the body from a file, or - for stdin")
	role := c.fs.String("role", "", "role that should pick it up")
	prio := c.fs.Int("priority", ticket.DefaultPriority, "0 (most urgent) to 3")
	status := c.fs.String("status", string(ticket.StatusTodo), "todo or blocked")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		b, err := c.readBody(*body, *bodyFile)
		if err != nil {
			return err
		}
		st, err := ticket.ParseStatus(*status)
		if err != nil {
			return err
		}
		t, err := s.Create(ctx, ticket.CreateRequest{
			Title: *title, Body: b, Role: *role, Priority: prio, Status: st,
			Actor: c.actor, IdempotencyKey: c.idemKey,
		})
		if err != nil {
			return err
		}
		return c.out(t, func(w io.Writer) { fmt.Fprintf(w, "created %s\n", oneLine(t)) })
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
	limit := c.fs.Int("limit", 0, "max results (0 = all)")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		f := ticket.ListFilter{Role: *role, Holder: *holder, Limit: *limit}
		var err error
		if f.Statuses, err = ticket.ParseStatuses(*status); err != nil {
			return err
		}
		switch {
		case *claimed && *unclaimed:
			return usagef("use --claimed or --unclaimed, not both")
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
		return c.out(map[string]any{"tickets": list}, func(w io.Writer) { printList(w, list) })
	})
}

func runClaim(ctx context.Context, c *call) error {
	next := c.fs.Bool("next", false, "claim the most urgent matching ticket")
	holder := c.fs.String("holder", "", "<bot-id>/<session-id> (required)")
	status := c.fs.String("status", "", "statuses to claim from (default todo for --next)")
	role := c.fs.String("role", "", "only tickets for this role")
	ttl := c.fs.Duration("ttl", ticket.DefaultTTL, "lease length")
	return c.withStore(ctx, func(s ticket.Store) error {
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
		return c.out(t, func(w io.Writer) { fmt.Fprintf(w, "claimed %s\n", oneLine(t)) })
	})
}

func runHeartbeat(ctx context.Context, c *call) error {
	holder := c.fs.String("holder", "", "<bot-id>/<session-id> (required)")
	ttl := c.fs.Duration("ttl", ticket.DefaultTTL, "new lease length from now")
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		if c.idemKey != "" {
			return usagef("heartbeat: --idempotency-key not supported; a heartbeat must always extend from now")
		}
		t, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: id, Holder: *holder, TTL: *ttl})
		if err != nil {
			return err
		}
		return c.out(t, func(w io.Writer) { fmt.Fprintf(w, "extended %s\n", oneLine(t)) })
	})
}

func runRelease(ctx context.Context, c *call) error {
	holder := c.fs.String("holder", "", "<bot-id>/<session-id> (required)")
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		t, err := s.Release(ctx, ticket.ReleaseRequest{ID: id, Holder: *holder, Actor: c.actor, IdempotencyKey: c.idemKey})
		if err != nil {
			return err
		}
		return c.out(t, func(w io.Writer) { fmt.Fprintf(w, "released %s\n", oneLine(t)) })
	})
}

func runTransition(ctx context.Context, c *call) error {
	to := c.fs.String("to", "", "new status (required)")
	from := c.fs.String("from", "", "fail unless the ticket is in this status")
	expect := c.fs.Int64("expect-version", 0, "fail unless the ticket is at this version")
	holder := c.fs.String("holder", "", "your <bot-id>/<session-id>, required if the ticket is claimed")
	role := c.fs.String("role", "", "reassign to this role")
	note := c.fs.String("note", "", "why, recorded on the event")
	release := c.fs.Bool("release", false, "drop the claim in the same write")
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
			Holder: *holder, Role: *role, Note: *note, Release: *release,
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
	return c.withStore(ctx, func(s ticket.Store) error {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		b, err := c.readBody(*body, *bodyFile)
		if err != nil {
			return err
		}
		cm, err := s.Comment(ctx, ticket.CommentRequest{ID: id, Body: b, Actor: c.actor, IdempotencyKey: c.idemKey})
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

func runRender(ctx context.Context, c *call) error {
	outPath := c.fs.String("out", "", "output file (default <repo>/.maestro/Backlog.md)")
	return c.withStore(ctx, func(s ticket.Store) error {
		if err := c.noArgs(); err != nil {
			return err
		}
		path := *outPath
		if path == "" {
			top, err := c.git("rev-parse", "--show-toplevel")
			if err != nil {
				return fmt.Errorf("%w: no --out and not in a git work tree: %v", ticket.ErrInvalid, err)
			}
			path = filepath.Join(top, ".maestro", "Backlog.md")
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
	s, err := sqlitestore.Open(ctx, path, sqlitestore.Options{Now: c.env.Now})
	if err != nil {
		rep.Checks = append(rep.Checks, ticket.Check{Name: "open", OK: false, Detail: err.Error()})
	} else {
		defer s.Close()
		r, err := s.Doctor(ctx)
		if err != nil {
			return err
		}
		rep.Checks = append(rep.Checks, r.Checks...)
	}
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

// silentExit carries an exit code without printing anything more.
type silentExit struct{ code int }

func (e silentExit) Error() string { return fmt.Sprintf("exit %d", e.code) }
