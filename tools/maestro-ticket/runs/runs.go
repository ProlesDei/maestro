// Package runs records the Claude Code sessions a bot starts with
// maestro-ticket launch: who launched each, its ticket and time limit, and
// its result once it ends or dies, so the bot learns of it without
// blocking on it.
//
// A run is a few files in one directory, which every worktree shares (runs/
// next to the ticket store): <id>.json, the record; <id>.in, .out and .err,
// the command's stdin, stdout and stderr; <id>.lock, which the supervisor
// holds while it lives; and <id>.acked, made once when the launcher says it
// acted on the result. Only one process writes a record at a time: launch
// until the supervisor starts, then whoever holds <id>.lock: the
// supervisor, or, once it went away, a reader that Adopt let in.
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
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/render"
)

// States. Starting and running are live; the rest are ended. A reader
// finds died when a record's supervisor is gone before it saved a result;
// the reader that adopts it writes it.
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
	// Rang is true once a ring reached the webhook; OrphanTries counts the
	// rings readers tried for a run whose supervisor went away first.
	Rang        bool `json:"rang,omitempty"`
	OrphanTries int  `json:"orphan_tries,omitempty"`
	// Host is the machine the supervisor runs on: only there can a reader
	// tell whether it still lives.
	Host string `json:"host,omitempty"`
	// Filled in by readers, never stored.
	AckedAt *time.Time `json:"acked_at,omitempty"`
	AckedBy string     `json:"acked_by,omitempty"`
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
		r.Host = hostname()
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
	r.AckedAt, r.AckedBy = nil, ""
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
// is gone is died, and the ack, if any, is filled in.
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
	if b, err := os.ReadFile(path(dir, id, ".acked")); err == nil {
		var a ack
		if json.Unmarshal(b, &a) == nil {
			r.AckedAt, r.AckedBy = &a.At, a.By
		}
	}
	return r, nil
}

// supervisorGone reports whether a live record has no supervisor. The
// supervisor locks <id>.lock before it writes running and holds it until
// it exits, however it exits, so a lock anyone can take means it is gone.
// A lock on another machine can't be tested, so that supervisor counts as
// alive.
func supervisorGone(dir string, r Record, now time.Time) bool {
	if r.State == Starting && now.Sub(r.StartedAt) < startGrace {
		return false
	}
	if r.Host != "" && r.Host != hostname() {
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

// List reads the records in dir, oldest first; with acked false, only
// those whose result wasn't acked, which it finds without reading the
// rest. A record it can't read lists as unreadable, so no result goes
// missing without a word. A missing dir has none.
func List(dir string, now time.Time, acked bool) ([]Record, error) {
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
		if !ok || !ValidID(id) || (!acked && Acked(dir, id)) {
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

type ack struct {
	By string    `json:"by"`
	At time.Time `json:"at"`
}

// Ack marks an ended run's result as acted on: it leaves pickup and the
// rings stop. Only the first caller gets true, however many ack at once,
// so each result is acked once.
func Ack(dir, id, by string, now time.Time) (bool, error) {
	b, _ := json.Marshal(ack{By: by, At: now})
	return once(path(dir, id, ".acked"), b)
}

// Acked reports whether id's result was acked.
func Acked(dir, id string) bool {
	_, err := os.Stat(path(dir, id, ".acked"))
	return err == nil
}

// once makes name with body, only if it doesn't exist yet: true for the
// one caller that made it.
func once(name string, body []byte) (bool, error) {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = f.Write(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return true, err
}

// orphanTries caps how often readers ring for one orphan whose rings
// fail, so a webhook that is down can't slow every pickup for good.
const orphanTries = 5

// Orphans lists the unacked runs whose supervisor may have gone away
// before a ring reached the launcher: a died run, or an ended one that
// never rang. Adopt takes each for one caller.
func Orphans(dir string, now time.Time) ([]string, error) {
	list, err := List(dir, now, false)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range list {
		if r.State != Unreadable && r.Ended() && !r.Rang && r.OrphanTries < orphanTries {
			out = append(out, r.ID)
		}
	}
	return out, nil
}

// Adopt takes the run's lock in place of its supervisor, if no one holds
// it, and returns the record as stored, with a live one written as died:
// a supervisor holds that lock from before it writes running until it
// exits, so a free lock means it is gone, and while the caller holds it no
// late supervisor, nor any other reader, can write the record. The caller
// rings, saves, then calls release. A caller killed halfway lets go of
// the lock, and the next one takes the run over.
func Adopt(dir, id string, now time.Time) (r Record, release func(), ok bool) {
	f, err := os.OpenFile(path(dir, id, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return r, nil, false
	}
	if got, err := holdLock(f); err != nil || !got {
		f.Close()
		return r, nil, false
	}
	release = func() { f.Close() }
	r, err = Load(dir, id)
	switch {
	case err != nil, Acked(dir, id), r.Rang, r.OrphanTries >= orphanTries,
		r.Host != "" && r.Host != hostname(), // its lock is on another machine
		r.State == Starting && now.Sub(r.StartedAt) < startGrace:
		release()
		return r, nil, false
	case !r.Ended():
		r.State, r.EndedAt = Died, &now
		r.Error = "its supervisor went away with no result: the machine restarted, or it was killed"
	}
	return r, release, true
}

var host = sync.OnceValue(func() string {
	h, _ := os.Hostname()
	return h
})

func hostname() string { return host() }

// Prune removes every file of a run acked longer than keep ago. A result
// no one acked stays, however old, and doctor flags it.
func Prune(dir string, keep time.Duration, now time.Time) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".acked")
		if !ok || !ValidID(id) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < keep {
			continue
		}
		// .json first, since List finds runs by it, and .acked last: while
		// .acked stays, no pickup shows the run again.
		for _, ext := range []string{".json", ".in", ".out", ".err", ".lock", ".acked"} {
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
