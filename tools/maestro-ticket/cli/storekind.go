package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/sqlitestore"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// The two kinds of ticket store.
const (
	kindSQLite  = "sqlite"
	kindMongoDB = "mongodb"
)

// uriFile is the secret that makes the store MongoDB: a file in the local
// maestro folder holding the server's URI. With none, the store is SQLite.
const uriFile = "store.uri"

// storeLoc is where a call's store lives. localDir holds this machine's
// own state (runs/, slots/, notify/ ...) for either kind; sqlitePath is
// the SQLite file in it, the store itself when kind is sqlite; source is
// where storePath found it. uri is set only for mongodb and never printed:
// it holds the password.
type storeLoc struct {
	kind       string
	localDir   string
	sqlitePath string
	source     string
	boardID    string
	uri        string
}

// storeOptions are what a store's open takes from the call.
type storeOptions struct {
	BoardID  string
	Now      func() time.Time // clock for the store; nil means the database's
	StopFile string           // the kill switch's path, in the local folder
	Host     string           // this machine's id, for a shared store
}

// openMongo opens a MongoDB store. Its error must not hold the URI.
// Tests swap it for a fake.
var openMongo = func(ctx context.Context, uri string, opts storeOptions) (ticket.Store, error) {
	s, err := mongostore.Open(ctx, uri, mongostore.Options{BoardID: opts.BoardID, Now: opts.Now, StopFile: opts.StopFile, Host: opts.Host})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// location resolves the store, once a call: storePath's folder, then
// store.uri in it. A store.uri that is there but can't be read, or is
// empty, refuses: falling back to SQLite would make a second board.
func (c *call) location() (storeLoc, error) {
	if !c.loc.done {
		c.loc.v, c.loc.err = c.resolveLoc()
		c.loc.done = true
	}
	return c.loc.v, c.loc.err
}

func (c *call) resolveLoc() (storeLoc, error) {
	path, source, err := c.storePath()
	if err != nil {
		return storeLoc{}, err
	}
	loc := storeLoc{kind: kindSQLite, localDir: filepath.Dir(path), sqlitePath: path, source: source}
	pin, err := readBoardPin(loc.localDir)
	if err != nil {
		return storeLoc{}, err
	}
	name := filepath.Join(loc.localDir, uriFile)
	b, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		if pin.ID != "" {
			return storeLoc{}, errors.New("board initialization incomplete: repeat store init with the original URI file")
		}
		return loc, nil
	}
	if err != nil {
		return storeLoc{}, fmt.Errorf("%w: can't read %s, so the store is unknown: %v", ticket.ErrInvalid, name, err)
	}
	uri := strings.TrimSpace(string(b))
	if uri == "" {
		return storeLoc{}, fmt.Errorf("%w: %s is empty: put the MongoDB URI in it, or remove it to use SQLite at %s", ticket.ErrInvalid, name, path)
	}
	if !strings.HasPrefix(uri, "mongodb://") && !strings.HasPrefix(uri, "mongodb+srv://") {
		return storeLoc{}, fmt.Errorf("%w: %s holds no mongodb:// or mongodb+srv:// URI", ticket.ErrInvalid, name)
	}
	if pin.ID != "" {
		db, err := mongostore.ExplicitDatabase(uri)
		if err != nil || db != pin.Database || uriHost(uri) != pin.Host {
			return storeLoc{}, errors.New("configured URI differs from the registered board target")
		}
		loc.boardID = pin.ID
	}
	loc.kind, loc.uri = kindMongoDB, uri
	return loc, nil
}

// missing says a SQLite store from the git default isn't there, which open
// and besideStore refuse. A MongoDB store has no file to look for.
func (loc storeLoc) missing() bool {
	if loc.kind != kindSQLite || loc.source != "git common dir" {
		return false
	}
	_, err := os.Stat(loc.sqlitePath)
	return errors.Is(err, fs.ErrNotExist)
}

// openStore opens loc's store, with no checks of its own. A failed open
// returns a nil Store, never a nil pointer in one, so callers can test
// s != nil.
func (c *call) openStore(ctx context.Context, loc storeLoc) (ticket.Store, error) {
	opts := storeOptions{BoardID: loc.boardID, Now: c.env.Now, StopFile: filepath.Join(loc.localDir, sqlitestore.StopFile)}
	if loc.kind == kindMongoDB {
		// Only for doctor's note of which build each machine runs: with
		// no id, the store just records none.
		if loc.localDir != "" {
			opts.Host, _ = HostID(loc.localDir)
		}
		s, err := openMongo(ctx, loc.uri, opts)
		if err != nil {
			return nil, redactURI(err, loc.uri)
		}
		return s, nil
	}
	s, err := sqlitestore.Open(ctx, loc.sqlitePath, sqlitestore.Options{Now: opts.Now})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// uriHost is the part of a MongoDB URI safe to print: its hosts, with no
// user or password.
func uriHost(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" {
		// url's error quotes the URI whole.
		return "an unreadable URI"
	}
	return u.Host
}

// redactURI takes uri, and its password alone, out of err's text, keeping
// what err wraps for errors.Is.
func redactURI(err error, uri string) error {
	msg := err.Error()
	out := strings.ReplaceAll(msg, uri, uriHost(uri))
	if u, perr := url.Parse(uri); perr == nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			out = strings.ReplaceAll(out, pw, "***")
		}
	}
	if out == msg {
		return err
	}
	return redacted{msg: out, err: err}
}

type redacted struct {
	msg string
	err error
}

func (e redacted) Error() string { return e.msg }
func (e redacted) Unwrap() error { return e.err }

// hostIDFile names this machine for a shared store. Hostnames change;
// this doesn't.
const hostIDFile = "host-id"

var hostIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// HostID returns this machine's id, kept in localDir/host-id: made once,
// at random, and read from then on. Of makers at once one wins and the
// rest read its id.
func HostID(localDir string) (string, error) {
	name := filepath.Join(localDir, hostIDFile)
	if id, err := readHostID(name); !errors.Is(err, fs.ErrNotExist) {
		return id, err
	}
	b := make([]byte, 16)
	rand.Read(b)
	id := hex.EncodeToString(b)
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return "", err
	}
	// Written whole, then linked in: the link fails if the name is taken,
	// so no reader sees a file half written.
	tmp, err := os.CreateTemp(localDir, ".tmp-host-id-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(id + "\n"); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", err
	}
	err = os.Link(tmp.Name(), name)
	if errors.Is(err, fs.ErrExist) {
		return readHostID(name)
	}
	if err != nil {
		// A filesystem with no hard links: make the name with O_EXCL.
		f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(err, fs.ErrExist) {
			return readHostID(name)
		}
		if err != nil {
			return "", err
		}
		if _, err := f.WriteString(id + "\n"); err != nil {
			f.Close()
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
	}
	return id, nil
}

func readHostID(name string) (string, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if !hostIDPattern.MatchString(id) {
		return "", fmt.Errorf("%w: %s holds no host id; remove it and a new one is made", ticket.ErrInvalid, name)
	}
	return id, nil
}
