package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"
)

// A ring's body is what every bot's webhook reads: exactly these fields,
// with these JSON names.
func TestNotificationFields(t *testing.T) {
	want := []string{"Text text", "Run run", "Ticket ticket,omitempty", "Launcher launcher", "State state"}
	var got []string
	rt := reflect.TypeFor[Notification]()
	for i := range rt.NumField() {
		f := rt.Field(i)
		got = append(got, f.Name+" "+f.Tag.Get("json"))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Notification's fields changed:\n got %q\nwant %q", got, want)
	}
}

// The bytes a ring sends, with a ticket and without.
func TestNoticeGolden(t *testing.T) {
	for _, c := range []struct {
		r    Record
		want string
	}{
		{Record{ID: "R-abcdefgh", Ticket: "T-1", Launcher: "senior-engineer", State: Done, Reply: "never sent", Error: "nor this"},
			`{"text":"Maestro run R-abcdefgh on T-1 ended: done. Run maestro-ticket pickup to read it, then ack it.","run":"R-abcdefgh","ticket":"T-1","launcher":"senior-engineer","state":"done"}`},
		{Record{ID: "R-abcdefgh", Launcher: "planner", State: Died, SessionID: "s1"},
			`{"text":"Maestro run R-abcdefgh ended: died. Run maestro-ticket pickup to read it, then ack it.","run":"R-abcdefgh","launcher":"planner","state":"died"}`},
	} {
		b, err := json.Marshal(Notice(c.r))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != c.want {
			t.Errorf("ring body:\n got %s\nwant %s", b, c.want)
		}
	}
}

// A ring's error sorts into a code, never its text.
func TestRingErrCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/404":
			w.WriteHeader(http.StatusNotFound)
		case "/503":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		case "/304":
			w.WriteHeader(http.StatusNotModified)
		}
	}))
	defer srv.Close()
	ring := func(ctx context.Context, p string) error { return Ring(ctx, Hook{URL: srv.URL + p}, Notification{}) }
	short, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	for _, c := range []struct {
		err  error
		want string
	}{
		{ring(t.Context(), "/ok"), "none"},
		{ring(t.Context(), "/404"), "http4xx"},
		{ring(t.Context(), "/503"), "http5xx"},
		{ring(t.Context(), "/304"), "other"},
		{ring(short, "/slow"), "timeout"},
		{fmt.Errorf("ring %s: %w", "https://x/…", &net.DNSError{Err: "no such host", Name: "x"}), "dns"},
		{errors.New("ring https://x/…: connection refused"), "other"},
	} {
		if got := RingErrCode(c.err); got != c.want {
			t.Errorf("RingErrCode(%v) = %s; want %s", c.err, got, c.want)
		}
	}
	// The text stays as it was.
	if err := ring(t.Context(), "/404"); err.Error() != "ring "+Host(srv.URL)+": 404 Not Found" {
		t.Errorf("ring error text: %q", err)
	}
}
