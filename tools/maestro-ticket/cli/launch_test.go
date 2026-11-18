//go:build unix

package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// hook is a webhook that counts its rings and keeps the auth each sent,
// and when each came.
type hook struct {
	mu    sync.Mutex
	rings []runs.Notification
	auths []string
	at    []time.Time
	ch    chan struct{}
}

func newHook(t *testing.T) (*hook, string) {
	k := &hook{ch: make(chan struct{}, 100)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n runs.Notification
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &n)
		k.mu.Lock()
		k.rings = append(k.rings, n)
		k.auths = append(k.auths, r.Header.Get("Authorization"))
		k.at = append(k.at, time.Now())
		k.mu.Unlock()
		k.ch <- struct{}{}
	}))
	t.Cleanup(srv.Close)
	return k, srv.URL + "/hook/secret-token"
}

func (k *hook) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.rings)
}

func (k *hook) lastAuth() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.auths[len(k.auths)-1]
}

func (k *hook) wait(t *testing.T, within time.Duration) runs.Notification {
	t.Helper()
	select {
	case <-k.ch:
	case <-time.After(within):
		t.Fatalf("no ring within %s", within)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.rings[len(k.rings)-1]
}

func TestNotifyCommand(t *testing.T) {
	h := newHarness(t)
	h.ok(nil, "doctor")
	k, url := newHook(t)
	h.fails(2, "notify", "Bad_Bot", "--url-file", "-")
	if code, _, _ := h.run("ftp://x", "notify", "planner", "--url-file", "-"); code != 2 {
		t.Fatalf("ftp URL: exit %d", code)
	}
	h.fails(3, "notify", "planner", "--test")
	if code, _, errs := h.run(url+"\n", "notify", "planner", "--url-file", "-"); code != 0 {
		t.Fatal(errs)
	}
	info, err := os.Stat(filepath.Join(h.dir, "notify", "planner.url"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("webhook file: %v %v", info, err)
	}
	code, out, _ := h.run("", "notify", "planner", "--test")
	if code != 0 || strings.Contains(out, "secret-token") || k.wait(t, 5*time.Second).State != "test" {
		t.Fatalf("test ring: %d %s", code, out)
	}
	var list struct{ Bots []string }
	h.ok(&list, "notify")
	if len(list.Bots) != 1 || list.Bots[0] != "planner" {
		t.Fatalf("list: %+v", list)
	}
	h.fails(2, "notify", "qa-lead", "--key-file", "-") // no URL yet
	if code, _, errs := h.run("k-123\n", "notify", "planner", "--key-file", "-"); code != 0 {
		t.Fatal(errs)
	}
	// An empty URL file is refused, and leaves the webhook and its key.
	if code, _, _ := h.run("\n", "notify", "planner", "--url-file", "-"); code != 2 {
		t.Fatalf("empty URL: exit %d", code)
	}
	info, err = os.Stat(filepath.Join(h.dir, "notify", "planner.key"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", info, err)
	}
	code, out, _ = h.run("", "notify", "planner", "--test")
	if code != 0 || strings.Contains(out, "k-123") || k.lastAuth() != "Bearer k-123" {
		t.Fatalf("test ring with key: %d %s %q", code, out, k.lastAuth())
	}
	h.ok(nil, "notify", "planner", "--clear")
	if _, err := os.Stat(filepath.Join(h.dir, "notify", "planner.key")); err == nil {
		t.Fatal("--clear left the key")
	}
	h.ok(&list, "notify")
	if len(list.Bots) != 0 {
		t.Fatalf("after clear: %+v", list)
	}
}
