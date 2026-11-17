package cli

import (
	"os"
	"path/filepath"
	"time"
)

// The marks and flags that stop two sessions doing one job: the handouts
// a look printed, a run's .active and .resumed marks, and the pause. They
// sit in a coordStore, which today keeps them as files next to the ticket
// store, shared by every worktree of the checkout. A store that shares
// them across machines keeps the same names, one record each.

// The spaces marks live in.
const (
	handoutMarks = "handouts" // a look's or pickup's sends, under handouts/
	runMarks     = "runs"     // a run's marks, beside its record under runs/
)

// pauseFlag is the pause pickup saw, next to the store.
const pauseFlag = "paused.json"

// A mark as a coordStore read it. ver tells this copy from one written
// over it since: a file's os.FileInfo, or a comparable value such as a
// version number. It is nil when no mark was read.
type mark struct {
	name string
	body string
	at   time.Time // when it was last written
	ver  any
}

// coordStore keeps marks and flags. Every mark holds its holder's text in
// its body, as the files always have.
type coordStore interface {
	// create makes a mark holding body, or fails with fs.ErrExist if one
	// stands: of creates at once, one wins.
	create(space, name, body string) error
	// read reads a mark's body and when it was last written.
	read(space, name string) (mark, error)
	// write sets a mark's body whole, made or not: a reader never sees
	// half of it.
	write(space, name, body string) error
	remove(space, name string) error
	// list names each mark in a space and when it was last written; it
	// reads no bodies.
	list(space string) ([]mark, error)
	// takeOver moves a mark aside under a takeover lock, if still says the
	// mark as it now stands is the one its taker found stale, so of takers
	// that all found it stale one wins. took is false while another taker
	// holds the lock; cleared says that lock was a dead taker's and is gone.
	takeOver(space, name string, still func(now mark) bool) (took, cleared bool)
	// flag reads a named flag, failing with fs.ErrNotExist if none is set.
	flag(name string) ([]byte, error)
	// setFlag sets a flag whole, so a reader never sees half of one.
	setFlag(name string, b []byte) error
	clearFlag(name string) error
}

// coord does the takes and checks on a coordStore that every command
// shares.
type coord struct{ s coordStore }

// coord is the coordStore this call's store keeps its marks in: for now
// always the files beside it.
func (c *call) coord() (coord, error) {
	path, _, err := c.storePath()
	if err != nil {
		return coord{}, err
	}
	dir := filepath.Dir(path)
	return coord{fileCoord{dir: dir, handouts: filepath.Join(dir, "handouts"), runs: filepath.Join(dir, "runs")}}, nil
}

// sameMark reports whether b is the very copy a was read from, not one
// written over it since.
func sameMark(a, b mark) bool {
	if fa, ok := a.ver.(os.FileInfo); ok {
		fb, ok := b.ver.(os.FileInfo)
		return ok && os.SameFile(fa, fb) && fa.ModTime().Equal(fb.ModTime())
	}
	return a.ver != nil && a.ver == b.ver
}

// fileCoord keeps each mark as a file in its space's folder and each flag
// as a file in dir, the store's folder.
type fileCoord struct{ dir, handouts, runs string }

func (f fileCoord) folder(space string) string {
	if space == runMarks {
		return f.runs
	}
	return f.handouts
}

func (f fileCoord) path(space, name string) string {
	return filepath.Join(f.folder(space), name)
}

// create makes the file with O_EXCL.
func (f fileCoord) create(space, name, body string) error {
	os.MkdirAll(f.folder(space), 0o755)
	fh, err := os.OpenFile(f.path(space, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if body != "" {
		fh.WriteString(body)
	}
	fh.Close()
	return nil
}

func (f fileCoord) read(space, name string) (mark, error) {
	p := f.path(space, name)
	b, err := os.ReadFile(p)
	if err != nil {
		return mark{}, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return mark{}, err
	}
	return mark{name: name, body: string(b), at: fi.ModTime(), ver: fi}, nil
}

// write writes a temp file and renames it over the mark.
func (f fileCoord) write(space, name, body string) error {
	os.MkdirAll(f.folder(space), 0o755)
	p := f.path(space, name)
	tmp := p + ".tmp" + newSessionID()[:8]
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (f fileCoord) remove(space, name string) error {
	return os.Remove(f.path(space, name))
}

func (f fileCoord) list(space string) ([]mark, error) {
	es, err := os.ReadDir(f.folder(space))
	if err != nil {
		return nil, err
	}
	var out []mark
	for _, e := range es {
		if fi, err := e.Info(); err == nil {
			out = append(out, mark{name: e.Name(), at: fi.ModTime(), ver: fi})
		}
	}
	return out, nil
}

// takeOver takes the file <mark>.takeover with O_EXCL and renames the mark
// to a .stale name. A lock older than activeGrace was left by a taker
// that died, and is removed.
func (f fileCoord) takeOver(space, name string, still func(now mark) bool) (took, cleared bool) {
	p := f.path(space, name)
	lock := p + ".takeover"
	lf, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if fi, err := os.Stat(lock); err == nil && time.Since(fi.ModTime()) > activeGrace {
			os.Remove(lock) // its taker died; the next try takes it
			return false, true
		}
		return false, false
	}
	lf.Close()
	defer os.Remove(lock)
	if m, err := f.read(space, name); err == nil && still(m) {
		os.Rename(p, p+".stale"+newSessionID()[:8])
	}
	return true, false
}

func (f fileCoord) flag(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(f.dir, name))
}

// setFlag writes a temp file and renames it over the flag.
func (f fileCoord) setFlag(name string, b []byte) error {
	p := filepath.Join(f.dir, name)
	tmp := p + ".tmp" + newSessionID()[:8]
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (f fileCoord) clearFlag(name string) error {
	return os.Remove(filepath.Join(f.dir, name))
}
