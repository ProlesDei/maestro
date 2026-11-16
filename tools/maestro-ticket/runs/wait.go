package runs

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A launched command that waits for a maestro-ticket slot marks the wait in
// the run's folder, and the supervisor leaves the wait out of the run's
// time limit, as Credit says: a run that spends its limit in a queue
// runs nothing and gives no result.
//
// Each wait is one empty file, <id>.wait.<start in unix nanoseconds>-<pid>
// (with -s after a session's own wait to start),
// whose mtime the waiter moves to now while it waits. Once it has a slot or
// gives up, it renames the file to <id>.waited.<...>, so the mtime is the
// end. A waiter killed mid-wait stops moving the mtime, so its wait counts
// up to its last touch.

// maxSlotCredit is the most slot waiting any run's time limit leaves out,
// so a machine whose slots never free can't keep a run alive for good. A
// var only so tests can shorten it.
var maxSlotCredit = 8 * time.Hour

// Credit is how much slot waiting a run with this limit leaves out of it:
// a wait for its session to start, up to maxSlotCredit, since nothing ran
// yet; a wait inside the session, up to twice the limit, so a session
// lasts at most three times its limit however long the line. A session
// that waits hours outlives its prompt cache, and each turn after writes
// it again. capped reports whether a cap cut it.
func Credit(dir, id string, start, now time.Time, limit time.Duration) (credit time.Duration, capped bool) {
	marks := waits(dir, id) // one read for both, so a wait ending between them counts once
	before := slotWait(dir, marks, start, now, func(n string) bool { return strings.HasSuffix(n, startMark) })
	inside := slotWait(dir, marks, start, now, func(n string) bool { return !strings.HasSuffix(n, startMark) })
	most := min(2*limit, maxSlotCredit)
	capped = before > maxSlotCredit || inside > most
	credit = min(before, maxSlotCredit) + min(inside, most)
	if credit > maxSlotCredit {
		credit, capped = maxSlotCredit, true
	}
	return credit, capped
}

// startMark ends the mark of a wait for a run's session to start, the
// wait of a claude command in a slot.
const startMark = "-s"

// liveWait: a waiter that touched its file this recently still waits.
const liveWait = 5 * time.Second

// Wait is one slot wait a launched command marked.
type Wait struct {
	file    string
	dir, id string
	session bool
	last    time.Time // the last touch, on the wall clock
}

// StartWait marks a slot wait for run id in dir. With no such run in dir,
// it marks nothing and returns nil: the waiter wasn't launched from this
// store, so no supervisor reads the mark.
// session says the waiter is the run's session itself, not yet started.
func StartWait(dir, id string, now time.Time, session bool) (*Wait, error) {
	if !ValidID(id) {
		return nil, nil
	}
	if _, err := os.Stat(path(dir, id, ".json")); err != nil {
		return nil, nil
	}
	mark := ""
	if session {
		mark = startMark
	}
	name := path(dir, id, fmt.Sprintf(".wait.%d-%d%s", now.UnixNano(), os.Getpid(), mark))
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	f.Close()
	return &Wait{file: name, dir: dir, id: id, session: session, last: now.Round(0)}, nil
}

// Touch says the wait goes on. Errors are ignored: the run may have ended
// and its supervisor removed the mark. A waiter touches every second or
// so, so a gap of FreezeGap or more on the wall clock means the machine
// was frozen or the waiter stopped: the mark ends at the last touch and a
// new one starts, so the gap earns the run no credit. The run's limit
// counts a freeze, and a frozen wait waited on nothing.
func (w *Wait) Touch(now time.Time) {
	if w == nil {
		return
	}
	now = now.Round(0) // the wall clock: the monotonic one stops in a freeze
	if !w.last.IsZero() && now.Sub(w.last) >= FreezeGap {
		w.End(w.last)
		if n, err := StartWait(w.dir, w.id, now, w.session); err == nil && n != nil {
			*w = *n
		}
		return
	}
	w.last = now
	if os.Chtimes(w.file, now, now) != nil {
		w.last = time.Time{} // the mark is gone with its run: start no new one
	}
}

// End marks the wait over at now.
func (w *Wait) End(now time.Time) {
	if w == nil {
		return
	}
	if os.Chtimes(w.file, now, now) == nil {
		dir, base := filepath.Split(w.file)
		os.Rename(w.file, filepath.Join(dir, strings.Replace(base, ".wait.", ".waited.", 1)))
	}
}

// SlotWait is how long run id's commands waited for slots between start
// and now, overlapping waits counted once.
func SlotWait(dir, id string, start, now time.Time) time.Duration {
	return slotWait(dir, waits(dir, id), start, now, func(string) bool { return true })
}

// slotWait is how long the marks keep picks waited.
func slotWait(dir string, marks map[string]bool, start, now time.Time, keep func(name string) bool) time.Duration {
	type span struct{ from, to time.Time }
	var spans []span
	for name, live := range marks {
		if !keep(name) {
			continue
		}
		nanos, _, ok := strings.Cut(name[strings.LastIndex(name, ".")+1:], "-")
		n, err := strconv.ParseInt(nanos, 10, 64)
		info, serr := os.Stat(filepath.Join(dir, name))
		if serr != nil && live {
			// It ended since the read: its end is under its new name.
			info, serr = os.Stat(filepath.Join(dir, strings.Replace(name, ".wait.", ".waited.", 1)))
			live = false
		}
		if !ok || err != nil || serr != nil {
			continue
		}
		s := span{time.Unix(0, n), info.ModTime()}
		if live && now.Sub(s.to) < liveWait {
			s.to = now
		}
		s.from, s.to = later(s.from, start), earlier(s.to, now)
		if s.to.After(s.from) {
			spans = append(spans, s)
		}
	}
	slices.SortFunc(spans, func(a, b span) int { return a.from.Compare(b.from) })
	var total time.Duration
	var end time.Time
	for _, s := range spans {
		s.from = later(s.from, end)
		if s.to.After(s.from) {
			total += s.to.Sub(s.from)
			end = s.to
		}
	}
	return total
}

// waits lists run id's wait marks in dir, each with whether its waiter
// hadn't ended it.
func waits(dir, id string) map[string]bool {
	ents, _ := os.ReadDir(dir)
	out := map[string]bool{}
	for _, e := range ents {
		if n := e.Name(); isWait(n, id) {
			out[n] = strings.HasPrefix(n, id+".wait.")
		}
	}
	return out
}

// isWait reports whether name is one of run id's wait marks.
func isWait(name, id string) bool {
	return strings.HasPrefix(name, id+".wait.") || strings.HasPrefix(name, id+".waited.")
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// removeWaits removes run id's wait marks.
func removeWaits(dir, id string) {
	for name := range waits(dir, id) {
		os.Remove(filepath.Join(dir, name))
	}
}
