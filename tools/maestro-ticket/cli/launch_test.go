//go:build unix

package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
