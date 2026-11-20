package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/mongostore"
)

type boardPin struct {
	ID       string `json:"board_id"`
	Host     string `json:"host"`
	Database string `json:"database"`
}

func readBoardPin(dir string) (boardPin, error) {
	var pin boardPin
	b, err := os.ReadFile(filepath.Join(dir, "runtime-board.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return pin, nil
	}
	if err != nil {
		return pin, err
	}
	if json.Unmarshal(b, &pin) != nil || pin.ID == "" || pin.Host == "" || pin.Database == "" {
		return pin, errors.New("invalid runtime board registration")
	}
	return pin, nil
}

func (c *call) initBoard(ctx context.Context, file string) error {
	if file != "-" {
		info, err := os.Stat(file)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return usagef("store init: use an owner-only URI file or stdin")
		}
	}
	uri, err := c.readURI(file)
	if err != nil {
		return err
	}
	db, err := mongostore.ExplicitDatabase(uri)
	if err != nil {
		return err
	}
	path, _, err := c.storePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	unlock, err := lockBoard(dir)
	if err != nil {
		return err
	}
	defer unlock()
	pin, err := readBoardPin(dir)
	if err != nil {
		return err
	}
	if pin.ID == "" {
		files, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.Name() != boardLock {
				return usagef("store init: choose an empty local board directory")
			}
		}
		if err = pingMongo(ctx, uri); err != nil {
			return redactURI(err, uri)
		}
		pin = boardPin{newSessionID(), uriHost(uri), db}
		data, err := json.Marshal(pin)
		if err != nil {
			return err
		}
		if err = writeWholeMode(filepath.Join(dir, "runtime-board.json"), data, 0600); err != nil {
			return err
		}
	} else if pin.Database != db || pin.Host != uriHost(uri) {
		return usagef("store init: database or host differs from the registered board")
	}
	s, err := mongostore.Open(ctx, uri, mongostore.Options{BoardID: pin.ID, Create: true})
	if err != nil {
		return err
	}
	defer s.Close()
	if err = writeStoreURI(dir, uri); err != nil {
		return err
	}
	return c.out(pin, func(w io.Writer) { fmt.Fprintf(w, "board %s at %s/%s\n", pin.ID, pin.Host, pin.Database) })
}

// runBranch names the branch in this board's namespace. A numeric ticket
// ID can exist on more than one board in the same Git repository.
func runBranch(_ context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	loc, err := c.location()
	if err != nil {
		return err
	}
	branch := "maestro/"
	if loc.boardID != "" {
		branch += loc.boardID + "/"
	}
	if len(c.args) > 0 {
		id, err := c.idArg()
		if err != nil {
			return err
		}
		branch += id.String()
	}
	return c.out(map[string]string{"branch": branch}, func(w io.Writer) { fmt.Fprintln(w, branch) })
}
