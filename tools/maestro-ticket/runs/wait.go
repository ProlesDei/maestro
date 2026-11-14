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
// time limit, up to maxSlotCredit: a run that spends its limit in a queue
// runs nothing and gives no result.
//
// Each wait is one empty file, <id>.wait.<start in unix nanoseconds>-<pid>,
// whose mtime the waiter moves to now while it waits. Once it has a slot or
// gives up, it renames the file to <id>.waited.<...>, so the mtime is the
// end. A waiter killed mid-wait stops moving the mtime, so its wait counts
// up to its last touch.

// maxSlotCredit is the most slot waiting a run's time limit leaves out, so
// a machine whose slots never free can't keep a run alive for good. A var
// only so tests can shorten it.
var maxSlotCredit = time.Hour

// liveWait: a waiter that touched its file this recently still waits.
const liveWait = 5 * time.Second

// Wait is one slot wait a launched command marked.
type Wait struct{ file string }

// StartWait marks a slot wait for run id in dir. With no such run in dir,
// it marks nothing and returns nil: the waiter wasn't launched from this
// store, so no supervisor reads the mark.
func StartWait(dir, id string, now time.Time) (*Wait, error) {
	if !ValidID(id) {
		return nil, nil
	}
	if _, err := os.Stat(path(dir, id, ".json")); err != nil {
		return nil, nil
	}
	name := path(dir, id, fmt.Sprintf(".wait.%d-%d", now.UnixNano(), os.Getpid()))
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	f.Close()
	return &Wait{name}, nil
}

// Touch says the wait goes on. Errors are ignored: the run may have ended
// and its supervisor removed the mark.
func (w *Wait) Touch(now time.Time) {
	if w != nil {
		os.Chtimes(w.file, now, now)
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
	type span struct{ from, to time.Time }
	var spans []span
	for name, live := range waits(dir, id) {
		nanos, _, ok := strings.Cut(name[strings.LastIndex(name, ".")+1:], "-")
		n, err := strconv.ParseInt(nanos, 10, 64)
		info, serr := os.Stat(filepath.Join(dir, name))
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
		switch n := e.Name(); {
		case strings.HasPrefix(n, id+".wait."):
			out[n] = true
		case strings.HasPrefix(n, id+".waited."):
			out[n] = false
		}
	}
	return out
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
