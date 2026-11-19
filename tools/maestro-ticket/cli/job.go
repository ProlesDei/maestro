package cli

import (
	"bufio"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// Models a job runs on. Every job runs at high effort: the two settings a
// team tests and pays for (owner's ruling, Nov 14 2026).
const (
	modelOpus   = "opus"
	modelSonnet = "sonnet"
	effortHigh  = "high"
)

// jobKind is one kind of work a bot hands to Claude Code.
type jobKind struct {
	name string
	// skill and prompt build the prompt: the skill's command, then the
	// ticket id, then the bot's text from stdin when text is set.
	skill  string
	prefix string // words between the skill and the ticket id
	ticket bool   // takes one ticket id
	ids    bool   // takes one or more ticket ids (judge)
	text   string // "need": stdin text is required; "" for none
	model  string
	// lean runs run none of the project's checks, so a 5-minute cache
	// costs less (team rules).
	lean bool
	// slot names the slot the run waits in, at most --slots at once: each
	// QA run holds its own copy of the app and a browser.
	slot string
	// stopHook runs stop-check as the session's Stop hook (stopcheck.go):
	// a job whose end is a note on its ticket.
	stopHook bool
	help     string
}

var jobKinds = []jobKind{
	{name: "build", skill: "/maestro-software-engineer", ticket: true, model: "tier", stopHook: true,
		help: "build <id>: the ticket's engineer session, Opus (senior tier or none) or Sonnet (junior, until it is sent back twice)"},
	{name: "review", skill: "/maestro-code-review", ticket: true, model: "route", stopHook: true,
		help: "review <id> [--approved SHA] [--drop IDS --drop-file F|-] [--full]: a fresh review, Sonnet on a docs-only PR, else Opus; limit sized to the diff"},
	{name: "qa", skill: "/maestro-qa-lead", ticket: true, model: modelOpus, slot: "qa", stopHook: true,
		help: "qa <id>: test the reviewed PR and merge it on a pass"},
	{name: "live", skill: "/maestro-qa-lead", prefix: "live", ticket: true, model: modelOpus, slot: "qa", stopHook: true,
		help: "live <id>: a goal's live run"},
	{name: "qa-ask", skill: "/maestro-qa-lead", text: "need", model: modelOpus, slot: "qa",
		help: "qa-ask [--ticket T-1] < ask: a second look, a repro, a test plan"},
	{name: "plan", skill: "/maestro-planner", text: "need", model: modelOpus, lean: true,
		help: "plan [--ticket T-1] < ask: plan a goal, split or rewrite, read the board"},
	// A ruling may split, rewrite or plan an approved design's screens:
	// planning, so Opus.
	{name: "rule", skill: "/maestro-planner", prefix: "rule", ticket: true, text: "need", model: modelOpus, lean: true,
		help: "rule <id> < decision: post the planner's ruling and move the ticket"},
	{name: "finish", skill: "/maestro-planner", prefix: "finish", ticket: true, model: modelSonnet, lean: true,
		help: "finish <id>: finish a split, drop or merge that stopped short"},
	{name: "judge", skill: "/maestro-planner", prefix: "judge", ids: true, text: "need", model: modelOpus, lean: true,
		help: "judge <id>... < rows and standing rules: the planner's calls that need reading"},
	{name: "design", skill: "/maestro-designer", prefix: "design", ticket: true, model: modelOpus, lean: true, stopHook: true,
		help: "design <id>: a goal's Design: ticket, new or sent back with changes"},
	{name: "cos", skill: "/maestro-chief-of-staff", text: "need", model: modelOpus, lean: true,
		help: "cos [--ticket T-1] < ask: a check, second opinion, audit or card draft"},
	// Reads the standing rules and their log, costs and flow, and writes
	// nothing: its reply is up to 3 proposals for the owner's yes.
	{name: "retro", skill: "/maestro-retro", model: modelOpus, lean: true,
		help: "retro: up to 3 proposals that turn standing rules into code, a lint or a script; it changes nothing"},
}

func findJob(name string) (jobKind, bool) {
	for _, k := range jobKinds {
		if k.name == name {
			return k, true
		}
	}
	return jobKind{}, false
}

func jobNames() string {
	n := make([]string, len(jobKinds))
	for i, k := range jobKinds {
		n[i] = k.name
	}
	return strings.Join(n, ", ")
}

// claudeCommand is the claude -p command line of a job, with the
// environment words the team rules ask of every run.
func claudeCommand(session, model, effort string, lean bool) []string {
	cmd := []string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"}
	if lean {
		cmd = append(cmd, "CLAUDE_CODE_PROMPT_CACHE_TTL=5m")
	}
	return append(cmd, "claude", "-p", "--session-id", session, "--model", model, "--effort", effort,
		"--permission-mode", "bypassPermissions", "--output-format", "json")
}

// defaultQASlots is how many QA runs go at once on one store unless the
// QA lead's memory sets another.
const defaultQASlots = 6

// inSlot runs claude through maestro-ticket slot, after the environment
// words, so a run waits in line for one of max slots; its time limit
// leaves out the wait.
func inSlot(cmd []string, name string, max int) []string {
	exe, err := os.Executable()
	if err != nil {
		exe = "maestro-ticket"
	}
	i := slices.Index(cmd, "claude")
	out := slices.Clone(cmd[:i])
	out = append(out, exe, "slot", "--name", name, "--max", strconv.Itoa(max), "--")
	return append(out, cmd[i:]...)
}

// newSessionID is a random UUID, version 4, for claude --session-id: each
// run gets its own, so a run that dies with no reply can still be resumed.
func newSessionID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// reviewLimit sizes a review's time limit to its diff: 45 minutes, plus 15
// for each 200 changed lines, at most 2 hours.
func reviewLimit(lines int) time.Duration {
	return min(45*time.Minute+time.Duration(lines/200)*15*time.Minute, 2*time.Hour)
}

// route is what review-prep.sh --route printed.
type route struct {
	light bool
	why   string
	lines int // -1 when it didn't say
}

// reviewRoute runs the review skill's prep script in route mode, which
// reads only the PR's file list: route=light when every file is docs or
// inert config, and the PR's changed lines.
func (c *call) reviewRoute(ctx context.Context, target string) (route, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return route{lines: -1, why: "no gh to read the PR's files"}, nil // full, by default
	}
	top, err := c.git("rev-parse", "--show-toplevel")
	if err != nil {
		return route{}, fmt.Errorf("%w: job review: run it inside the repo: %v", ticket.ErrInvalid, err)
	}
	root := strings.TrimSpace(top)
	rel := filepath.Join(".claude", "skills", "maestro-code-review", "scripts", "review-prep.sh")
	script := filepath.Join(root, rel)
	if !fileExists(script) {
		// Skills hidden from git live in the main checkout only.
		if common, err := c.git("rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
			if alt := filepath.Join(filepath.Dir(strings.TrimSpace(common)), rel); fileExists(alt) {
				script = alt
			}
		}
	}
	cmd := exec.CommandContext(ctx, "bash", script, "--route", target)
	cmd.Dir = strings.TrimSpace(root)
	// A gh the script started keeps stdout open after ctx kills bash.
	cmd.WaitDelay = 5 * time.Second
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 3 {
			return route{}, fmt.Errorf("%w: job review %s: %s", ticket.ErrNotFound, target, msg)
		}
		if errors.Is(err, fs.ErrNotExist) || strings.Contains(msg, "No such file") {
			return route{}, fmt.Errorf("%w: job review: no %s: install Maestro's skills in this repo", ticket.ErrInvalid, script)
		}
		return route{}, fmt.Errorf("%w: job review %s: review-prep.sh --route: %v: %s", ticket.ErrInvalid, target, err, msg)
	}
	r := route{lines: -1}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "route":
			r.light = v == "light"
		case "why":
			r.why = v
		case "lines":
			if n, err := strconv.Atoi(v); err == nil {
				r.lines = n
			}
		}
	}
	return r, nil
}

// jobReq is one job to start, as runJob parses it or look builds it.
type jobReq struct {
	kind          jobKind
	ids           []string // ticket ids, as T-<n>
	ticket        string   // a text job's ticket
	text          string   // the bot's text, for a text job
	approved      string   // review
	drop, dropWhy string   // review
	full          bool     // review
	model, effort string   // overrides
	limit         time.Duration
	slots         int  // QA runs at once; 0 for the default
	inherit       bool // a start no bot typed: it takes the launcher's last overrides
	chainOf       string
	hops          int // a chained start: the run whose end starts it, and its hops
}

// runJob starts one bot job as a Claude Code run: it builds the prompt,
// picks the model, effort and time limit, gives the run a session id,
// and launches it as launch does. A bot spends one step on it.
func runJob(ctx context.Context, c *call) error {
	model := c.fs.String("model", "", "override the job's model (opus or sonnet as a rule)")
	effort := c.fs.String("effort", "", "override the effort (default high)")
	limit := c.fs.Duration("limit", 0, "override the time limit (default 2h; a review's is sized to its diff)")
	tk := c.fs.String("ticket", "", "the ticket a text job (qa-ask, plan, cos) works on")
	approved := c.fs.String("approved", "", "review: the head sha the owner said yes to")
	drop := c.fs.String("drop", "", "review: the finding ids a ruling dropped; the reason comes from --drop-file")
	dropFile := c.fs.String("drop-file", "", "review: read why the findings dropped from this file, or - for stdin")
	full := c.fs.Bool("full", false, "review: on the full model, whatever the route")
	slots := c.fs.Int("slots", 0, "qa, live, qa-ask: QA runs at once on this store (default 6); the rest wait in line")
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) == 0 {
		return usagef("job: name the job: %s", jobNames())
	}
	if c.args[0] == "again" {
		if err := c.runAgain(ctx, *limit, *full); !c.goingOn(err) {
			return err
		}
		return nil
	}
	k, ok := findJob(c.args[0])
	if !ok {
		return usagef("job: unknown job %q: want %s", c.args[0], jobNames())
	}
	req := jobReq{kind: k, ids: c.args[1:], approved: *approved, drop: *drop, full: *full, model: *model, effort: *effort, limit: *limit, slots: *slots}
	if *slots != 0 && k.slot == "" {
		return usagef("job %s: --slots is for qa, live and qa-ask", k.name)
	}
	if *slots < 0 {
		return usagef("job: --slots must be 1 or more")
	}
	if !runs.Supported {
		return usagef("job needs a unix system")
	}
	if err := c.needActor("job"); err != nil {
		return err
	}
	if k.name != "review" && (*approved != "" || *drop != "" || *dropFile != "" || *full) {
		return usagef("job %s: --approved, --drop, --drop-file and --full are for review", k.name)
	}
	if (*drop == "") != (*dropFile == "") {
		return usagef("job review: --drop names the findings and --drop-file gives why: pass both")
	}
	if *tk != "" {
		t, err := ticket.ParseID(*tk)
		if err != nil {
			return err
		}
		req.ticket = t.String()
	}
	if k.text != "" || *dropFile == "-" {
		b, err := c.readStdin()
		if err != nil {
			return err
		}
		req.text = strings.TrimSpace(string(b))
	}
	if *dropFile != "" {
		if *dropFile == "-" {
			req.dropWhy, req.text = req.text, ""
		} else {
			b, err := c.readBody("", *dropFile)
			if err != nil {
				return err
			}
			req.dropWhy = b
		}
	}
	r, m, why, err := c.startJob(ctx, req)
	if c.goingOn(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.printStarted(r, m, why)
}

// startJob checks a job, builds its prompt and command, and starts it. It
// returns the run, its model and why that model.
func (c *call) startJob(ctx context.Context, req jobReq) (runs.Record, string, string, error) {
	k, ids := req.kind, slices.Clone(req.ids)
	fail := func(err error) (runs.Record, string, string, error) { return runs.Record{}, "", "", err }
	if err := c.admit(); err != nil {
		return fail(err)
	}
	switch {
	case k.ticket && len(ids) != 1:
		return fail(usagef("job %s: want one ticket id", k.name))
	case k.ids && len(ids) == 0:
		return fail(usagef("job %s: want one or more ticket ids", k.name))
	case !k.ticket && !k.ids && len(ids) > 0:
		return fail(usagef("job %s: takes no ticket id; give it as --ticket, and the ask on stdin", k.name))
	case k.text == "need" && req.text == "":
		return fail(usagef("job %s: send the ask on stdin, in a quoted heredoc", k.name))
	case req.approved != "" && !shaPattern.MatchString(req.approved):
		return fail(usagef("job review: --approved %q: want a commit sha", req.approved))
	case req.drop != "" && !dropPattern.MatchString(req.drop):
		return fail(usagef("job review: --drop %q: want finding ids such as F1,F3", req.drop))
	}
	for i, id := range ids {
		t, err := ticket.ParseID(id)
		if err != nil {
			return fail(err)
		}
		ids[i] = t.String()
	}
	ticketID := req.ticket
	switch {
	case k.ticket:
		ticketID = ids[0]
	case k.ids && ticketID != "":
		return fail(usagef("job %s: the ids name its tickets; drop --ticket", k.name))
	}
	dropWhy := oneLineText(req.dropWhy)
	if req.drop != "" && dropWhy == "" {
		return fail(usagef("job review: --drop-file is empty: say why the findings dropped"))
	}
	if ticketID != "" {
		if err := c.jobFits(ctx, k, ticketID); err != nil {
			return fail(err)
		}
	}

	m, why := k.model, ""
	side := &sideFacts{Why: "fixed"}
	runLimit := 2 * time.Hour
	words := []string{k.skill}
	if k.prefix != "" {
		words = append(words, k.prefix)
	}
	switch k.model {
	case "tier":
		t, err := c.ticketForJob(ctx, ticketID)
		if err != nil {
			return fail(err)
		}
		// A junior's ticket sent back twice goes to the senior, on Opus.
		m, why = modelOpus, "tier "+dash(t.Tier)
		side.fromTicket(t)
		side.Why = "tier_" + side.Tier
		if t.Tier == ticket.TierJunior && t.Tries < 2 {
			m = modelSonnet
		} else if t.Tier == ticket.TierJunior {
			why += fmt.Sprintf(", sent back %d times", t.Tries)
			side.Why = "sent_back"
		}
	case "route":
		t, err := c.ticketForJob(ctx, ticketID)
		if err != nil {
			return fail(err)
		}
		side.fromTicket(t)
		r, err := c.reviewRoute(ctx, ticketID)
		if err != nil {
			return fail(err)
		}
		m, why = modelOpus, "full review"
		side.Why = "route_full"
		if r.light && !req.full {
			m, why = modelSonnet, "light review: "+r.why
			side.Why = "route_light"
		}
		if r.lines >= 0 {
			runLimit = reviewLimit(r.lines)
		}
	default:
		if ticketID != "" {
			t, err := c.ticketForJob(ctx, ticketID)
			if err != nil {
				return fail(err)
			}
			side.fromTicket(t)
		}
	}
	words = append(words, ids...)
	if k.name == "review" {
		if m == modelSonnet {
			words = append(words, "--light")
		}
		if req.approved != "" {
			words = append(words, "--approved", req.approved)
		}
		if req.drop != "" {
			words = append(words, "--drop", req.drop+":", dropWhy)
			side.Drops = strings.Split(req.drop, ",")
		}
	}
	prompt := strings.Join(words, " ")
	switch {
	case k.name == "rule":
		prompt += ": " + req.text
	case k.ids && req.text != "":
		prompt += "\n" + req.text // judge: the rows and rules follow the ids
	case k.text != "" && req.text != "":
		prompt += " " + req.text
	}
	if k.name == "plan" || k.name == "rule" || k.name == "retro" {
		if rules, _ := c.standingRules(); rules != "" {
			prompt += "\nStanding rules:\n" + rules
		}
	}
	if req.inherit && req.model == "" && req.effort == "" && req.limit == 0 {
		// What was picked for this ticket, its tier's model or a review's
		// model and size, stands: a carried flag replaces only a fixed
		// default.
		model, effort, limit, err := c.lastOverrides(k.name)
		if err != nil {
			return fail(err)
		}
		if k.model != "tier" && k.model != "route" {
			req.model = model
		}
		if k.model != "route" {
			req.limit = limit
		}
		req.effort = effort
	}
	var overrides []string
	if req.model != "" {
		if req.model != m {
			side.Why = "override"
		}
		m = req.model
		overrides = append(overrides, "--model", req.model)
	}
	if req.effort != "" {
		overrides = append(overrides, "--effort", req.effort)
	}
	if req.limit != 0 {
		overrides = append(overrides, "--limit", req.limit.String())
	}
	e := effortHigh
	if req.effort != "" {
		e = req.effort
	}
	if req.limit != 0 {
		runLimit = req.limit
	}
	if runLimit < time.Second || runLimit > 24*time.Hour {
		return fail(usagef("job: --limit %s: want 1s to 24h", runLimit))
	}
	cmd, err := c.stopHook(claudeCommand(newSessionID(), m, e, k.lean), k.name)
	if err != nil {
		return fail(err)
	}
	if k.slot != "" {
		cmd = inSlot(cmd, k.slot, cmp.Or(req.slots, c.boardQASlots(), defaultQASlots))
	}
	rec := runs.Record{
		Ticket: ticketID, Limit: short(runLimit), Job: k.name,
		Command: withEnv(cmd), ChainOf: req.chainOf, Hops: req.hops, Overrides: overrides,
	}
	if k.ids {
		rec.Tickets = ids
	}
	var commit func(string) error
	abort := func() {}
	if ticketID != "" {
		dir, err := c.besideStore("runs")
		if err != nil {
			return fail(err)
		}
		co, err := c.coord()
		if err != nil {
			return fail(err)
		}
		if commit, abort, err = co.takeActive(dir, ticketID, k.name, c.actor, runLimit); err != nil {
			return fail(fmt.Errorf("job %s %s: %w", k.name, ticketID, err))
		}
	}
	side.PromptBytes = ptr(len(prompt) + 1)
	r, err := c.startRun(rec, []byte(prompt+"\n"), runs.DefaultRings, side, commit)
	if err != nil {
		abort()
		return fail(err)
	}
	return r, m, why, nil
}

var (
	shaPattern  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	dropPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(,[A-Za-z0-9._-]+)*$`)
)

// jobStatuses are the statuses a job's ticket may be in: a job on any
// other only refuses once its session starts, after it spent the start.
var jobStatuses = map[string][]ticket.Status{
	"build":  {ticket.StatusTodo, ticket.StatusInProgress},
	"review": {ticket.StatusInReview},
	"qa":     {ticket.StatusInReview},
	"live":   {ticket.StatusTodo, ticket.StatusInProgress},
	"design": {ticket.StatusTodo, ticket.StatusInProgress},
}

// jobFits refuses a job on a ticket in a status it can't work, and a
// second run of one job on one ticket while the first goes: of two bots
// handed the same job, one runs it.
func (c *call) jobFits(ctx context.Context, k jobKind, id string) error {
	if want, ok := jobStatuses[k.name]; ok {
		t, err := c.ticketForJob(ctx, id)
		if err != nil {
			return err
		}
		if !slices.Contains(want, t.Status) {
			return fmt.Errorf("%w: job %s: %s is %s, not %s: nothing to do", ticket.ErrStatusConflict, k.name, id, t.Status, joinStatuses(want))
		}
		if k.name == "review" {
			if d, err := c.detailForJob(ctx, id); err == nil && strings.HasPrefix(latest(d).Body, "Review: GO") {
				return fmt.Errorf("%w: job review: %s passed review at its head: its GO stands, and QA takes it", ticket.ErrStatusConflict, id)
			}
		}
	}
	// A run started before job marked its runs, or by plain launch, holds
	// the ticket too.
	if dir, err := c.besideStore("runs"); err == nil {
		list, _ := runs.List(dir, time.Now(), false)
		for _, r := range list {
			if !r.Ended() && r.Ticket == id && (r.Job == k.name || r.Job == "") {
				if r.Launcher == c.actor && r.Job == k.name {
					return alreadyGoing{run: r.ID}
				}
				return fmt.Errorf("%w: job %s: run %s, %s's, already works on %s", ticket.ErrClaimed, k.name, r.ID, r.Launcher, id)
			}
		}
	}
	return nil
}

// resumeFits refuses to resume a session whose ticket moved on without
// it: a build or design whose ticket is in review or closed, a review or
// QA run whose ticket left review. A blocked ticket is fine: a session
// that asked resumes on it.
func (c *call) resumeFits(ctx context.Context, r runs.Record) error {
	if r.Ticket == "" {
		return nil
	}
	t, err := c.ticketForJob(ctx, r.Ticket)
	if err != nil {
		return err
	}
	moved := t.Status.Terminal()
	switch r.Job {
	case "build", "design":
		moved = moved || t.Status == ticket.StatusInReview
	case "review", "qa":
		moved = moved || (t.Status != ticket.StatusInReview && t.Status != ticket.StatusBlocked)
	}
	if moved {
		return fmt.Errorf("%w: resume: %s is %s now: run %s's session has nothing left to do there", ticket.ErrStatusConflict, r.Ticket, t.Status, r.ID)
	}
	return nil
}

// detailForJob reads a job's ticket with its notes.
func (c *call) detailForJob(ctx context.Context, id string) (ticket.Detail, error) {
	tid, err := ticket.ParseID(id)
	if err != nil {
		return ticket.Detail{}, err
	}
	s, err := c.open(ctx)
	if err != nil {
		return ticket.Detail{}, err
	}
	defer s.Close()
	return s.Get(ctx, tid)
}

// activeName marks the one live run of a job on a ticket: made exclusive
// before the run starts, it holds the run's id, and the run's end
// removes it. Of starts at once only one makes it.
func activeName(ticketID, job string) string { return ticketID + "." + job + ".active" }

// takeActive makes a ticket's job mark for a run about to start, or names
// the live run that holds it. A mark left by a run that ended is taken
// over. dir holds the runs; limit is the new run's.
//
// On a shared store the mark says whose run it is, as an activeMark: a
// run on another machine has no record here to read, so it holds until
// it is overdue, past any limit its own supervisor would let it run.
func (co coord) takeActive(dir, ticketID, job, actor string, limit time.Duration) (commit func(runID string) error, abort func(), err error) {
	body := func(id string) string { return id }
	if co.host != "" {
		body = func(id string) string {
			b, _ := json.Marshal(activeMark{Run: id, Host: co.host, By: actor, Started: time.Now().UTC().Round(time.Second), Limit: short(limit)})
			return string(b)
		}
	}
	return co.takeMark(activeName(ticketID, job), body, func(id string, m mark) error {
		if a, ok := readActive(m.body); ok && a.Host != "" && a.Host != co.host {
			lim, err := time.ParseDuration(a.Limit)
			if err != nil || lim <= 0 {
				lim = 24 * time.Hour // the most any job's limit may be
			}
			if runs.Overdue(m.at, lim, time.Now()) {
				return nil
			}
			if a.By == actor {
				return alreadyGoing{run: a.Run}
			}
			return fmt.Errorf("%w: run %s, %s's on another machine, already does it", ticket.ErrClaimed, a.Run, dash(a.By))
		}
		if r, err := runs.Get(dir, id, time.Now()); err == nil && !r.Ended() {
			if r.Launcher == actor {
				return alreadyGoing{run: r.ID}
			}
			return fmt.Errorf("%w: run %s, %s's, already does it", ticket.ErrClaimed, r.ID, r.Launcher)
		}
		return nil
	})
}

// activeMark is what a shared store's .active mark holds. A file's holds
// the run id alone.
type activeMark struct {
	Run     string    `json:"run"`
	Host    string    `json:"host"`
	By      string    `json:"by,omitempty"`
	Started time.Time `json:"started"`
	Limit   string    `json:"limit"`
}

// readActive reads a mark's body as an activeMark, if it is one.
func readActive(body string) (activeMark, bool) {
	var a activeMark
	b := strings.TrimSpace(body)
	if !strings.HasPrefix(b, "{") || json.Unmarshal([]byte(b), &a) != nil {
		return activeMark{}, false
	}
	return a, true
}

// markRun is the run a run mark names, or "" for none yet.
func markRun(body string) string {
	if a, ok := readActive(body); ok {
		return a.Run
	}
	return strings.TrimSpace(body)
}

// alreadyGoing is a start a session of the same bot already made: the
// work goes on, so it is no failure, and the bot does nothing more. ran
// says that start has ended since: a stale do: line, run late.
type alreadyGoing struct {
	run string
	ran bool
}

func (a alreadyGoing) Error() string {
	if a.ran {
		return "already went again as " + a.run
	}
	return "already going as " + a.run
}

// movedOn is a retry or resume refused for good: the run's ticket moved
// on without it, so no later try starts it. The command a bot runs acks
// the run, so it never waits unacked for a start that can't come; a
// run's own end drops the line.
type movedOn struct {
	run string
	err error
}

func (m movedOn) Error() string { return m.err.Error() }
func (m movedOn) Unwrap() error { return m.err }

// goingOn prints a start another session of this bot made, or a run
// whose ticket moved on, which it acks, and reports whether err was one.
// An ack that fails leaves the refusal to stand.
func (c *call) goingOn(err error) bool {
	var a alreadyGoing
	var m movedOn
	if errors.As(err, &m) {
		dir, derr := c.besideStore("runs")
		if derr == nil {
			_, derr = runs.Ack(dir, m.run, c.actor, time.Now().UTC().Round(time.Millisecond))
		}
		if derr != nil {
			return false
		}
		why := strings.TrimPrefix(m.err.Error(), ticket.ErrStatusConflict.Error()+": ")
		c.out(map[string]any{"acked": m.run, "moved_on": why}, func(w io.Writer) {
			fmt.Fprintf(w, "%s: acked %s\n", why, m.run)
		})
		return true
	}
	if !errors.As(err, &a) {
		return false
	}
	if a.ran {
		c.out(map[string]any{"went_again_as": a.run}, func(w io.Writer) {
			fmt.Fprintf(w, "went again as %s already: nothing to do\n", a.run)
		})
		return true
	}
	c.out(map[string]any{"already_going": a.run}, func(w io.Writer) {
		fmt.Fprintf(w, "%s already going: nothing to do\n", a.run)
	})
	return true
}

// takeMark makes the run mark name exclusive, so of takers at once one
// wins; its commit writes body(run id) in it. A mark that names a run
// holds while held says so; a mark with no run yet, left by a taker that
// died before its commit, holds for activeGrace. A taker that finds one
// stale moves it aside under a takeover lock, and only if it still holds
// what it read, so two takers that both find it stale can't both win.
func (co coord) takeMark(name string, body func(runID string) string, held func(id string, m mark) error) (commit func(runID string) error, abort func(), err error) {
	busy := fmt.Errorf("%w: another start of this run is under way", ticket.ErrClaimed)
	for range 3 {
		if co.s.create(runMarks, name, "") == nil {
			commit = func(id string) error { return co.s.write(runMarks, name, body(id)) }
			return commit, func() { co.s.remove(runMarks, name) }, nil
		}
		m, _ := co.s.read(runMarks, name)
		id := markRun(m.body)
		if id == "" {
			if m.ver != nil && time.Since(m.at) < activeGrace {
				return nil, nil, busy
			}
		} else if err := held(id, m); err != nil {
			return nil, nil, err
		}
		took, cleared := co.s.takeOver(runMarks, name, func(now mark) bool { return sameMark(m, now) })
		if !took && !cleared {
			return nil, nil, busy
		}
	}
	return nil, nil, busy
}

// activeGrace is how long a start may hold an active file before it
// writes its run's id.
const activeGrace = 30 * time.Second

// dropActive removes a run's active mark when the run ends, if it is
// still the run's.
func (co coord) dropActive(r runs.Record) {
	if r.Ticket == "" || r.Job == "" {
		return
	}
	name := activeName(r.Ticket, r.Job)
	if m, err := co.s.read(runMarks, name); err == nil && markRun(m.body) == r.ID {
		co.s.remove(runMarks, name)
	}
}

func joinStatuses(st []ticket.Status) string {
	s := make([]string, len(st))
	for i, x := range st {
		s[i] = string(x)
	}
	return strings.Join(s, " or ")
}

// mayStart refuses a new run while maestro/STOP sits next to the store,
// or while a usage limit pickup saw holds: such a run only stops, or fails
// again, and spends a start.
func (c *call) mayStart() error {
	co, err := c.coord()
	if err != nil {
		return err
	}
	if stop, err := co.s.stopped(); err != nil {
		return err
	} else if stop {
		return fmt.Errorf("%w: stopped: maestro/STOP is set: start no run", ticket.ErrStopped)
	}
	if p, ok := co.readPause(time.Now()); ok {
		return fmt.Errorf("%w: paused: %s, until %s: start no run until then", ticket.ErrPaused, p.Why, p.Until.Format(time.RFC3339))
	}
	return nil
}

// A pause is a usage limit pickup saw: no job starts until it lifts, on
// any bot, since the plan's limit is the whole team's.
type pause struct {
	Until time.Time `json:"until"`
	Why   string    `json:"why"`
}

// readPause reads the pause, if one holds now.
func (co coord) readPause(now time.Time) (pause, bool) {
	b, err := co.s.flag(pauseFlag)
	if err != nil {
		return pause{}, false
	}
	var p pause
	if json.Unmarshal(b, &p) != nil || !now.Before(p.Until) {
		return pause{}, false
	}
	return p, true
}

// setPause records a pause until the later of its time and any pause
// already set, written whole so a reader never sees half of one.
func (co coord) setPause(p pause, now time.Time) bool {
	if old, ok := co.readPause(now); ok && !p.Until.After(old.Until) {
		return false // as long a pause holds already
	}
	b, _ := json.Marshal(p)
	co.s.setFlag(pauseFlag, b)
	return true
}

// ticketForJob reads the ticket a job names, so a typo fails here and not
// in a session that already spent its start.
func (c *call) ticketForJob(ctx context.Context, id string) (ticket.Ticket, error) {
	tid, err := ticket.ParseID(id)
	if err != nil {
		return ticket.Ticket{}, err
	}
	s, err := c.open(ctx)
	if err != nil {
		return ticket.Ticket{}, err
	}
	defer s.Close()
	d, err := s.Get(ctx, tid)
	return d.Ticket, err
}

// oneLineText joins text onto one line, for a reason that goes in a
// prompt's flag.
func oneLineText(s string) string { return strings.Join(strings.Fields(s), " ") }

// printStarted prints a started run in one line, then, if any of the
// actor's runs ended and aren't acked, one line that counts them, so a
// turn that starts a run learns of them.
func (c *call) printStarted(r runs.Record, model, why string) error {
	dir, err := c.besideStore("runs")
	if err != nil {
		return err
	}
	list, _ := runs.List(dir, time.Now(), false)
	ended := []string{}
	for _, o := range list {
		if o.Owner() == c.actor && o.Ended() && !o.Settling && o.ID != r.ID {
			ended = append(ended, o.ID)
		}
	}
	res := map[string]any{"run": r, "model": model, "ended": ended}
	return c.out(res, func(w io.Writer) {
		on := ""
		if r.Ticket != "" {
			on = " " + r.Ticket
		}
		extra := ""
		if why != "" {
			extra = ", " + why
		}
		fmt.Fprintf(w, "%s started: %s%s on %s, limit %s%s\n", r.ID, dash(r.Job), on, model, r.Limit, extra)
		if len(ended) > 0 {
			fmt.Fprintf(w, "%d ended, unacked: run pickup\n", len(ended))
		}
	})
}

// runAgain starts a run's job afresh, as again does, and prints the run.
func (c *call) runAgain(ctx context.Context, limit time.Duration, full bool) error {
	if len(c.args) != 2 || !runs.ValidID(c.args[1]) {
		return usagef("job again: want one run id, such as R-abcdefgh")
	}
	nr, err := c.again(ctx, againOpts{run: c.args[1], limit: limit, full: full})
	if err != nil {
		return err
	}
	return c.printStarted(nr, dash(flagValue(nr.Command, "--model")), "again: "+c.args[1])
}

// againOpts are what job again takes: the run, and a new limit or the
// full model when given.
type againOpts struct {
	run   string
	limit time.Duration
	full  bool
}

// again starts a run's job afresh: its prompt and command as they were,
// a new session, and a new limit when given. With full, a light review
// goes on the full model. A retry keeps every flag the bot gave the
// first run (--approved, --drop, --slots), and the chain it was part of.
// A run goes again once, whoever asks and however often: a run's end
// and its bot may both run the same do: line.
func (c *call) again(ctx context.Context, o againOpts) (runs.Record, error) {
	fail := func(err error) (runs.Record, error) { return runs.Record{}, err }
	if err := c.needActor("job"); err != nil {
		return fail(err)
	}
	dir, err := c.besideStore("runs")
	if err != nil {
		return fail(err)
	}
	r, err := runs.Get(dir, o.run, time.Now())
	if errors.Is(err, fs.ErrNotExist) {
		return fail(fmt.Errorf("%w: run %s", ticket.ErrNotFound, o.run))
	}
	if err != nil {
		return fail(err)
	}
	switch {
	case r.Launcher != c.actor:
		return fail(fmt.Errorf("%w: job again: run %s is %s's", ticket.ErrForbidden, r.ID, dash(r.Launcher)))
	case r.Job == "":
		return fail(usagef("job again: run %s isn't a job's", r.ID))
	case !r.Ended():
		return fail(fmt.Errorf("%w: job again: %s", ticket.ErrStatusConflict, stillRunning(r)))
	}
	// A resumed run's prompt is only its resume text: start from the run
	// the session began with.
	first := r
	for range 50 {
		if first.ResumeOf == "" {
			break
		}
		p, err := runs.Get(dir, first.ResumeOf, time.Now())
		if err != nil {
			return fail(fmt.Errorf("%w: job again: run %s resumes %s, which is gone: start the job with job", ticket.ErrNotFound, first.ID, first.ResumeOf))
		}
		first = p
	}
	prompt, err := os.ReadFile(filepath.Join(dir, first.ID+".in"))
	if err != nil {
		return fail(err)
	}
	if err := c.admit(); err != nil {
		return fail(err)
	}
	k, _ := findJob(r.Job)
	if r.Ticket != "" {
		if err := c.jobFits(ctx, k, r.Ticket); err != nil {
			if errors.Is(err, ticket.ErrStatusConflict) {
				err = movedOn{run: r.ID, err: err}
			}
			return fail(err)
		}
	}
	cmd, err := c.stopHook(thisSlotBinary(first.Command), r.Job)
	if err != nil {
		return fail(err)
	}
	for i := 0; i+1 < len(cmd); i++ {
		switch {
		case cmd[i] == "--session-id":
			cmd[i+1] = newSessionID()
		case cmd[i] == "--model" && (o.full || flagValue(r.Command, "--model") == modelOpus):
			cmd[i+1] = modelOpus // a session moved to Opus stays there
		}
	}
	if o.full {
		prompt = []byte(strings.Replace(string(prompt), " --light", "", 1))
	}
	lim := r.LimitDuration()
	if o.limit != 0 {
		lim = o.limit
	}
	if lim < time.Second || lim > 24*time.Hour {
		return fail(usagef("job again: --limit %s: want 1s to 24h", lim))
	}
	rec := runs.Record{Ticket: r.Ticket, Tickets: r.Tickets, Limit: short(lim), Job: r.Job, Command: cmd,
		AgainOf: r.ID, ChainOf: r.ChainOf, Hops: r.Hops, Dir: r.Dir}
	// Once: of agains at once one makes the mark; a later one finds it
	// naming the run it made. A start that fails takes it back.
	co, err := c.coord()
	if err != nil {
		return fail(err)
	}
	commitMark, abortMark, err := co.takeMark(r.ID+".again", func(id string) string { return id }, func(id string, _ mark) error {
		if nr, err := runs.Get(dir, id, time.Now()); err == nil && nr.Ended() {
			return alreadyGoing{run: id, ran: true}
		}
		return alreadyGoing{run: id}
	})
	if err != nil {
		return fail(err)
	}
	commitActive, abort := func(string) error { return nil }, func() {}
	if r.Ticket != "" {
		if commitActive, abort, err = co.takeActive(dir, r.Ticket, r.Job, c.actor, lim); err != nil {
			abortMark()
			return fail(fmt.Errorf("job again %s: %w", r.ID, err))
		}
	}
	// Its facts carry on the run it retries; its drops, the prompt's.
	side := &sideFacts{Why: "again", AgainOf: r.ID, PromptBytes: ptr(len(prompt))}
	side.fromParent(dir, r.ID)
	if p, ok := readSide(dir, first.ID); ok {
		side.Drops = p.Drops
	}
	commit := func(id string) error { return errors.Join(commitMark(id), commitActive(id)) }
	nr, err := c.startRun(rec, prompt, runs.DefaultRings, side, commit)
	if err != nil {
		abort()
		abortMark()
		return fail(err)
	}
	runs.Ack(dir, r.ID, c.actor, time.Now()) // the new run carries on its work
	if (r.Job == "plan" || r.Job == "rule" || r.Job == "finish") && r.Ticket != "" {
		// The rerun holds the ticket, as look's start would, so no look
		// starts a second planner run beside it.
		co.recordHandouts([]send{{"planner-job", r.Ticket}})
	}
	return nr, nil
}

// stillRunning says why a live run can't go again yet, and the way out.
func stillRunning(r runs.Record) string {
	return fmt.Sprintf("run %s is %s, %s into its %s limit: wait for its end, or end it with maestro-ticket stop %s", r.ID, r.State, ago(time.Since(r.StartedAt)), r.Limit, r.ID)
}

// diedText is what a run that died is resumed with.
const diedText = "Your run died. Go on from your last step."

// goOnText is what a session a usage limit stopped is resumed with.
const goOnText = "A usage limit stopped your run; it has lifted. Go on from your last step."

// apiGoOnText is what a session an API outage stopped is resumed with.
const apiGoOnText = "An API outage stopped your run; its pause has lifted. Go on from your last step."

// escalateText is what a build session that ended on a question, with
// no Escalation: posted, is resumed with, so the planner can answer it.
const escalateText = "You stopped on a question. Post it on the ticket as an Escalation:, as maestro-working-principles says for a block, move the ticket to blocked, release it, and stop."

// decideText is what a planner session that stopped on a question is
// resumed with: it picks what is the planner's, and sends up the rest.
const decideText = "Make each call you stopped on that is the planner's yourself, naming your pick, and finish. Put each call that is the chief of staff's on a line of its own: up: <the card in the team rules' shape, on one line, its fields split by |>."

// opusText is what a junior build's stuck session is resumed with, once
// it moves to Opus in the senior engineer's place.
const opusText = "You are now on Opus, in the senior engineer's place. Settle your question yourself if the ticket and the repo settle it, and go on. If they don't, send it up as your skill says."

// runResume runs resume; a resume another session of this bot made is
// already going, no failure.
func runResume(ctx context.Context, c *call) error {
	if err := resumeRun(ctx, c); !c.goingOn(err) {
		return err
	}
	return nil
}

// resumeRun resumes a run's session, as resume does, and prints the run.
func resumeRun(ctx context.Context, c *call) error {
	died := c.fs.Bool("died", false, "the run died: resume it once with \""+diedText+"\"")
	opus := c.fs.Bool("opus", false, "resume on Opus at high effort, as for a junior build's stuck session")
	escalate := c.fs.Bool("escalate", false, "resume a build that ended on a question with \""+escalateText+"\"")
	decide := c.fs.Bool("decide", false, "resume a planner session that stopped on a question: it decides, or sends calls up")
	goOn := c.fs.Bool("go-on", false, "resume a session a usage limit stopped, once the limit lifts")
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) != 1 || !runs.ValidID(c.args[0]) {
		return usagef("resume: want one run id, such as R-abcdefgh")
	}
	if !runs.Supported {
		return usagef("resume needs a unix system")
	}
	nr, err := c.resume(ctx, resumeOpts{run: c.args[0], died: *died, opus: *opus, escalate: *escalate, decide: *decide, goOn: *goOn, answer: c.readStdin})
	if err != nil {
		return err
	}
	return c.printStarted(nr, dash(flagValue(nr.Command, "--model")), "resumes "+c.args[0])
}

// resumeOpts are what resume takes: the run, which fixed text it goes on
// with, and where an answer comes from when none does (nil: none).
type resumeOpts struct {
	run                                string
	died, opus, escalate, decide, goOn bool
	answer                             func() ([]byte, error)
}

// resume resumes a run's session as a new run with the same command,
// ticket, limit and chain: with the answer; with died, once, after the
// run died; with opus, on Opus at high effort.
func (c *call) resume(ctx context.Context, o resumeOpts) (runs.Record, error) {
	fail := func(err error) (runs.Record, error) { return runs.Record{}, err }
	if err := c.needActor("resume"); err != nil {
		return fail(err)
	}
	dir, err := c.besideStore("runs")
	if err != nil {
		return fail(err)
	}
	now := time.Now()
	r, err := runs.Get(dir, o.run, now)
	if errors.Is(err, fs.ErrNotExist) {
		return fail(fmt.Errorf("%w: run %s", ticket.ErrNotFound, o.run))
	}
	if err != nil {
		return fail(err)
	}
	switch {
	case r.Launcher != c.actor:
		return fail(fmt.Errorf("%w: resume: run %s is %s's: only the bot that launched it resumes it", ticket.ErrForbidden, r.ID, dash(r.Launcher)))
	case !r.Ended():
		return fail(fmt.Errorf("%w: resume: %s", ticket.ErrStatusConflict, stillRunning(r)))
	case o.died && r.State != runs.Died && !r.LauncherStopped():
		return fail(fmt.Errorf("%w: resume --died: run %s %s, it didn't die: a hung or failed run goes to whoever handed you the job", ticket.ErrStatusConflict, r.ID, r.State))
	}
	session := r.SessionID
	if session == "" {
		session = flagValue(r.Command, "--session-id")
	}
	if session == "" {
		session = flagValue(r.Command, "--resume")
	}
	if session == "" {
		return fail(fmt.Errorf("%w: resume: run %s has no session id to resume", ticket.ErrInvalid, r.ID))
	}
	// Stopped, paused or past a floor, it starts nothing: say so before
	// reading every past run.
	if err := c.admit(); err != nil {
		return fail(err)
	}
	nudge := ""
	switch {
	case o.opus:
		nudge = "opus"
	case o.escalate:
		nudge = "escalate"
	case o.decide:
		nudge = "decide"
	}
	all, err := c.pastRuns()
	if err != nil {
		return fail(err)
	}
	for _, x := range all {
		if neverRan(x) {
			continue // its start failed: it holds nothing
		}
		if nudge != "" && x.Nudge == nudge && flagValue(x.Command, "--resume") == session {
			return fail(fmt.Errorf("%w: resume --%s: session %s had it once already (%s): it goes up instead", ticket.ErrStatusConflict, nudge, session, x.ID))
		}
		if x.ResumeOf == r.ID {
			return fail(alreadyGoing{run: x.ID}) // only its launcher resumes a run: this bot did
		}
		if o.died && x.AfterDeath && flagValue(x.Command, "--resume") == session {
			return fail(fmt.Errorf("%w: resume --died: session %s was resumed once after a death already (%s): report it to whoever handed you the job", ticket.ErrStatusConflict, session, x.ID))
		}
	}
	text := ""
	switch {
	case o.died:
		text = diedText
	case o.escalate:
		text = escalateText
	case o.decide:
		text = decideText
	case o.goOn && apiDown(r):
		text = apiGoOnText
	case o.goOn:
		text = goOnText
	default:
		var b []byte
		if o.answer != nil {
			if b, err = o.answer(); err != nil {
				return fail(err)
			}
		}
		text = strings.TrimSpace(string(b))
		if text == "" && o.opus {
			text = opusText
		}
		if text == "" {
			return fail(usagef("resume: send the answer on stdin, in a quoted heredoc"))
		}
	}
	cmd, err := c.stopHook(resumeCommand(r.Command, session, o.opus), r.Job)
	if err != nil {
		return fail(err)
	}
	rec := runs.Record{
		Ticket: r.Ticket, Tickets: r.Tickets, Limit: r.Limit, Job: r.Job, Command: cmd,
		ResumeOf: r.ID, AfterDeath: o.died, Nudge: nudge, ChainOf: r.ChainOf, Hops: r.Hops, Dir: r.Dir,
	}
	if err := c.resumeFits(ctx, r); err != nil {
		if errors.Is(err, ticket.ErrStatusConflict) {
			err = movedOn{run: r.ID, err: err}
		}
		return fail(err)
	}
	// A run resumes once: of resumes at once, one makes its marker.
	co, err := c.coord()
	if err != nil {
		return fail(err)
	}
	commitMark, abortMark, err := co.takeMark(r.ID+".resumed", func(id string) string { return id }, func(id string, _ mark) error {
		return alreadyGoing{run: id}
	})
	if err != nil {
		return fail(err)
	}
	// And one run of a job on a ticket at a time, resumed or not.
	commitActive, abortActive := func(string) error { return nil }, func() {}
	if r.Ticket != "" && r.Job != "" {
		if commitActive, abortActive, err = co.takeActive(dir, r.Ticket, r.Job, c.actor, rec.LimitDuration()); err != nil {
			abortMark()
			return fail(fmt.Errorf("resume %s: %w", r.ID, err))
		}
	}
	side := &sideFacts{Why: "resume", PromptBytes: ptr(len(text) + 1)}
	side.fromParent(dir, r.ID)
	commit := func(id string) error { return errors.Join(commitMark(id), commitActive(id)) }
	nr, err := c.startRun(rec, []byte(text+"\n"), runs.DefaultRings, side, commit)
	if err != nil {
		abortMark()
		abortActive()
		return fail(err)
	}
	// Its result is acted on: pickup stops showing it, and its rings stop.
	runs.Ack(dir, r.ID, c.actor, time.Now())
	return nr, nil
}

// thisSlotBinary is cmd with its slot wrapper on this binary as it is now,
// not the path the run started from, which an upgrade may have moved.
func thisSlotBinary(cmd []string) []string {
	cmd = slices.Clone(cmd)
	exe, err := os.Executable()
	if err != nil {
		return cmd
	}
	for i := 0; i+1 < len(cmd); i++ {
		if cmd[i+1] == "slot" && filepath.Base(cmd[i]) == filepath.Base(exe) {
			cmd[i] = exe
		}
	}
	return cmd
}

// flagValue is the word after flag in a command line, or "".
func flagValue(cmd []string, flag string) string {
	for i, w := range cmd {
		if w == flag && i+1 < len(cmd) {
			return cmd[i+1]
		}
		if v, ok := strings.CutPrefix(w, flag+"="); ok {
			return v
		}
	}
	return ""
}

// resumeCommand is a run's command made to resume session: its own
// --session-id or --resume goes, --resume <session> comes after -p, and
// with opus the model and effort become Opus at high.
func resumeCommand(cmd []string, session string, opus bool) []string {
	out := []string{}
	cmd = thisSlotBinary(cmd)
	for i := 0; i < len(cmd); i++ {
		w := cmd[i]
		name, _, eq := strings.Cut(w, "=")
		switch {
		case w == "--session-id" || w == "--resume" || (opus && (w == "--model" || w == "--effort")):
			i++ // and its value
			continue
		case strings.HasPrefix(w, "--") && eq && (name == "--session-id" || name == "--resume" || (opus && (name == "--model" || name == "--effort"))):
			continue
		}
		out = append(out, w)
		if w == "-p" || w == "--print" {
			out = append(out, "--resume", session)
			if opus {
				out = append(out, "--model", modelOpus, "--effort", effortHigh)
			}
		}
	}
	if !slices.Contains(out, "--resume") {
		out = append(out, "--resume", session)
		if opus {
			out = append(out, "--model", modelOpus, "--effort", effortHigh)
		}
	}
	return out
}
