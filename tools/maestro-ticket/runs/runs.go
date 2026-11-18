// Package runs records the Claude Code sessions a bot starts with
// maestro-ticket launch: who launched each, its ticket and time limit, and
// its result once it ends or dies, so the bot learns of it without
// blocking on it.
//
// A run is a few files in one directory, which every worktree shares (runs/
// next to the ticket store): <id>.json, the record; <id>.in, .out and .err,
// the command's stdin, stdout and stderr; <id>.lock, which the supervisor
// holds while it lives; <id>.acked, made once when the launcher says it
// acted on the result; <id>.stop, made once when someone asks the run to
// stop; <id>.wait.* and <id>.waited.*, the slot waits its command marked
// (wait.go); <id>.stats, the facts job, again and resume know at its start
// that stats.db keeps and the record doesn't (cli); and <id>.stream, a
// streamed run's stdout, which its supervisor reads once the command is
// over and removes once its end is recorded (Options.Stream). A record
// saved as it ended is settling while its supervisor still routes it and
// starts its next step (Options.OnEnd): no reader lists it as a result
// until that is over, so no bot acts on a send the end starts itself.
// Only one process writes a record at a time: launch until the supervisor
// starts, then whoever holds <id>.lock: the supervisor, or, once it went
// away, a reader that Adopt let in.
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
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/envcheck"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/render"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/streamfilter"
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
// finds no supervisor calls it died. It runs from before the record's
// first write, through the supervisor's exec, to its lock: on a box busy
// with hundreds of runs that can take far longer than usual, and a run
// adopted as died before its supervisor locks never runs. A start that
// truly died holds its slot this long.
const startGrace = 5 * time.Minute

// Record is one run.
type Record struct {
	ID       string   `json:"id"`
	Launcher string   `json:"launcher"`
	Ticket   string   `json:"ticket,omitempty"`
	Limit    string   `json:"limit"`
	Command  []string `json:"command"`
	// Job is the kind of work maestro-ticket job started (build, review,
	// qa, ...), which pickup reads to say what the result means; empty for
	// a plain launch.
	Job string `json:"job,omitempty"`
	// Tickets are the tickets a job names beyond Ticket, as a judge run
	// names every ticket it judges, so look counts them busy meanwhile.
	Tickets []string `json:"tickets,omitempty"`
	// ResumeOf is the run whose session this one resumed; AfterDeath marks
	// the one resume a session gets after its run died.
	ResumeOf   string `json:"resume_of,omitempty"`
	AfterDeath bool   `json:"after_death,omitempty"`
	// AgainOf is the run this one started afresh (job again): a run goes
	// again once, so a run that already did goes up when it stops.
	AgainOf string `json:"again_of,omitempty"`
	// Overrides are the --model, --effort and --limit flags its job
	// started with, as its launcher's memories set them: the jobs code
	// starts for that launcher later carry them (cli's lastOverrides).
	Overrides []string `json:"overrides,omitempty"`
	// Nudge is the fixed resume a run began with (opus, escalate,
	// decide): each comes once per session, so a session that asks again
	// goes up instead of round and round.
	Nudge     string     `json:"nudge,omitempty"`
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
	// Frozen marks a time limit that ended a run only because the machine
	// was frozen: by the monotonic clock it had time left. Pickup counts
	// it as no repeat timeout, so one freeze escalates nothing; it reruns
	// it at its limit twice at most per head.
	Frozen    bool   `json:"frozen,omitempty"`
	Rings     int    `json:"rings,omitempty"`
	RingError string `json:"ring_error,omitempty"`
	// Rang is true once a ring reached the webhook; OrphanTries counts the
	// rings readers tried for a run whose supervisor went away first.
	Rang        bool `json:"rang,omitempty"`
	OrphanTries int  `json:"orphan_tries,omitempty"`
	// LaunchID is the run's row in the store's launches, which alerts
	// and export read; 0 for none.
	LaunchID int64 `json:"launch_id,omitempty"`
	// Host is the machine the supervisor runs on. It matters only where
	// the store may sit on a disk machines share: see elsewhere.
	Host string `json:"host,omitempty"`
	// Stamp tells PID's process from a later one that reuses its pid,
	// where a supervisor's death leaves the process running: see Stamp.
	Stamp string `json:"stamp,omitempty"`
	// ChainOf is the run whose end started this one as its fixed next
	// step, and Hops how many such ends in a row led here (cli/chain.go).
	ChainOf string `json:"chain_of,omitempty"`
	Hops    int    `json:"hops,omitempty"`
	// Chained are the handoffs this run's end started itself, each as
	// "<bot>: <line>". Routed is what its end routed, as pickup shows it,
	// kept when the end handed some of it on: pickup shows that and
	// doesn't route again, since the ticket has moved on since. Notify is
	// the bot its end left the rest to, when not its launcher.
	Chained []string        `json:"chained,omitempty"`
	Routed  json.RawMessage `json:"routed,omitempty"`
	Notify  string          `json:"notify,omitempty"`
	// Settling is true while the run's end is still being handled: saved
	// ended, but not yet routed and chained. Readers leave it out of the
	// results until whoever holds its lock clears it. Missing means
	// settled, so an older record reads as before.
	Settling bool `json:"settling,omitempty"`
	// Filled in by readers, never stored.
	AckedAt *time.Time `json:"acked_at,omitempty"`
	AckedBy string     `json:"acked_by,omitempty"`
	// End is what only the supervisor sees as the run ends, for its stats
	// row: never stored, so no reader's bytes change. nil on a record
	// read from disk.
	End *EndFacts `json:"-"`
	// Stream is what the supervisor read from a streamed run's stdout,
	// never stored either; nil for a run that wasn't streamed.
	Stream *streamfilter.Summary `json:"-"`
}

// Stop kinds: how the supervisor ended a run's command.
const (
	StopNone = "none" // it ended on its own
	StopTerm = "term" // a stop or the time limit sent SIGTERM, and that did
	StopKill = "kill" // the grace ran out, and SIGKILL followed
)

// EndFacts are a run's end as its supervisor saw it.
type EndFacts struct {
	// WaitMs is the slot waiting the run's limit left out, as Credit gave
	// it once the command ended; WaitCapped says a cap cut it.
	WaitMs     int64
	WaitCapped bool
	Stop       string
	// Escaped counts the run's processes outside its group that the stop
	// sent SIGTERM: what slot or Claude Code started in a group of its own.
	Escaped int
	// Cause is who stopped the run, if someone did: CauseAsked or
	// CauseLauncher; "" for a run that ended or timed out.
	Cause string
}

// Stop causes.
const (
	CauseAsked    = "asked"    // maestro-ticket stop
	CauseLauncher = "launcher" // the launcher was stopped
)

// Owner is the bot whose pickup shows the run once it ends, who acks it
// and whose webhook rings for it: its launcher, unless its end named
// another in Notify.
func (r Record) Owner() string {
	if r.Notify != "" {
		return r.Notify
	}
	return r.Launcher
}

// Ended reports whether the run is over.
func (r Record) Ended() bool { return r.State != Starting && r.State != Running }

// LauncherStoppedText opens the error of a run whose supervisor was
// stopped, as at a shutdown or reboot, and stopped the run with it.
const LauncherStoppedText = "the launcher was stopped, so it stopped the run"

// LauncherStopped reports whether the run's supervisor was stopped, so it
// stopped the run: the machine went down under it, as a died run's did,
// though its supervisor lived to save the end.
func (r Record) LauncherStopped() bool {
	return r.State == Failed && strings.HasPrefix(r.Error, LauncherStoppedText)
}

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
// A lock on another machine can't be tested, so a supervisor that may be
// elsewhere counts as alive until the run is overdue.
func supervisorGone(dir string, r Record, now time.Time) bool {
	if r.State == Starting && now.Sub(r.StartedAt) < startGrace {
		return false
	}
	far := elsewhere(r)
	f, err := os.OpenFile(path(dir, r.ID, ".lock"), os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return !far || overdue(r, now)
	}
	if err != nil {
		return false
	}
	defer f.Close()
	ok, err := tryLock(f)
	if err != nil {
		return false
	}
	// A lock held here is a live supervisor here, whatever the record's
	// host; a free one may only mean the lock is on another machine.
	return ok && (!far || overdue(r, now)) && !orphanLives(dir, r, now)
}

// orphanLives reports whether a run whose supervisor went away still has
// its session running here, where nothing ended it with the supervisor:
// it is the run's till it ends, its limit passes or someone asks it to
// stop. Then Adopt ends it, as its supervisor would have.
func orphanLives(dir string, r Record, now time.Time) bool {
	if elsewhere(r) || !sessionLeft(r) {
		return false
	}
	if _, asked := StopAsked(dir, r.ID); asked {
		return false
	}
	limit := r.LimitDuration()
	credit, _ := Credit(dir, r.ID, r.StartedAt, now, limit)
	return limit <= 0 || now.Sub(r.StartedAt) <= limit+credit+KillGrace
}

// elsewhere reports whether r's supervisor may run on another machine,
// where its lock can't be tested. The store sits on a disk machines share
// only when envcheck.AllowEnv lets it. Otherwise every supervisor is here,
// and a host that differs is this machine under an old name: a Mac's
// changes with its network, a container's on a restart.
func elsewhere(r Record) bool {
	return r.Host != "" && r.Host != hostname() && sharedStore()
}

// sharedStore reports whether the store may be on a shared disk. Tests
// replace it.
var sharedStore = func() bool { return os.Getenv(envcheck.AllowEnv) == "1" }

// overdue reports whether a run started long enough ago that no
// supervisor, on any machine, would still let it run: past its limit, the
// most slot wait the limit leaves out and its kill grace, with an hour
// for clocks that differ between machines.
func overdue(r Record, now time.Time) bool {
	limit := r.LimitDuration()
	return limit > 0 && Overdue(r.StartedAt, limit, now)
}

// Overdue reports whether a run with limit, started at started, is past
// any time a supervisor on any machine would let it run, as overdue says.
// Another machine's run, known only by its start and limit, holds until
// then.
func Overdue(started time.Time, limit time.Duration, now time.Time) bool {
	return now.Sub(started) > limit+maxSlotCredit+KillGrace+time.Hour
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

// AskStop asks run id's supervisor to stop its command, as its time
// limit would, and record it failed, stopped by by: the supervisor looks
// for the ask every few seconds. Only the first ask counts; a second
// returns false.
func AskStop(dir, id, by string, now time.Time) (bool, error) {
	b, _ := json.Marshal(ack{By: by, At: now})
	return once(path(dir, id, ".stop"), b)
}

// StopAsked reports whether someone asked run id to stop, and who.
func StopAsked(dir, id string) (by string, ok bool) {
	b, err := os.ReadFile(path(dir, id, ".stop"))
	if err != nil {
		return "", false
	}
	// once makes the file, then writes it: an empty one is an ask half
	// made, which the next look reads whole.
	var a ack
	if json.Unmarshal(b, &a) != nil || a.By == "" {
		return "", false
	}
	return a.By, true
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

// Unacked lists the ended runs left to bot that it hasn't acked and whose
// end is settled, oldest first: what its ring and its pickup carry.
func Unacked(dir, bot string, now time.Time) ([]Record, error) {
	list, err := List(dir, now, false)
	out := []Record{}
	for _, r := range list {
		if r.State != Unreadable && r.Ended() && !r.Settling && r.Owner() == bot {
			out = append(out, r)
		}
	}
	return out, err
}

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
// late supervisor, nor any other reader, can write the record. died says
// this call wrote it died, so its end is the caller's to handle; a run
// saved died before had its end handled by an earlier caller. The caller
// rings, saves, then calls release. A caller killed halfway lets go of
// the lock, and the next one takes the run over.
func Adopt(dir, id string, now time.Time) (r Record, release func(), died, ok bool) {
	f, err := os.OpenFile(path(dir, id, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return r, nil, false, false
	}
	if got, err := holdLock(f); err != nil || !got {
		f.Close()
		return r, nil, false, false
	}
	release = func() { f.Close() }
	r, err = Load(dir, id)
	switch {
	case err != nil, Acked(dir, id), r.Rang, r.OrphanTries >= orphanTries,
		// Its lock may be on another machine: it rings its own result,
		// and a live one is its own until it is overdue.
		elsewhere(r) && (r.Ended() || !overdue(r, now)),
		r.State == Starting && now.Sub(r.StartedAt) < startGrace,
		!r.Ended() && orphanLives(dir, r, now):
		release()
		return r, nil, false, false
	case !r.Ended():
		if sessionLeft(r) {
			// Past its limit or asked to stop: slot passes this on to
			// its command, as on its supervisor's stop.
			killGroup(r.PID, syscall.SIGTERM)
		}
		r.State, r.EndedAt = Died, &now
		r.Error = "its supervisor went away with no result: the machine restarted, or it was killed"
		r.Host = hostname() // ours from here on: a failed ring is tried again
		died = true
	}
	return r, release, died, true
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
	pruned := map[string]bool{}
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".acked")
		if !ok || !ValidID(id) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < keep {
			continue
		}
		pruned[id] = true
		// .json first, since List finds runs by it, and .acked last: while
		// .acked stays, no pickup shows the run again.
		for _, ext := range []string{".json", ".in", ".out", ".err", ".lock", ".resumed", ".again", ".stop", ".stopcheck", ".stats", streamExt, ".acked"} {
			os.Remove(path(dir, id, ext))
		}
	}
	// Their slot wait marks, from the same listing: one listing per run
	// would cost the square of the runs.
	for _, e := range ents {
		if id, _, ok := strings.Cut(e.Name(), "."); ok && pruned[id] && isWait(e.Name(), id) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
		// A side file's temp copy that a killed launch left.
		if rest, ok := strings.CutPrefix(e.Name(), "."); ok {
			if id, _, ok := strings.Cut(rest, ".stats.tmp-"); ok && pruned[id] {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
		// An active-run mark a later start moved aside, or one whose run
		// ended a day ago.
		if strings.Contains(e.Name(), ".active.stale") || strings.HasSuffix(e.Name(), ".active") || strings.HasSuffix(e.Name(), ".takeover") {
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > keep {
				os.Remove(filepath.Join(dir, e.Name()))
			}
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
// of Claude's JSON output, or else the output's tail. A streamed run's
// .out holds no stream line but its result (streamfilter.Scan); a
// command run on stream-json itself is read as before.
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
