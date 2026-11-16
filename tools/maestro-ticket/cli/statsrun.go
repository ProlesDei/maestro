package cli

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// The run facts stats.db keeps: built here from what a run's end already
// holds, so the stats package sees only typed values. Nothing here prints:
// a run's end reads the same with stats on, off or broken.

// botProfiles are the seven Grok bots' ids, as bot-profiles/ names them.
var botProfiles = []string{"chief-of-staff", "planner", "senior-engineer", "junior-engineer", "qa-lead", "reviewer", "designer"}

// statsRole reduces a launcher to a profile name, person or other, by
// exact match, before anything is written.
func statsRole(launcher string) string {
	switch {
	case launcher == "":
		return stats.Other
	case slices.Contains(botProfiles, launcher):
		return launcher
	}
	return "person"
}

var (
	// claude-opus-5-5, claude-sonnet-4-5-20250929, claude-opus-4-6[1m]
	modelID = regexp.MustCompile(`^claude-(opus|sonnet|haiku)-([3-9])(?:[-.]([0-9]))?(?:-[0-9]{8})?(?:\[1m\])?$`)
	// claude-3-5-sonnet-20241022, the older order
	oldModelID = regexp.MustCompile(`^claude-([3-9])(?:-([0-9]))?-(opus|sonnet|haiku)(?:-[0-9]{8})?$`)
)

// modelFamily maps a model name, as argv or modelUsage gives it, to its
// family on the list: opus, opus-5.5, sonnet-4.5 and the like. Any other,
// such as a proxy's own id, is other.
func modelFamily(id string) string {
	switch id {
	case modelOpus, modelSonnet, "haiku":
		return id
	}
	fam, major, minor := "", "", ""
	if m := modelID.FindStringSubmatch(id); m != nil {
		fam, major, minor = m[1], m[2], m[3]
	} else if m := oldModelID.FindStringSubmatch(id); m != nil {
		fam, major, minor = m[3], m[1], m[2]
	} else {
		return stats.Other
	}
	if minor != "" {
		return fam + "-" + major + "." + minor
	}
	return fam + "-" + major
}

// modelNames is every family modelFamily can return.
func modelNames() []string {
	out := []string{modelOpus, modelSonnet, "haiku"}
	for _, f := range []string{"opus", "sonnet", "haiku"} {
		for major := 3; major <= 9; major++ {
			out = append(out, fmt.Sprintf("%s-%d", f, major))
			for minor := 0; minor <= 9; minor++ {
				out = append(out, fmt.Sprintf("%s-%d.%d", f, major, minor))
			}
		}
	}
	return out
}

// argvFacts reads a run's model, effort and lean start from its command,
// as claudeCommand writes them: the one place they live.
func argvFacts(cmd []string) (model, effort string, lean bool) {
	if m := flagValue(cmd, "--model"); m != "" {
		model = modelFamily(m)
	}
	return model, flagValue(cmd, "--effort"), slices.Contains(cmd, "CLAUDE_CODE_PROMPT_CACHE_TTL=5m")
}

// statsLists are the Go lists stats.db's codes hold.
func statsLists() stats.Lists {
	jobs := []string{"launch"}
	for _, k := range jobKinds {
		jobs = append(jobs, k.name)
	}
	return stats.Lists{
		"role":        append(slices.Clone(botProfiles), "person"),
		"job":         jobs,
		"model":       modelNames(),
		"effort":      {"low", "medium", "high", "xhigh", "max"},
		"why":         {"tier_senior", "tier_junior", "tier_none", "sent_back", "route_light", "route_full", "fixed", "override", "again", "resume"},
		"tier":        {"junior", "senior", "none"},
		"nudge":       {"none", "opus", "escalate", "decide"},
		"stop":        {"none", "term", "kill"},
		"state":       {runs.Starting, runs.Running, runs.Done, runs.Failed, runs.TimedOut, runs.Died, runs.Unreadable},
		"outcome":     {outDone, outQuestion, outUsageLimit, outDiskFull, outTimedOut, outDied, outRefused, outFailed},
		"reason":      {"no_result", "max_turns", "api_error", "budget", "stopped", "exit_nonzero", "is_error", "limit", "reboot", "supervisor"},
		"subtype":     {"success", "error_max_turns", "error_during_execution", "error_max_budget_usd", "error_max_structured_output_retries"},
		"stop_reason": {"end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal", "model_context_window_exceeded"},
		"stream":      {"ok", "no_result", "lost", "off"},
		"ring_err":    {"none", "timeout", "dns", "http4xx", "http5xx"},
		"os":          {"linux", "darwin", "freebsd", "openbsd", "netbsd"},
		"arch":        {"amd64", "arm64", "386", "arm", "riscv64"},
		"cost_src":    {"result", "stream"},
	}
}

// reasonOf says why a run ended badly, from its record and result: one
// of the reason list, or "" for a run that didn't.
func reasonOf(r runs.Record, outcome string, res claudeReply, resErr error) string {
	switch outcome {
	case outDied:
		return "supervisor"
	case outTimedOut:
		return "limit"
	case outFailed:
	default:
		return ""
	}
	switch {
	case strings.HasPrefix(r.Error, "the launcher was stopped"):
		return "stopped"
	case resErr != nil:
		return "no_result"
	case res.Subtype.V == "error_max_turns":
		return "max_turns"
	case strings.Contains(res.Subtype.V, "budget"):
		return "budget"
	case res.APIErrorStatus.Set:
		return "api_error"
	case res.IsError.V || r.IsError:
		return "is_error"
	case r.Exit != nil && *r.Exit != 0:
		return "exit_nonzero"
	}
	return ""
}

func ptr[T any](v T) *T { return &v }

// whole is a loose number as a count of whole units, or nil.
func whole(n looseNum) *int64 {
	if !n.OK || n.V < 0 || n.V > 1<<53 {
		return nil
	}
	return ptr(int64(n.V))
}

// vTool is the commit this binary was built from, or "" when the build
// didn't stamp one.
var vTool = sync.OnceValue(func() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && sha40.MatchString(s.Value) {
			return s.Value
		}
	}
	return ""
})

var sha40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

// statsRow is the run's row: what its record, its result and the machine
// say at its end.
func (c *call) statsRow(storeDir string, r runs.Record, res claudeReply, resErr error) stats.Run {
	outcome, _ := outcomeOf(r)
	row := stats.Run{
		Run: r.ID, Session: r.SessionID, ResumeOf: r.ResumeOf, Ticket: int64(runTicket(r)),
		Role: statsRole(r.Launcher), Job: cmp.Or(r.Job, "launch"), Nudge: cmp.Or(r.Nudge, "none"),
		State: r.State, Outcome: outcome, Reason: reasonOf(r, outcome, res, resErr),
		AfterDeath: r.AfterDeath, StartedAt: r.StartedAt, ReplyBytes: int64(len(r.Reply)),
		Stream: "off", OS: runtime.GOOS, Arch: runtime.GOARCH, VTool: vTool(), VMaestro: c.statsSha,
	}
	if !c.inSupervisor {
		row.VMaestro = stats.Sha(storeDir)
	}
	row.Model, row.Effort, row.Lean = argvFacts(r.Command)
	if r.EndedAt != nil {
		row.EndedAt = *r.EndedAt
	}
	if r.Exit != nil {
		row.Exit = ptr(int64(*r.Exit))
	}
	if resErr == nil {
		row.Session = cmp.Or(res.SessionID, row.Session)
		row.Subtype, row.StopReason = res.Subtype.V, res.StopReason.V
		if res.IsError.OK {
			row.IsError = ptr(res.IsError.V)
		}
		row.ClaudeMs, row.APIMs, row.Turns = whole(res.DurationMS), whole(res.DurationAPIMS), whole(res.NumTurns)
		for name, u := range res.ModelUsage {
			m := stats.Model{Model: modelFamily(name), InputCum: u.InputTokens, OutputCum: u.OutputTokens,
				CacheReadCum: u.CacheReadInputTokens, CacheWriteCum: u.CacheCreationInputTokens, CostSrc: "result"}
			if u.CostUSD.OK {
				m.CostCum = ptr(u.CostUSD.V)
			}
			row.Models = append(row.Models, m)
		}
		slices.SortFunc(row.Models, func(a, b stats.Model) int { return strings.Compare(a.Model, b.Model) })
	}
	free := envcheck.DiskFree
	if c.env.DiskFree != nil {
		free = c.env.DiskFree
	}
	if f, ok, err := free(storeDir); err == nil && ok {
		row.DiskFreePct, row.InodeFreePct = ptr(int64(f.Space)), ptr(int64(f.Inodes))
	}
	return row
}

// statsUnsafe reports a store whose filesystem checks failed and were
// let through, or were told to be: stats stays off there.
func (c *call) statsUnsafe() bool {
	if c.env.Getenv(envcheck.AllowEnv) == "1" {
		return true
	}
	return slices.ContainsFunc(c.storeChecks, func(k ticket.Check) bool {
		return !k.OK || k.Warn || strings.HasPrefix(k.Detail, "allowed by ")
	})
}

func (c *call) statsOpen(path string, create bool) (*sql.DB, error) {
	if c.env.StatsOpen != nil {
		return c.env.StatsOpen(path, create)
	}
	return stats.Open(path, create)
}

// statsPrepare is how long a supervisor gives the start of stats.db: a
// goroutine at its start, while its command runs. statsRetry is all a
// second try at run end gets, since the ring waits on it.
const (
	statsPrepare = 2 * time.Second
	statsRetry   = 300 * time.Millisecond
)

// startStats readies stats.db for a supervisor: it reads the maestro-sha
// once and creates or migrates the file in the background.
func (c *call) startStats(storeDir string) {
	c.inSupervisor = true
	c.statsSha = stats.Sha(storeDir)
	c.statsPrep = make(chan error, 1)
	if stats.Off(storeDir, c.env.Getenv, c.statsUnsafe()) != "" {
		c.statsPrep <- nil
		return
	}
	go func() { c.statsPrep <- c.prepareStats(storeDir, statsPrepare) }()
}

func (c *call) prepareStats(storeDir string, budget time.Duration) error {
	db, err := c.statsOpen(filepath.Join(storeDir, stats.DBName), true)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	return stats.Prepare(ctx, db, statsLists())
}

// orphanWait is all a write from pickup or doctor waits on stats.db: a
// bot reads their output.
const orphanWait = 250 * time.Millisecond

// writeStats stores the run's row, after the rest of its end. In the
// supervisor it creates or migrates stats.db first if it must; anywhere
// else it writes only to a file that is there at this binary's schema,
// within orphanWait, and drops the row otherwise. It never prints.
func (c *call) writeStats(runsDir string, r runs.Record, res claudeReply, resErr error) {
	storeDir := filepath.Dir(runsDir)
	if stats.Off(storeDir, c.env.Getenv, c.statsUnsafe()) != "" {
		return
	}
	path := filepath.Join(storeDir, stats.DBName)
	row := c.statsRow(storeDir, r, res, resErr)
	ctx := context.Background()
	if !c.inSupervisor {
		// An empty file is one no supervisor prepared: opening it would
		// set it up.
		if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
			return
		}
		db, err := c.statsOpen(path, false)
		if err != nil {
			return
		}
		defer db.Close()
		ctx, cancel := context.WithTimeout(ctx, orphanWait)
		defer cancel()
		stats.Write(ctx, db, row)
		return
	}
	if c.statsPrep != nil {
		if err := <-c.statsPrep; err != nil {
			c.prepareStats(storeDir, statsRetry) // once more: a busy start, or a file made since
		}
		c.statsPrep = nil
	}
	db, err := c.statsOpen(path, false)
	if err != nil {
		return
	}
	defer db.Close()
	stats.Write(ctx, db, row)
}

// statsPruneLimit bounds the supervisor's work after its first ring.
const statsPruneLimit = 3 * time.Second

// afterRing records whether the run's first ring got through, then
// prunes stats.db, if this supervisor claims this hour's prune.
func (c *call) afterRing(runsDir string, r runs.Record, ringErr error) {
	storeDir := filepath.Dir(runsDir)
	if stats.Off(storeDir, c.env.Getenv, c.statsUnsafe()) != "" {
		return
	}
	db, err := c.statsOpen(filepath.Join(storeDir, stats.DBName), false)
	if err != nil {
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), statsPruneLimit)
	defer cancel()
	stats.SetRing(ctx, db, r.ID, r.Rang, runs.RingErrCode(ringErr))
	stats.Prune(ctx, db, time.Now())
}
