package cli

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	"github.com/ProlesDei/maestro/tools/maestro-ticket/render"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/streamfilter"
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
		"stop":        {runs.StopNone, runs.StopTerm, runs.StopKill},
		"state":       {runs.Starting, runs.Running, runs.Done, runs.Failed, runs.TimedOut, runs.Died, runs.Unreadable},
		"outcome":     {outDone, outQuestion, outUsageLimit, outDiskFull, outTimedOut, outDied, outRefused, outFailed},
		"reason":      append([]string{"no_result", "max_turns", "api_error", "budget", "stopped", "exit_nonzero", "is_error", "limit", "frozen", "asked", "reboot", "supervisor"}, escalateCauses...),
		"head":        headNames(),
		"verdict":     {"go", "nogo", "escalate"},
		"mode":        {"full", "incremental", "ruling"},
		"finding":     {"finding", "follow_up", "dropped"},
		"sev":         {"blocker", "major", "minor", "nit"},
		"action":      {"auto-fix", "ask-user", "no-op"},
		"prior":       {"new", "still_present"},
		"subtype":     {"success", "error_max_turns", "error_during_execution", "error_max_budget_usd", "error_max_structured_output_retries"},
		"stop_reason": {"end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal", "model_context_window_exceeded"},
		"stream":      {"ok", "no_result", "lost", "off"},
		"ring_err":    {"none", "timeout", "dns", "http4xx", "http5xx"},
		"os":          {"linux", "darwin", "freebsd", "openbsd", "netbsd"},
		"arch":        {"amd64", "arm64", "386", "arm", "riscv64"},
		"cost_src":    {"result", "stream"},
		"step":        streamfilter.Codes(streamfilter.Names{Sub: commandNames()}),
	}
}

// reasonOf says why a run ended badly, from its record and result: one
// of the reason list, or "" for a run that didn't.
func reasonOf(r runs.Record, outcome string, res claudeReply, resErr error) string {
	switch outcome {
	case outDied:
		return "supervisor"
	case outTimedOut:
		if r.Frozen {
			return "frozen" // the machine froze past the limit
		}
		return "limit"
	case outFailed:
	default:
		return ""
	}
	switch {
	case strings.HasPrefix(r.Error, "the launcher was stopped"):
		return "stopped"
	case strings.HasPrefix(r.Error, "stopped by "):
		return "asked" // maestro-ticket stop
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

// sideFacts are what job, again and resume know as they start a run that
// its record doesn't hold: written to runs/<id>.stats before its
// supervisor starts, read into its row at its end, so a run that ends at
// once still has them. Names from the lists, numbers, and the finding ids
// --drop named.
type sideFacts struct {
	Why         string   `json:"why,omitempty"`
	Tier        string   `json:"tier,omitempty"`
	Tries       *int     `json:"tries,omitempty"`
	AgainOf     string   `json:"again_of,omitempty"`
	Drops       []string `json:"drops,omitempty"`
	PromptBytes int      `json:"prompt_bytes"`
	// Stream says its supervisor reads the run's stdout as stream-json.
	Stream bool `json:"stream,omitempty"`
}

// sideExt ends a run's side file; runs.Prune removes it with the run.
const sideExt = ".stats"

// sideMax is the most of a side file a run's end reads: a few hundred
// bytes is all one holds.
const sideMax = 64 << 10

// fromTicket takes a ticket's tier and send-backs as the job saw them.
func (s *sideFacts) fromTicket(t ticket.Ticket) {
	s.Tier, s.Tries = cmp.Or(t.Tier, "none"), ptr(t.Tries)
}

// fromParent takes the tier and send-backs of the run a resume or a retry
// carries on, if its side file is there.
func (s *sideFacts) fromParent(runsDir, id string) {
	if p, ok := readSide(runsDir, id); ok {
		s.Tier, s.Tries = p.Tier, p.Tries
	}
}

// writeSide writes run id's side file whole, through a rename; nil writes
// none. A write that fails costs only the row's side facts.
func writeSide(runsDir, id string, s *sideFacts) {
	if s == nil {
		return
	}
	if b, err := json.Marshal(s); err == nil {
		render.WriteAtomic(filepath.Join(runsDir, id+sideExt), append(b, '\n'))
	}
}

// readSide reads run id's side file, if there is a whole one.
func readSide(runsDir, id string) (sideFacts, bool) {
	var s sideFacts
	f, err := os.Open(filepath.Join(runsDir, id+sideExt))
	if err != nil {
		return s, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, sideMax+1))
	if err != nil || len(b) > sideMax || json.Unmarshal(b, &s) != nil {
		return sideFacts{}, false
	}
	return s, true
}

// startedOff reports a run that started with stats off, by the shell's
// variable or the marker: a job, again or resume run with no side file.
// No row of it is written after, and stats health doesn't count it.
func startedOff(runsDir string, r runs.Record) bool {
	if r.Job == "" && r.ResumeOf == "" {
		return false
	}
	_, err := os.Lstat(filepath.Join(runsDir, r.ID+sideExt))
	return errors.Is(err, fs.ErrNotExist)
}

// streamOK says a run's stdout may go through the stream filter: on
// Linux, where its supervisor's death ends the command, and while the
// store's disk has the room claim asks for, since the stream's file
// holds a whole run's transcript until the run ends.
func (c *call) streamOK(store string) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	min, err := c.minFree()
	return err == nil && c.headroom(store, min) == nil
}

// streamNames are the lists the supervisor of run id scans its stream
// with, or nil when its side file doesn't ask for a stream or stats went
// off since it started.
func (c *call) streamNames(runsDir, id string) *streamfilter.Names {
	sf, ok := readSide(runsDir, id)
	if !ok || !sf.Stream || stats.Off(filepath.Dir(runsDir), c.env.Getenv, c.statsUnsafe()) != "" {
		return nil
	}
	n := scanNames()
	return &n
}

// scanNames are the lists a stream is scanned with.
func scanNames() streamfilter.Names {
	return streamfilter.Names{Sub: commandNames(), Model: modelFamily}
}

// commandNames are maestro-ticket's subcommands.
func commandNames() []string { return slices.Clone(subNames) }

// subNames is filled from commands in init: read straight from a
// function that commands reaches, commands would wait on itself.
var subNames []string

func init() {
	for _, k := range commands {
		subNames = append(subNames, k.name)
	}
}

// streamFacts puts what the stream filter read into the row. A run with
// no result gets its tokens from the stream's own turns.
func streamFacts(row *stats.Run, r runs.Record, side sideFacts, noResult bool, inSupervisor bool) {
	s := r.Stream
	switch {
	case s != nil && s.Result:
		row.Stream = "ok"
	case s != nil:
		row.Stream = "no_result"
	case side.Stream && !inSupervisor && r.State == runs.Died:
		row.Stream = "lost" // its supervisor died, and its stream file is gone
		return
	default:
		return
	}
	row.CtxPeak, row.Compactions, row.Retries = ptr(s.CtxPeak), ptr(s.Compactions), ptr(s.Retries)
	row.Subagents, row.ToolCalls, row.ToolFails = ptr(s.Subagents), ptr(s.ToolCalls), ptr(s.ToolFails)
	row.VClaude = s.Version
	if s.OpenCode != "" {
		row.OpenCode = s.OpenCode
		if !s.OpenAt.IsZero() && r.EndedAt != nil {
			row.OpenMs = ptr(max(0, r.EndedAt.Sub(s.OpenAt).Milliseconds()))
		}
	}
	for _, c := range s.Tail {
		row.Tail = append(row.Tail, stats.TailCall{Code: c.Code, Fail: c.Fail})
	}
	for _, st := range s.Steps {
		row.Steps = append(row.Steps, stats.Step{Code: st.Code, Calls: st.Calls, Fails: st.Fails, MsSum: st.MsSum, MsMax: st.MsMax})
	}
	if noResult {
		for _, u := range s.Usage {
			row.Models = append(row.Models, stats.Model{Model: u.Model, InputCum: u.Input, OutputCum: u.Output,
				CacheReadCum: u.CacheRead, CacheWriteCum: u.CacheWrite, CostSrc: "stream"})
		}
	}
}

// statsRow is the run's row: what its record, its side file, its
// supervisor, its result and the machine say at its end.
func (c *call) statsRow(storeDir string, r runs.Record, res claudeReply, resErr error) stats.Run {
	outcome, _ := outcomeOf(r)
	row := stats.Run{
		Run: r.ID, Session: sessionOf(r), ResumeOf: r.ResumeOf, Ticket: int64(runTicket(r)),
		Role: statsRole(r.Launcher), Job: cmp.Or(r.Job, "launch"), Nudge: cmp.Or(r.Nudge, "none"),
		State: r.State, Outcome: outcome, Reason: reasonOf(r, outcome, res, resErr),
		AfterDeath: r.AfterDeath, StartedAt: r.StartedAt, ReplyBytes: int64(len(r.Reply)),
		Stream: "off", OS: runtime.GOOS, Arch: runtime.GOARCH, VTool: vTool(), VMaestro: c.statsSha,
	}
	if !c.inSupervisor {
		row.VMaestro = stats.Sha(storeDir)
	}
	row.Model, row.Effort, row.Lean = argvFacts(r.Command)
	sf, ok := readSide(filepath.Join(storeDir, "runs"), r.ID)
	if ok {
		row.Why, row.Tier, row.AgainOf = sf.Why, sf.Tier, sf.AgainOf
		row.PromptBytes, row.Drops = ptr(int64(sf.PromptBytes)), ptr(int64(len(sf.Drops)))
		if sf.Tries != nil {
			row.Tries = ptr(int64(*sf.Tries))
		}
	}
	reviewFacts(&row, r.Job, r.Reply, sf.Drops)
	streamFacts(&row, r, sf, resErr != nil, c.inSupervisor)
	if e := r.End; e != nil {
		row.WaitMs, row.WaitCapped = ptr(e.WaitMs), ptr(e.WaitCapped)
		row.Stop = e.Stop
		if runs.CanScan { // off Linux no scan counts them: unknown, not none
			row.Escaped = ptr(int64(e.Escaped))
		}
	}
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
		// Its supervisor dropped the row of a run started with stats off
		// in its shell; this shell may have them on.
		if startedOff(runsDir, r) {
			return
		}
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
