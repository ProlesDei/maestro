// Package cli is maestro-play's command line: flag parsing, config and
// output. The engine does the work.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-play/engine"
)

const usage = `maestro-play runs Maestro playbooks.

Usage:
  maestro-play run <playbook> [--ticket <id>] [--bot <bot-id>] [--input <text>]
  maestro-play resume <run-id>
  maestro-play render <playbook> --step <id> [--ticket <id> | --ticket-file <f>] [--env <f>] [--input <t>]
  maestro-play lint [<playbooks-dir>]

<playbook> is a folder in the playbooks folder: work runs
playbooks/work/playbook.yaml. On a playbook that claims a ticket, run with
no --ticket takes the first unclaimed ticket its intake matches. --bot
defaults to $MAESTRO_BOT, else user.

Flags for every command:
  --playbooks DIR   playbooks folder ($MAESTRO_PLAYBOOKS, else .maestro/src/playbooks
                    when Maestro was installed there, else ./playbooks)
  --skills DIR      skills folder with use.yaml ($MAESTRO_SKILLS, else <playbooks>/../skills)
  --common DIR      Maestro's state folder ($MAESTRO_COMMON, else $(git rev-parse --git-common-dir)/maestro)
  --ticket-bin P    maestro-ticket ($MAESTRO_TICKET_BIN, else maestro-ticket)
  --harness H       claude or codex runs prompt steps ($MAESTRO_HARNESS, else claude)

Environment: MAESTRO_GIT_BIN, MAESTRO_GH_BIN, MAESTRO_CLAUDE_BIN and
MAESTRO_CODEX_BIN set the binaries; MAESTRO_MODEL_MID and
MAESTRO_MODEL_FRONTIER the models per tier; MAESTRO_CLAUDE_ARGS and
MAESTRO_CODEX_ARGS extra session flags, split on spaces. Sessions may do
anything (claude --permission-mode bypassPermissions, codex
--dangerously-bypass-approvals-and-sandbox) unless those flags set
permissions, for example "--permission-mode acceptEdits".
MAESTRO_SESSION_TIMEOUT (30m), MAESTRO_CHECK_TIMEOUT (15m) and
MAESTRO_GIT_TIMEOUT (5m, each git or gh call) are Go durations; 0 is no
limit.

run and resume print one JSON object: id, state, playbook, ticket, step,
reason.

Exit codes: 0 done (lint: clean), 1 failed (lint: problems), 2 usage,
3 blocked, 4 lease lost, 7 stopped (<common>/STOP exists).
`

// Env is what the CLI reads from outside.
type Env struct {
	Args   []string
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	Dir    string
	// Engine adjusts the engine config before use (tests).
	Engine func(*engine.Config)
}

// Main runs one command and returns the exit code.
func Main(env Env) int {
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	if len(env.Args) == 0 || env.Args[0] == "-h" || env.Args[0] == "--help" || env.Args[0] == "help" {
		fmt.Fprint(env.Stdout, usage)
		if len(env.Args) == 0 {
			return 2
		}
		return 0
	}
	c := &cmd{env: env, fs: flag.NewFlagSet(env.Args[0], flag.ContinueOnError)}
	c.fs.SetOutput(env.Stderr)
	c.fs.StringVar(&c.playbooks, "playbooks", env.Getenv("MAESTRO_PLAYBOOKS"), "playbooks folder")
	c.fs.StringVar(&c.skills, "skills", env.Getenv("MAESTRO_SKILLS"), "skills folder")
	c.fs.StringVar(&c.common, "common", env.Getenv("MAESTRO_COMMON"), "Maestro's state folder")
	c.fs.StringVar(&c.ticketBin, "ticket-bin", env.Getenv("MAESTRO_TICKET_BIN"), "maestro-ticket binary")
	c.fs.StringVar(&c.harness, "harness", env.Getenv("MAESTRO_HARNESS"), "claude or codex")
	switch env.Args[0] {
	case "run":
		return c.run()
	case "resume":
		return c.resume()
	case "render":
		return c.render()
	case "lint":
		return c.lint()
	}
	fmt.Fprintf(env.Stderr, "maestro-play: unknown command %q\n\n%s", env.Args[0], usage)
	return 2
}

type cmd struct {
	env                                           Env
	fs                                            *flag.FlagSet
	playbooks, skills, common, ticketBin, harness string
	args                                          []string
}

// parse allows flags before and after the positional arguments.
func (c *cmd) parse() error {
	rest := c.env.Args[1:]
	for {
		if err := c.fs.Parse(rest); err != nil {
			return err
		}
		rest = c.fs.Args()
		if len(rest) == 0 {
			return nil
		}
		c.args = append(c.args, rest[0])
		rest = rest[1:]
	}
}

func (c *cmd) usageErr(format string, a ...any) int {
	fmt.Fprintf(c.env.Stderr, "maestro-play %s: %s\n", c.env.Args[0], fmt.Sprintf(format, a...))
	return 2
}

func (c *cmd) config() (engine.Config, error) {
	dir := c.env.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	// Commands run from the top of the checkout, where .maestro/ lives.
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			dir = top
		}
	}
	// Commands run in the main checkout, the git common dir's parent: it
	// holds .maestro/src, install.yaml, project-setup.md and the ticket
	// worktrees, so a run started in a linked worktree finds them there and
	// doesn't nest its worktrees in it.
	var common string
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--git-common-dir").Output(); err == nil {
		common = strings.TrimSpace(string(out))
		if !filepath.IsAbs(common) {
			common = filepath.Join(dir, common)
		}
	}
	main := dir
	if filepath.Base(common) == ".git" {
		main = filepath.Dir(common)
	}
	get := c.env.Getenv
	models := map[string]string{}
	for tier, key := range map[string]string{"mid": "MAESTRO_MODEL_MID", "frontier": "MAESTRO_MODEL_FRONTIER"} {
		if v := get(key); v != "" {
			models[tier] = v
		}
	}
	cfg := engine.Config{
		Playbooks: c.playbooks, Skills: c.skills, Common: c.common, Dir: main,
		TicketBin: c.ticketBin, GitBin: get("MAESTRO_GIT_BIN"), GhBin: get("MAESTRO_GH_BIN"),
		ClaudeBin: get("MAESTRO_CLAUDE_BIN"), CodexBin: get("MAESTRO_CODEX_BIN"), Harness: c.harness,
		Models: models, ClaudeArgs: strings.Fields(get("MAESTRO_CLAUDE_ARGS")), CodexArgs: strings.Fields(get("MAESTRO_CODEX_ARGS")),
	}
	for key, d := range map[string]*time.Duration{
		"MAESTRO_SESSION_TIMEOUT": &cfg.SessionTimeout, "MAESTRO_CHECK_TIMEOUT": &cfg.CheckTimeout, "MAESTRO_GIT_TIMEOUT": &cfg.GitTimeout,
	} {
		v := get(key)
		if v == "" {
			continue
		}
		t, err := time.ParseDuration(v)
		if err != nil || t < 0 {
			return cfg, fmt.Errorf("%s=%q: want a duration such as 30m, or 0 for no limit", key, v)
		}
		if t == 0 {
			t = -1 // the engine's no limit
		}
		*d = t
	}
	if cfg.Playbooks == "" {
		// The install guide clones Maestro into .maestro/src.
		cfg.Playbooks = filepath.Join(main, ".maestro", "src", "playbooks")
		if fi, err := os.Stat(cfg.Playbooks); err != nil || !fi.IsDir() {
			cfg.Playbooks = filepath.Join(dir, "playbooks")
		}
	}
	if cfg.Skills == "" {
		cfg.Skills = filepath.Join(filepath.Dir(filepath.Clean(cfg.Playbooks)), "skills")
	}
	if cfg.Common == "" {
		if common == "" {
			return cfg, errors.New("not in a git repo: pass --common or set MAESTRO_COMMON")
		}
		cfg.Common = filepath.Join(common, "maestro")
	}
	if c.env.Engine != nil {
		c.env.Engine(&cfg)
	}
	return cfg, nil
}

func (c *cmd) engine() (*engine.Engine, int) {
	cfg, err := c.config()
	if err != nil {
		return nil, c.usageErr("%v", err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		fmt.Fprintf(c.env.Stderr, "maestro-play: %v\n", err)
		return nil, 1
	}
	c.warnStale(cfg.Dir)
	return e, 0
}

// buildRevision is the commit this binary was built from, "" if unknown.
var buildRevision = func() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return ""
}

// warnStale warns when this binary was built from a commit older than the
// one the repo pins in .maestro/project/install.yaml: its engine may not
// match the playbooks in .maestro/src. One maestro-play serves every repo
// on the machine, so a newer build is fine.
func (c *cmd) warnStale(dir string) {
	rev, pin := buildRevision(), pinnedSHA(filepath.Join(dir, ".maestro", "project", "install.yaml"))
	if rev == "" || pin == "" || strings.HasPrefix(rev, pin) {
		return
	}
	src := filepath.Join(dir, ".maestro", "src")
	if exec.Command("git", "-C", src, "merge-base", "--is-ancestor", rev, pin).Run() != nil {
		return
	}
	fmt.Fprintf(c.env.Stderr, "maestro-play: warning: built from %.12s, older than the pinned %s; rebuild it from .maestro/src/tools/maestro-play\n", rev, pin)
}

// pinnedSHA is the maestro-sha: line of an install.yaml, "" if none.
func pinnedSHA(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "maestro-sha:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (c *cmd) report(r engine.Result, err error) int {
	if err != nil {
		r = engine.Result{Status: engine.StatusFailed, Reason: err.Error()}
	}
	b, _ := json.Marshal(r)
	fmt.Fprintln(c.env.Stdout, string(b))
	return r.ExitCode()
}

func (c *cmd) run() int {
	ticketID := c.fs.String("ticket", "", "ticket id; empty takes the next one intake matches")
	bot := c.fs.String("bot", "", "bot id ($MAESTRO_BOT, else user)")
	input := c.fs.String("input", "", "text for a playbook with input: (.input.text)")
	if err := c.parse(); err != nil {
		return 2
	}
	if len(c.args) != 1 {
		return c.usageErr("want one <playbook>")
	}
	if *bot == "" {
		*bot = c.env.Getenv("MAESTRO_BOT")
	}
	if *bot == "" {
		*bot = "user"
	}
	e, code := c.engine()
	if e == nil {
		return code
	}
	return c.report(e.Run(engine.RunOpts{Ref: c.args[0], Ticket: *ticketID, Bot: *bot, Input: *input}))
}

func (c *cmd) resume() int {
	if err := c.parse(); err != nil {
		return 2
	}
	if len(c.args) != 1 {
		return c.usageErr("want one <run-id>")
	}
	e, code := c.engine()
	if e == nil {
		return code
	}
	return c.report(e.Resume(c.args[0]))
}

func (c *cmd) render() int {
	o := engine.RenderOpts{}
	c.fs.StringVar(&o.Step, "step", "", "step id, done (the note) or escalate (required)")
	c.fs.StringVar(&o.Ticket, "ticket", "", "ticket id, read from the store")
	c.fs.StringVar(&o.TicketFile, "ticket-file", "", "a maestro-ticket get --json document instead of the store")
	c.fs.StringVar(&o.EnvFile, "env", "", "JSON laid over .env, with project and limits keys for .project and .limits")
	c.fs.StringVar(&o.Bot, "bot", "", "bot id for .run (default render)")
	c.fs.StringVar(&o.Input, "input", "", "text for .input.text")
	if err := c.parse(); err != nil {
		return 2
	}
	if len(c.args) != 1 || o.Step == "" {
		return c.usageErr("want <playbook> --step <id>")
	}
	o.Ref = c.args[0]
	e, code := c.engine()
	if e == nil {
		return code
	}
	out, err := e.Render(o)
	if err != nil {
		fmt.Fprintf(c.env.Stderr, "maestro-play render: %v\n", err)
		return 1
	}
	fmt.Fprint(c.env.Stdout, out)
	return 0
}

func (c *cmd) lint() int {
	if err := c.parse(); err != nil {
		return 2
	}
	if len(c.args) > 1 {
		return c.usageErr("want at most one playbooks folder")
	}
	if len(c.args) == 1 {
		c.playbooks = c.args[0]
	}
	cfg, err := c.config()
	if err != nil && cfg.Playbooks == "" {
		return c.usageErr("%v", err)
	}
	res, err := engine.Lint(cfg.Playbooks, cfg.Skills)
	if err != nil {
		fmt.Fprintf(c.env.Stderr, "maestro-play lint: %v\n", err)
		return 1
	}
	for _, p := range res.Problems {
		fmt.Fprintln(c.env.Stdout, p.String())
	}
	fmt.Fprintf(c.env.Stdout, "%d playbooks, %d problems\n", res.Playbooks, len(res.Problems))
	if len(res.Problems) > 0 {
		return 1
	}
	return 0
}
