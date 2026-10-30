package engine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

// Run statuses.
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusBlocked = "blocked"
	StatusLost    = "lost"    // the lease was lost; nothing more was written
	StatusStopped = "stopped" // the stop file exists; resume later
	StatusFailed  = "failed"  // the engine couldn't go on (no harness, a bad playbook)
)

// Phases of a run.
const (
	phaseSteps = "steps"
	phaseDone  = "done"
	phaseBlock = "block"
	phaseEnd   = "end" // on: end: no note, no move; release, log, clean up
)

// State is a run's state, saved at <common>/runs/<run-id>.json after every
// step. It replaces the scratchpad: the token, the holder and every step's
// output live here, so resume starts at the first unfinished step.
type State struct {
	ID       string `json:"id"`
	Playbook string `json:"playbook"` // its folder name
	Role     string `json:"role"`
	Bot      string `json:"bot"`
	Holder   string `json:"holder"`
	Ticket   string `json:"ticket,omitempty"`
	Token    int64  `json:"token,omitempty"` // 0 when the run holds no lease
	// Session is the session id; SessionStarted says whether the next
	// prompt resumes it. Harness runs it (claude or codex), under its own
	// id HarnessSession (claude takes ours; codex picks one).
	Session        string `json:"session"`
	SessionStarted bool   `json:"session_started,omitempty"`
	Harness        string `json:"harness,omitempty"`
	HarnessSession string `json:"harness_session,omitempty"`

	Status string `json:"status"`
	Phase  string `json:"phase"`
	// Cursor is the step to run next. Inflight is the step that was running
	// when the state was saved: on resume it runs again, with the same
	// idempotency keys.
	Cursor   string `json:"cursor"`
	Inflight string `json:"inflight,omitempty"`

	Visits  map[string]int            `json:"visits"`
	Steps   map[string]map[string]any `json:"steps"`
	Skipped []string                  `json:"skipped,omitempty"`
	Env     map[string]any            `json:"env"`
	TicketD map[string]any            `json:"ticket_data,omitempty"`
	Retry   map[string]any            `json:"retry,omitempty"`

	StoppedAt  string `json:"stopped_at,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
	// NoPrompt is set when the block reason forbids another prompt.
	NoPrompt bool `json:"no_prompt,omitempty"`

	// Input is run --input's text; ClaimRole the ticket's role when the run
	// took it (Return-role:); BlockStep the step whose block: applies.
	Input     string `json:"input,omitempty"`
	ClaimRole string `json:"claim_role,omitempty"`
	BlockStep string `json:"block_step,omitempty"`

	Branch    string    `json:"branch,omitempty"`
	Worktree  string    `json:"worktree,omitempty"`
	Processes []Process `json:"processes,omitempty"`

	Escalation map[string]any `json:"escalation,omitempty"`
	// EscalationTicket is the ticket a run with none filed its block on.
	EscalationTicket string          `json:"escalation_ticket,omitempty"`
	Wrapped          map[string]bool `json:"wrapped,omitempty"` // finished wrap-up parts

	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Process is one a prompt reply said it started.
type Process struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

var runIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func newRunID(job, ticketID string, now time.Time) string {
	b := make([]byte, 4)
	rand.Read(b)
	id := job
	if ticketID != "" {
		id += "-" + ticketID
	}
	return fmt.Sprintf("%s-%s-%s", id, now.UTC().Format("20060102T150405"), hex.EncodeToString(b))
}

// newUUID is a random (version 4) UUID.
func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func runsDir(common string) string { return filepath.Join(common, "runs") }

func statePath(common, id string) string { return filepath.Join(runsDir(common), id+".json") }

// runDir holds a run's prompt and schema files.
func runDir(common, id string) string { return filepath.Join(runsDir(common), id+".d") }

// save writes the state atomically: temp file, fsync, rename.
func (e *Engine) save(st *State) error {
	st.Updated = e.now()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(statePath(e.cfg.Common, st.ID), b)
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// LoadState reads a run's state.
func (e *Engine) LoadState(id string) (*State, error) {
	if !runIDRE.MatchString(id) {
		return nil, fmt.Errorf("run id %q: bad id", id)
	}
	b, err := os.ReadFile(statePath(e.cfg.Common, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("run %s: no state at %s", id, statePath(e.cfg.Common, id))
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("run %s: %w", id, err)
	}
	if st.Visits == nil {
		st.Visits = map[string]int{}
	}
	if st.Steps == nil {
		st.Steps = map[string]map[string]any{}
	}
	if st.Wrapped == nil {
		st.Wrapped = map[string]bool{}
	}
	for _, rec := range st.Steps {
		normalize(rec)
	}
	normalize(st.Env)
	normalize(st.TicketD)
	normalize(st.Retry)
	normalize(st.Escalation)
	return &st, nil
}

// lockRun takes the run's lock, <runs>/<id>.d/lock, so one process drives
// a run at a time: a run being driven looks the same on disk as one that
// crashed. ok is false when another process holds it. The lock goes with
// the process, so a crash frees it.
func (e *Engine) lockRun(id string) (unlock func(), ok bool, err error) {
	dir := runDir(e.cfg.Common, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() { f.Close() }, true, nil
}
