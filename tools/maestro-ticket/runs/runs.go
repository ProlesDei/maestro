// Package runs records the Claude Code sessions a bot starts with
// maestro-ticket launch: who launched each, its ticket and time limit, and
// its result once it ends or dies, so the bot learns of it without
// blocking on it.
//
// A run is a few files in one directory, which every worktree shares (runs/
// next to the ticket store): <id>.json, the record; <id>.in, .out and .err,
// the command's stdin, stdout and stderr; <id>.lock, which the supervisor
// holds while it lives; and <id>.picked, made once when the result is
// reported. Only one process writes a record at a time: launch until the
// supervisor starts, then the supervisor. Readers never write it.
package runs

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/render"
)

// States. Starting and running are live; the rest are ended. Died is never
// written: a reader finds it when a live record's supervisor is gone.
const (
	Starting = "starting"
	Running  = "running"
	Done     = "done"      // exit 0
	Failed   = "failed"    // exit not 0, a start that failed, or a stopped launcher
	TimedOut = "timed_out" // the time limit ended it
	Died     = "died"      // the supervisor went away with no result
	// Unreadable is a record List couldn't read, such as one a crash cut
	// short: never written, so it shows instead of vanishing.
	Unreadable = "unreadable"
)

// startGrace is how long a record may stay starting before a reader that
// finds no supervisor calls it died.
const startGrace = 30 * time.Second

// Record is one run.
type Record struct {
	ID        string     `json:"id"`
	Launcher  string     `json:"launcher"`
	Ticket    string     `json:"ticket,omitempty"`
	Limit     string     `json:"limit"`
	Command   []string   `json:"command"`
	Dir       string     `json:"dir"`
	StartedAt time.Time  `json:"started_at"`
	State     string     `json:"state"`
	PID       int        `json:"pid,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Exit      *int       `json:"exit,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
	IsError   bool       `json:"is_error,omitempty"`
	Reply     string     `json:"reply,omitempty"`
	Error     string     `json:"error,omitempty"`
	Rings     int        `json:"rings,omitempty"`
	RingError string     `json:"ring_error,omitempty"`
	// Filled in by readers, never stored.
	PickedAt *time.Time `json:"picked_at,omitempty"`
	PickedBy string     `json:"picked_by,omitempty"`
}

// Ended reports whether the run is over.
func (r Record) Ended() bool { return r.State != Starting && r.State != Running }

// LimitDuration is the run's time limit.
func (r Record) LimitDuration() time.Duration {
	d, _ := time.ParseDuration(r.Limit)
	return d
}

var idPattern = regexp.MustCompile(`^R-[a-z2-7]{8}$`)

// ValidID reports whether s looks like a run id.
func ValidID(s string) bool { return idPattern.MatchString(s) }

func path(dir, id, ext string) string { return filepath.Join(dir, id+ext) }

// Create reserves a new run id in dir, saves stdin as its .in file and
// writes its first record, in state starting.
func Create(dir string, r Record, stdin []byte) (Record, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return r, err
	}
	for range 10 {
		r.ID = newID()
		// The .in file reserves the id; readers list only .json files, which
		// Save writes whole.
		f, err := os.OpenFile(path(dir, r.ID, ".in"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return r, err
		}
		_, err = f.Write(stdin)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return r, err
		}
		r.State = Starting
		return r, Save(dir, r)
	}
	return r, errors.New("could not pick a free run id")
}

func newID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return "R-" + string(b)
}

// Save writes r whole, synced, through a rename, so a reader never sees
// half a record, even after a crash.
func Save(dir string, r Record) error {
	r.PickedAt, r.PickedBy = nil, ""
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return render.WriteAtomic(path(dir, r.ID, ".json"), append(b, '\n'))
}

// Load reads one record as stored, without the reader's view.
func Load(dir, id string) (Record, error) {
	var r Record
	b, err := os.ReadFile(path(dir, id, ".json"))
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	return r, err
}

// Get reads one record as a reader sees it: a live record whose supervisor
// is gone is died, and the pick, if any, is filled in.
func Get(dir, id string, now time.Time) (Record, error) {
	r, err := Load(dir, id)
	if err != nil {
		return r, err
	}
	if !r.Ended() && supervisorGone(dir, r, now) {
		// It may have saved its result and exited since the first read.
		if r, err = Load(dir, id); err != nil {
			return r, err
		}
		if !r.Ended() {
			r.State = Died
		}
	}
	if b, err := os.ReadFile(path(dir, id, ".picked")); err == nil {
		var p pick
		if json.Unmarshal(b, &p) == nil {
			r.PickedAt, r.PickedBy = &p.At, p.By
		}
	}
	return r, nil
}

// supervisorGone reports whether a live record has no supervisor. The
// supervisor locks <id>.lock before it writes running and holds it until
// it exits, however it exits, so a lock anyone can take means it is gone.
func supervisorGone(dir string, r Record, now time.Time) bool {
	if r.State == Starting && now.Sub(r.StartedAt) < startGrace {
		return false
	}
	f, err := os.OpenFile(path(dir, r.ID, ".lock"), os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	defer f.Close()
	ok, err := tryLock(f)
	return ok && err == nil
}

// List reads the records in dir, oldest first; with picked false, only
// those whose result wasn't picked up, which it finds without reading the
// rest. A record it can't read lists as unreadable, so no result goes
// missing without a word. A missing dir has none.
func List(dir string, now time.Time, picked bool) ([]Record, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !ValidID(id) || (!picked && Picked(dir, id)) {
			continue
		}
		r, err := Get(dir, id, now)
		if errors.Is(err, fs.ErrNotExist) {
			continue // pruned since ReadDir
		}
		if err != nil {
			r = Record{ID: id, State: Unreadable, Error: "record unreadable: " + err.Error()}
			if info, serr := e.Info(); serr == nil {
				r.StartedAt = info.ModTime()
			}
		}
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b Record) int { return a.StartedAt.Compare(b.StartedAt) })
	return out, nil
}

type pick struct {
	By string    `json:"by"`
	At time.Time `json:"at"`
}

// Pick marks an ended run's result as reported. Only the first caller
// gets true, however many pick at once, so each result is reported once.
func Pick(dir, id, by string, now time.Time) (bool, error) {
	f, err := os.OpenFile(path(dir, id, ".picked"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	b, _ := json.Marshal(pick{By: by, At: now})
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return true, err
}

// Picked reports whether id's result was reported.
func Picked(dir, id string) bool {
	_, err := os.Stat(path(dir, id, ".picked"))
	return err == nil
}

// Prune removes every file of a run picked up longer than keep ago. A
// result no one picked up stays, however old, and doctor flags it.
func Prune(dir string, keep time.Duration, now time.Time) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".picked")
		if !ok || !ValidID(id) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < keep {
			continue
		}
		// .json first, since List finds runs by it, and .picked last: while
		// .picked stays, no pickup takes the run again.
		for _, ext := range []string{".json", ".in", ".out", ".err", ".lock", ".picked"} {
			os.Remove(path(dir, id, ext))
		}
	}
	return nil
}

// reply is the part of claude -p --output-format json (or the last line
// of stream-json) that a bot reads.
type reply struct {
	Result    *string `json:"result"`
	SessionID string  `json:"session_id"`
	IsError   bool    `json:"is_error"`
}

// maxReply caps a reply kept in the record; the whole output stays in
// <id>.out.
const maxReply = 64 << 10

// OutputWindow is how much of a run's stdout ParseOutput needs: the end,
// where Claude writes its result.
const OutputWindow = 8 << 20

// ParseOutput reads the end of a run's stdout: the result and session id
// of Claude's JSON output, or else the output's tail.
func ParseOutput(out []byte) (text, session string, isError bool) {
	parse := func(b []byte) bool {
		var r reply
		if json.Unmarshal(b, &r) != nil || (r.Result == nil && r.SessionID == "") {
			return false
		}
		if r.Result != nil {
			text = tail(*r.Result, maxReply)
		}
		session, isError = r.SessionID, r.IsError
		return true
	}
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if parse(lines[i]) {
			return
		}
	}
	if parse(out) { // one JSON object over several lines
		return
	}
	return tail(string(bytes.TrimSpace(out)), 4<<10), "", false
}

// ReadTail reads at most the last n bytes of a file.
func ReadTail(name string, n int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := max(0, info.Size()-n)
	b := make([]byte, info.Size()-off)
	_, err = f.ReadAt(b, off)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return b, err
}

// tail keeps the last n bytes of s, starting on a whole rune.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return "…" + s
}
