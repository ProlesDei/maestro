package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

const testURI = "mongodb://maestro:s3cret-pw@db.example.net:27017/board?replicaSet=rs0"

// writeURI puts a store.uri beside h's store.
func writeURI(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, uriFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// noSecret fails if the URI, or its password, shows anywhere.
func noSecret(t *testing.T, what string, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if strings.Contains(s, testURI) || strings.Contains(s, "s3cret-pw") {
			t.Fatalf("%s printed the uri: %s", what, s)
		}
	}
}

func TestStoreKindSQLiteDefault(t *testing.T) {
	h := newHarness(t)
	var doc struct {
		OK    bool
		Store string
	}
	h.ok(&doc, "doctor")
	if want := filepath.Join(h.dir, "tickets.db"); !doc.OK || doc.Store != want {
		t.Fatalf("doctor: %+v; want ok with store %s", doc, want)
	}
	h.ok(nil, "create", "--title", "a")
	c := &call{env: Env{Getenv: func(k string) string { return h.env[k] }, Dir: h.dir}}
	loc, err := c.location()
	if err != nil || loc.kind != kindSQLite || loc.localDir != h.dir || loc.uri != "" {
		t.Fatalf("location: %+v %v", loc, err)
	}
}

func TestStoreKindMongoStub(t *testing.T) {
	h := newHarness(t)
	writeURI(t, h.dir, testURI+"\n")
	for _, args := range [][]string{{"list"}, {"create", "--title", "a"}} {
		code, out, errs := h.run("", append(args, "--json")...)
		if code != 2 || !strings.Contains(out, "mongodb store not built yet") {
			t.Fatalf("%v: exit %d %s %s", args, code, out, errs)
		}
		noSecret(t, args[0], out, errs)
	}
	code, out, errs := h.run("", "doctor")
	if code == 0 || !strings.Contains(out, "mongodb at db.example.net:27017") || !strings.Contains(out, "mongodb store not built yet") {
		t.Fatalf("doctor: exit %d\n%s%s", code, out, errs)
	}
	noSecret(t, "doctor", out, errs)
	// Never a fall back to SQLite: no board made beside it.
	if _, err := os.Stat(filepath.Join(h.dir, "tickets.db")); !os.IsNotExist(err) {
		t.Fatalf("a SQLite store was made: %v", err)
	}
	c := &call{env: Env{Getenv: func(k string) string { return h.env[k] }, Dir: h.dir}}
	loc, err := c.location()
	if err != nil || loc.kind != kindMongoDB || loc.uri != testURI || loc.localDir != h.dir {
		t.Fatalf("location: kind %s dir %s %v", loc.kind, loc.localDir, err)
	}
}

func TestStoreKindRefusesBadURIFile(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"empty":     func(t *testing.T, dir string) { writeURI(t, dir, " \n") },
		"no scheme": func(t *testing.T, dir string) { writeURI(t, dir, "maestro:s3cret-pw@db.example.net\n") },
		"a folder": func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, uriFile), 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"unreadable": func(t *testing.T, dir string) {
			if os.Geteuid() == 0 {
				t.Skip("root reads any file")
			}
			writeURI(t, dir, testURI)
			if err := os.Chmod(filepath.Join(dir, uriFile), 0); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			setup(t, h.dir)
			for _, args := range [][]string{{"list"}, {"doctor"}} {
				code, out, errs := h.run("", append(args, "--json")...)
				if code != 2 || !strings.Contains(out, uriFile) {
					t.Fatalf("%v: exit %d %s %s", args, code, out, errs)
				}
				noSecret(t, args[0], out, errs)
			}
			if _, err := os.Stat(filepath.Join(h.dir, "tickets.db")); !os.IsNotExist(err) {
				t.Fatalf("fell back to SQLite: %v", err)
			}
		})
	}
}

// A MongoDB store has no tickets.db to look for: from the git default,
// the folder beside it still serves runs and the like.
func TestStoreKindMongoGitDefault(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	writeURI(t, filepath.Join(repo, ".git", "maestro"), testURI)
	h := &harness{t: t, dir: repo, env: map[string]string{"MAESTRO_TICKET_ACTOR": "planner"}}
	h.ok(nil, "pickup")
	code, out, errs := h.run("", "list", "--json")
	if code != 2 || strings.Contains(out, "no ticket store") || !strings.Contains(out, "mongodb store not built yet") {
		t.Fatalf("list: exit %d %s %s", code, out, errs)
	}
	noSecret(t, "list", out, errs)
}

// An open's error that quotes the URI prints only its host.
func TestOpenMongoRedactsURI(t *testing.T) {
	old := openMongo
	t.Cleanup(func() { openMongo = old })
	openMongo = func(ctx context.Context, uri string, opts storeOptions) (ticket.Store, error) {
		return nil, fmt.Errorf("%w: dial %s: auth failed for password s3cret-pw", ticket.ErrBusy, uri)
	}
	c := &call{env: Env{Getenv: func(string) string { return "" }}}
	s, err := c.openStore(context.Background(), storeLoc{kind: kindMongoDB, uri: testURI})
	if s != nil || err == nil || !errors.Is(err, ticket.ErrBusy) || !strings.Contains(err.Error(), "dial db.example.net:27017") {
		t.Fatalf("openStore: %v %v", s, err)
	}
	noSecret(t, "openStore", err.Error())
	if got := uriHost("mongodb://u:p@h1:1,h2:2/x"); got != "h1:1,h2:2" {
		t.Fatalf("uriHost: %q", got)
	}
	if got := uriHost("mongodb://u:p%zz@h/x"); strings.Contains(got, "p%zz") {
		t.Fatalf("uriHost printed a bad uri: %q", got)
	}
}

func TestHostID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "maestro")
	var wg sync.WaitGroup
	ids := make([]string, 8)
	errs := make([]error, 8)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = HostID(dir)
		}()
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] || !hostIDPattern.MatchString(ids[i]) {
			t.Fatalf("HostID %d: %q %v; first %q", i, ids[i], errs[i], ids[0])
		}
	}
	fi, err := os.Stat(filepath.Join(dir, hostIDFile))
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("host-id: %v %v", fi, err)
	}
	if again, err := HostID(dir); err != nil || again != ids[0] {
		t.Fatalf("HostID again: %q %v", again, err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("left behind: %v", ents)
	}
	os.WriteFile(filepath.Join(dir, hostIDFile), nil, 0o644)
	if _, err := HostID(dir); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("empty host-id: %v", err)
	}
}
