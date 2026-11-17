package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/sqlitestore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// The marks and flags that stop two sessions doing one job: the handouts
// a look printed, a run's .active and .resumed marks, and the pause. They
// sit in a coordStore: beside a SQLite store, files next to it, shared by
// every worktree of the checkout; on a MongoDB store, one record each in
// the database, under the same names, shared by every machine on it.

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
	// stopped reports the halt: while it stands no run starts and no
	// claim is made. A shared store must still honour this machine's own
	// STOP file.
	stopped() bool
}

// coord does the takes and checks on a coordStore that every command
// shares. host is this machine's id when the store is shared across
// machines, and "" when it is not.
type coord struct {
	s    coordStore
	host string
}

// coord is the coordStore this call's store keeps its marks in: the files
// beside a SQLite store, or the records of a MongoDB one, which every
// machine on it shares.
func (c *call) coord() (coord, error) {
	loc, err := c.location()
	if err != nil {
		return coord{}, err
	}
	dir := loc.localDir
	files := fileCoord{dir: dir, handouts: filepath.Join(dir, "handouts"), runs: filepath.Join(dir, "runs")}
	if loc.kind != kindMongoDB {
		return coord{s: files}, nil
	}
	// With no id, this machine's marks would read as no machine's, and
	// another machine would judge them by runs it can't see.
	host, err := c.hostID()
	if err != nil {
		return coord{}, err
	}
	return coord{s: sharedCoord{local: files, board: c.board}, host: host}, nil
}

// hostID is this machine's id when the store is shared, else "": a store
// on one machine needs none, and gets no host-id file.
func (c *call) hostID() (string, error) {
	sh := c.sharedOnce()
	sh.hostOnce.Do(func() {
		loc, err := c.location()
		switch {
		case err != nil:
			sh.hostErr = err
		case loc.kind == kindMongoDB:
			sh.host, sh.hostErr = HostID(loc.localDir)
		}
	})
	return sh.host, sh.hostErr
}

// callShared is what a call works out once about a shared store, safe for
// the goroutines that end runs at once.
type callShared struct {
	hostOnce  sync.Once
	host      string
	hostErr   error
	boardOnce sync.Once
	board     *mongostore.Store
	boardErr  error
}

func (c *call) sharedOnce() *callShared {
	if c.shared == nil {
		c.shared = &callShared{} // a call a test made by hand
	}
	return c.shared
}

// board is the call's MongoDB store for its marks and the halt: opened at
// first use, shared by the call's marks, and closed as the call ends.
func (c *call) board() (*mongostore.Store, error) {
	sh := c.sharedOnce()
	sh.boardOnce.Do(func() {
		loc, err := c.location()
		if err != nil {
			sh.boardErr = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), markTimeout)
		defer cancel()
		s, err := c.openStore(ctx, loc)
		if err != nil {
			sh.boardErr = err
			return
		}
		ms, ok := s.(*mongostore.Store)
		if !ok {
			s.Close()
			sh.boardErr = fmt.Errorf("%w: this store keeps no shared marks", ticket.ErrInvalid)
			return
		}
		sh.board = ms
	})
	return sh.board, sh.boardErr
}

// closeBoard closes the store board opened, if it did.
func (c *call) closeBoard() {
	if c.shared != nil && c.shared.board != nil {
		c.shared.board.Close()
	}
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

// stopped reports the STOP file in dir, set by hand or by a bot.
func (f fileCoord) stopped() bool { return fileExists(filepath.Join(f.dir, sqlitestore.StopFile)) }

// markTimeout bounds each call to a shared store's marks.
const markTimeout = 15 * time.Second

// flagMarks is the space a shared store keeps its flags in.
const flagMarks = "flags"

// sharedCoord keeps the marks and flags in a MongoDB store, where every
// machine on it sees them, and keeps on this machine what only this
// machine reads: a run's .resumed mark, since only its launcher resumes
// it, and the STOP file. The store's clock dates each mark: read back,
// a mark's time is this machine's now less the age the server gave it,
// so callers that compare it with time.Now agree across machines whose
// clocks differ.
type sharedCoord struct {
	local fileCoord
	board func() (*mongostore.Store, error)
}

// here reports whether a mark stays on this machine.
func (s sharedCoord) here(space, name string) bool {
	return space == runMarks && !strings.HasSuffix(name, ".active")
}

// with runs fn on the store, bounded by markTimeout.
func (s sharedCoord) with(fn func(ctx context.Context, b *mongostore.Store) error) error {
	b, err := s.board()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), markTimeout)
	defer cancel()
	return fn(ctx, b)
}

func fromShared(m mongostore.Mark) mark {
	return mark{name: m.Name, body: m.Body, at: time.Now().Add(-m.Age), ver: m.Ver}
}

func (s sharedCoord) create(space, name, body string) error {
	if s.here(space, name) {
		return s.local.create(space, name, body)
	}
	return s.with(func(ctx context.Context, b *mongostore.Store) error { return b.CreateMark(ctx, space, name, body) })
}

func (s sharedCoord) read(space, name string) (mark, error) {
	if s.here(space, name) {
		return s.local.read(space, name)
	}
	var m mark
	err := s.with(func(ctx context.Context, b *mongostore.Store) error {
		got, err := b.ReadMark(ctx, space, name)
		m = fromShared(got)
		return err
	})
	if err != nil {
		return mark{}, err
	}
	return m, nil
}

func (s sharedCoord) write(space, name, body string) error {
	if s.here(space, name) {
		return s.local.write(space, name, body)
	}
	return s.with(func(ctx context.Context, b *mongostore.Store) error { return b.WriteMark(ctx, space, name, body) })
}

func (s sharedCoord) remove(space, name string) error {
	if s.here(space, name) {
		return s.local.remove(space, name)
	}
	return s.with(func(ctx context.Context, b *mongostore.Store) error { return b.RemoveMark(ctx, space, name) })
}

// list lists the shared marks of a space. Only handouts are listed, and
// they are all shared.
func (s sharedCoord) list(space string) ([]mark, error) {
	var out []mark
	err := s.with(func(ctx context.Context, b *mongostore.Store) error {
		ms, err := b.ListMarks(ctx, space)
		for _, m := range ms {
			out = append(out, fromShared(m))
		}
		return err
	})
	return out, err
}

// takeOver needs no lock: the store removes the mark only while it is
// still the copy read, so of takers that all found it stale one removes
// it, and the next create decides. A mark already gone is took, so its
// taker tries the create again.
func (s sharedCoord) takeOver(space, name string, still func(now mark) bool) (took, cleared bool) {
	if s.here(space, name) {
		return s.local.takeOver(space, name, still)
	}
	m, err := s.read(space, name)
	if errors.Is(err, fs.ErrNotExist) {
		return true, false
	}
	if err != nil {
		return false, false
	}
	if still(m) {
		err = s.with(func(ctx context.Context, b *mongostore.Store) error {
			_, err := b.RemoveMarkIf(ctx, space, name, m.ver.(string))
			return err
		})
		if err != nil {
			return false, false
		}
	}
	return true, false
}

func (s sharedCoord) flag(name string) ([]byte, error) {
	m, err := s.read(flagMarks, name)
	if err != nil {
		return nil, err
	}
	return []byte(m.body), nil
}

func (s sharedCoord) setFlag(name string, b []byte) error { return s.write(flagMarks, name, string(b)) }

func (s sharedCoord) clearFlag(name string) error { return s.remove(flagMarks, name) }

// stopped reports the halt stop --all set on the board, or this
// machine's STOP file. A board it can't read counts as halted: no claim
// could land on it either. It copies a halt it read into the STOP file
// first, for the skills and bots that look for the file, as
// mirrorHalt says.
func (s sharedCoord) stopped() bool {
	halted := true
	err := s.with(func(ctx context.Context, b *mongostore.Store) error {
		var err error
		halted, err = b.Halted(ctx)
		return err
	})
	if err != nil {
		return true
	}
	mirrorHalt(s.local.dir, halted)
	return halted || s.local.stopped()
}

// haltBody is what a STOP file stop --all made holds: the mark that tells
// it from one a person or a bot made, which the CLI never removes.
const haltBody = "made by maestro-ticket stop --all; maestro-ticket stop --all --off removes it\n"

// haltOut is what stop --all prints.
type haltOut struct {
	Halted  bool   `json:"halted"`
	Shared  bool   `json:"shared"` // on every machine on the store
	Changed bool   `json:"changed"`
	Stop    string `json:"stop_file"`
	// Kept is a STOP file the lift left: made by hand or by a bot, it
	// still stops this machine.
	Kept bool `json:"kept,omitempty"`
}

// halt sets or lifts the halt for every machine on the store: on a shared
// store, the board's halt, which every machine copies to its STOP file,
// and this one at once; on SQLite, where one machine works, the STOP file
// alone. Lifting removes only a STOP file stop --all made.
func (c *call) halt(ctx context.Context, on bool) error {
	loc, err := c.location()
	if err != nil {
		return err
	}
	out := haltOut{Halted: on, Shared: loc.kind == kindMongoDB, Stop: filepath.Join(loc.localDir, sqlitestore.StopFile)}
	if out.Shared {
		b, err := c.board()
		if err != nil {
			return err
		}
		if out.Changed, err = b.SetHalt(ctx, on); err != nil {
			return err
		}
	}
	if on {
		if err := os.MkdirAll(loc.localDir, 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(out.Stop, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		switch {
		case err == nil:
			_, err = f.WriteString(haltBody)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			out.Changed = true
		case !errors.Is(err, fs.ErrExist):
			return err
		}
	} else {
		b, err := os.ReadFile(out.Stop)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return err
		case string(b) == haltBody:
			if err := os.Remove(out.Stop); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			out.Changed = true
		default:
			out.Kept = true
		}
	}
	return c.out(out, func(w io.Writer) {
		where := "this machine"
		if out.Shared {
			where = "every machine on the board"
		}
		switch {
		case on:
			fmt.Fprintf(w, "halted %s: no claim lands and no run starts until maestro-ticket stop --all --off\n", where)
		case out.Kept:
			fmt.Fprintf(w, "halt lifted on %s; %s stays, made by hand or by a bot: this machine stays stopped until it is removed\n", where, out.Stop)
		default:
			fmt.Fprintf(w, "halt lifted on %s\n", where)
		}
	})
}

// mirrorHalt makes dir's STOP file while the board is halted, and removes
// it once the halt lifts, if the CLI made it.
func mirrorHalt(dir string, halted bool) {
	p := filepath.Join(dir, sqlitestore.StopFile)
	if halted {
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			f.WriteString(haltBody)
			f.Close()
		}
		return
	}
	if b, err := os.ReadFile(p); err == nil && string(b) == haltBody {
		os.Remove(p)
	}
}
