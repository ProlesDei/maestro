package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// The Stop hook. job passes Claude Code a --settings whose Stop hook runs
// stop-check on a build, review, QA, live or design session, so the
// session can't end without the note its job leaves; else its end routes
// as a mismatch and wakes the planner. It blocks at most stopBlocks times
// a run and fails open: on any error, or whenever an end is no fault of
// the session's, it lets the session end and prints nothing.

// stopHookTimeout is the seconds Claude Code gives the hook.
const stopHookTimeout = 30

// stopBlocks is how often the hook blocks one run's end.
const stopBlocks = 2

// stopLeft is the time a run must have left for the hook to block: a
// session told to go on with less only times out.
const stopLeft = 3 * time.Minute

// shq quotes s for sh -c, which runs a hook's command.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// stopSettings is the --settings JSON that runs exe's stop-check on the
// store at store. The store rides in the command, not the session's
// environment, where every maestro-ticket the session ran, in a scratch
// repo too, would take it for its own.
func stopSettings(exe, store string) string {
	hook := map[string]any{"type": "command", "command": shq(exe) + " stop-check --store " + shq(store), "timeout": stopHookTimeout}
	b, _ := json.Marshal(map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{hook}}}}})
	return string(b)
}

// withStopHook is cmd with any --settings pair dropped and, for a hooked
// job, one fresh pair after it, on this binary and store as they are now:
// a resume or retry of a run an upgrade moved runs today's stop-check.
func withStopHook(cmd []string, hooked bool, exe, store string) []string {
	out := make([]string, 0, len(cmd)+2)
	for i := 0; i < len(cmd); i++ {
		if cmd[i] == "--settings" {
			i++ // and its value
			continue
		}
		if strings.HasPrefix(cmd[i], "--settings=") {
			continue
		}
		out = append(out, cmd[i])
	}
	if hooked {
		out = append(out, "--settings", stopSettings(exe, store))
	}
	return out
}

// stopHook is withStopHook for a run of job on this call's store.
func (c *call) stopHook(cmd []string, job string) ([]string, error) {
	k, ok := findJob(job)
	if !ok {
		return cmd, nil // a plain launch's command is the launcher's own
	}
	store, _, err := c.storePath()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "maestro-ticket"
	}
	return withStopHook(cmd, k.stopHook, exe, store), nil
}

// stopInput is what Claude Code sends a Stop hook on stdin.
type stopInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	StopHookActive bool   `json:"stop_hook_active"`
	// Newer Claude Code sends the reply; else it is read from the
	// transcript.
	LastAssistantMessage string `json:"last_assistant_message"`
}

// stopNotes are the notes that end each hooked job: one posted since its
// session began lets it end.
var stopNotes = map[string][]string{
	"build":  {"Handoff:", "Escalation:"},
	"review": {"Review:", "Kickback:", "Escalation:"},
	"qa":     {"QA:", "Kickback: QA fail", "Rereview:", "Escalation:"},
	"live":   {"Live run:", "Hold:", "Escalation:"},
	"design": {"Design spec:", "Escalation:", "Sent up:"},
}

// qaEndText is a QA or live reply that tested nothing and says why.
var qaEndText = regexp.MustCompile(`(?i)^[a-z][a-z -]*: (refused|blocked)\b`)

// runStopCheck is the Stop hook. It prints a block only when the session
// ended its job with nothing to show; every other way, it exits 0 and
// prints nothing.
func runStopCheck(ctx context.Context, c *call) (err error) {
	defer func() {
		if recover() != nil {
			err = nil
		}
	}()
	if c.parse() != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, (stopHookTimeout-5)*time.Second)
	defer cancel()
	if why := c.stopCheck(ctx); why != "" {
		b, _ := json.Marshal(map[string]string{"decision": "block", "reason": why})
		fmt.Fprintln(c.env.Stdout, string(b))
	}
	return nil
}

// stopCheck is why the session must go on, or "" to let it end.
func (c *call) stopCheck(ctx context.Context) string {
	id := c.env.Getenv(runs.RunIDEnv)
	if !runs.ValidID(id) {
		return ""
	}
	b, err := c.readStdin()
	if err != nil {
		return ""
	}
	var in stopInput
	if json.Unmarshal(b, &in) != nil {
		return ""
	}
	dir, err := c.besideStore("runs")
	if err != nil {
		return ""
	}
	r, err := runs.Load(dir, id)
	if err != nil {
		return ""
	}
	k, ok := findJob(r.Job)
	if !ok || !k.stopHook || r.Ticket == "" || in.SessionID == "" || in.SessionID != sessionOf(r) {
		return "" // a session this run didn't start, such as one it ran itself
	}
	if _, asked := runs.StopAsked(dir, r.ID); asked || c.mayStart() != nil {
		return "" // stopped, or a pause: nothing it does now gets far
	}
	now := c.now()
	limit := r.LimitDuration()
	credit, _ := runs.Credit(dir, r.ID, r.StartedAt, now, limit)
	if r.StartedAt.Add(limit+credit).Sub(now) < stopLeft {
		return ""
	}
	msg := in.LastAssistantMessage
	if msg == "" {
		msg = lastAssistantText(in.TranscriptPath)
	}
	if usageLimitText.MatchString(firstLine(msg)) || usageLimitExact.MatchString(firstLine(msg)) {
		return ""
	}
	if ended, err := c.stopEnded(ctx, dir, r, msg); err != nil || ended {
		return ""
	}
	marks := stopMarks(dir, r.ID)
	if in.StopHookActive || marks >= stopBlocks {
		if marks > 0 {
			addStopMark(dir, r.ID, stopCapped)
		}
		return ""
	}
	if !addStopMark(dir, r.ID, stopBlocked) {
		return "" // with no mark, no cap: let it end
	}
	return fmt.Sprintf("Your %s job on %s left no note. Wait for your subagents and background commands, then post your note on %s, or reply `%s: refused: <why>`.", r.Job, r.Ticket, r.Ticket, r.Job)
}

// stopEnded reports whether r's session ended its job one of the ways its
// job's end routes: a note since the session began, the ticket moved or
// held by another, or a reply its route reads.
func (c *call) stopEnded(ctx context.Context, dir string, r runs.Record, msg string) (bool, error) {
	head := firstLine(msg)
	switch {
	case refusedText.MatchString(head):
		return true, nil
	case r.Job == "build" && flagValue(r.Command, "--model") == modelSonnet && strings.HasSuffix(lastLine(msg), "?"):
		return true, nil // the junior's question goes on, on Opus
	case r.Job == "review" && (fullModelText.MatchString(head) || strings.Contains(msg, "REPORT-ONLY")):
		return true, nil
	case (r.Job == "qa" || r.Job == "live") && qaEndText.MatchString(head):
		return true, nil
	}
	// The session began with its first run: a note from before a resume
	// counts.
	first := r
	for range 50 {
		if first.ResumeOf == "" {
			break
		}
		p, err := runs.Load(dir, first.ResumeOf)
		if err != nil {
			return false, err
		}
		first = p
	}
	if r.Job == "review" {
		if p, err := os.ReadFile(filepath.Join(dir, first.ID+".in")); err == nil && bytes.Contains(p, []byte("--report-only")) {
			return true, nil
		}
	}
	d, err := c.detailForJob(ctx, r.Ticket)
	if err != nil {
		return false, err
	}
	if cl := d.LiveClaim(); cl != nil && ticket.HolderBot(cl.Holder) != ticket.HolderBot(r.Launcher) {
		return true, nil // another run holds it: the session refused
	}
	if !slices.Contains(jobStatuses[r.Job], d.Status) {
		return true, nil
	}
	for _, cm := range d.Comments {
		if cm.CreatedAt.Before(first.StartedAt) {
			continue
		}
		for _, p := range stopNotes[r.Job] {
			if strings.HasPrefix(cm.Body, p) {
				return true, nil
			}
		}
	}
	return false, nil
}

// The marks the hook keeps in runs/<id>.stopcheck, one a line: a block,
// and an end it let through after blocking, past its cap.
const (
	stopBlocked = "block"
	stopCapped  = "capped"
)

func stopMarkPath(dir, id string) string { return filepath.Join(dir, id+".stopcheck") }

// addStopMark appends one mark, and reports whether it was written.
func addStopMark(dir, id, mark string) bool {
	f, err := os.OpenFile(stopMarkPath(dir, id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	_, err = f.WriteString(mark + "\n")
	return f.Close() == nil && err == nil
}

// stopMarks counts the times the hook blocked run id's end.
func stopMarks(dir, id string) int {
	b, _ := os.ReadFile(stopMarkPath(dir, id))
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if l == stopBlocked {
			n++
		}
	}
	return n
}

// stopWasCapped reports whether run id's session ended past the hook's
// blocks with no note.
func stopWasCapped(dir, id string) bool {
	b, _ := os.ReadFile(stopMarkPath(dir, id))
	return slices.Contains(strings.Split(string(b), "\n"), stopCapped)
}

// stopCapped is stopWasCapped for a run on this call's store.
func (c *call) stopCapped(id string) bool {
	dir, err := c.besideStore("runs")
	return err == nil && stopWasCapped(dir, id)
}

// lastAssistantText is the text of the last assistant message in a
// Claude Code transcript, or "".
func lastAssistantText(path string) string {
	if path == "" {
		return ""
	}
	b, err := runs.ReadTail(path, 1<<20)
	if err != nil {
		return ""
	}
	text := ""
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var line struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "assistant" {
			continue
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(line.Message.Content, &parts) != nil {
			continue
		}
		t := ""
		for _, p := range parts {
			if p.Type == "text" {
				t += p.Text
			}
		}
		if strings.TrimSpace(t) != "" {
			text = t
		}
	}
	return text
}
