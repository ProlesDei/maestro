package runs

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
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
