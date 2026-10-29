package ticket

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestParseID(t *testing.T) {
	for in, want := range map[string]ID{"T-12": 12, "t-3": 3, "7": 7, " T-1 ": 1} {
		got, err := ParseID(in)
		if err != nil || got != want {
			t.Errorf("ParseID(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "T-", "T-0", "-1", "X-1", "T-abc"} {
		if _, err := ParseID(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseID(%q) err = %v; want ErrInvalid", in, err)
		}
	}
}

func TestIDJSONRoundTrip(t *testing.T) {
	b, _ := json.Marshal(ID(42))
	if string(b) != `"T-42"` {
		t.Fatalf("marshal = %s", b)
	}
	var id ID
	for _, in := range []string{`"T-42"`, `"42"`, `42`} {
		if err := json.Unmarshal([]byte(in), &id); err != nil || id != 42 {
			t.Errorf("unmarshal %s = %v, %v", in, id, err)
		}
	}
}

func TestTransitions(t *testing.T) {
	allowed := [][2]Status{
		{StatusTodo, StatusInProgress}, {StatusInProgress, StatusInReview}, {StatusInReview, StatusInQA},
		{StatusInQA, StatusReadyToRelease}, {StatusReadyToRelease, StatusDone},
		{StatusInReview, StatusInProgress}, {StatusInQA, StatusInProgress}, {StatusBlocked, StatusTodo},
		{StatusInProgress, StatusTodo}, {StatusTodo, StatusCancelled}, {StatusInProgress, StatusDone},
	}
	for _, p := range allowed {
		if !CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s should be allowed", p[0], p[1])
		}
	}
	denied := [][2]Status{
		{StatusTodo, StatusDone}, {StatusInReview, StatusDone}, {StatusInQA, StatusDone}, {StatusTodo, StatusInReview},
		{StatusDone, StatusTodo}, {StatusCancelled, StatusTodo}, {StatusTodo, StatusTodo},
		{StatusBlocked, StatusDone},
	}
	for _, p := range denied {
		if CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s should be denied", p[0], p[1])
		}
	}
	// Every active status can be blocked and cancelled; terminals are terminal.
	for _, s := range Statuses {
		if s.Terminal() {
			if len(NextStatuses(s)) != 0 {
				t.Errorf("%s is terminal but has exits", s)
			}
			continue
		}
		if s != StatusBlocked && !CanTransition(s, StatusBlocked) {
			t.Errorf("%s cannot be blocked", s)
		}
		if !CanTransition(s, StatusCancelled) {
			t.Errorf("%s cannot be cancelled", s)
		}
	}
	if !StatusDone.Terminal() || !StatusCancelled.Terminal() || StatusBlocked.Terminal() {
		t.Error("terminal set wrong")
	}
}

func TestValidateHolder(t *testing.T) {
	for _, h := range []string{"jse/s1", "senior-software-engineer/0b6f-uuid", "bot.1/sess_2:step"} {
		if err := ValidateHolder(h); err != nil {
			t.Errorf("ValidateHolder(%q) = %v", h, err)
		}
	}
	for _, h := range []string{"", "jse", "/s1", "jse/", "a/b/c", "j se/s1"} {
		if err := ValidateHolder(h); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidateHolder(%q) = %v; want ErrInvalid", h, err)
		}
	}
	if HolderBot("qa-lead/abc") != "qa-lead" {
		t.Error("HolderBot")
	}
}

func TestValidateTTL(t *testing.T) {
	if d, err := ValidateTTL(0); err != nil || d != DefaultTTL {
		t.Errorf("default ttl = %v, %v", d, err)
	}
	for _, d := range []time.Duration{time.Second, -time.Minute, 25 * time.Hour} {
		if _, err := ValidateTTL(d); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidateTTL(%v) = %v", d, err)
		}
	}
}

func TestCreateValidate(t *testing.T) {
	r := CreateRequest{Title: "  x  "}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if r.Title != "x" || *r.Priority != DefaultPriority || r.Status != StatusTodo || r.Actor != "unknown" {
		t.Errorf("defaults not applied: %+v", r)
	}
	bad := []CreateRequest{
		{Title: ""}, {Title: "a\nb"}, {Title: "x", Role: "Bad Role"},
		{Title: "x", Status: StatusDone}, {Title: "x", Priority: ptr(9)},
	}
	for _, r := range bad {
		if err := r.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("Validate(%+v) = %v", r, err)
		}
	}
}

func TestClaimValidate(t *testing.T) {
	r := ClaimRequest{Holder: "qa-lead/s1"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(r.Statuses) != 1 || r.Statuses[0] != StatusTodo || r.TTL != DefaultTTL || r.Actor != "qa-lead" {
		t.Errorf("defaults not applied: %+v", r)
	}
	r = ClaimRequest{Holder: "qa-lead/s1", Statuses: []Status{StatusDone}}
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("claiming done should be invalid, got %v", err)
	}
}

func TestClassify(t *testing.T) {
	cases := map[error]int{
		nil: ExitOK, ErrNoWork: ExitNoWork, ErrNotFound: ExitNotFound,
		fmt.Errorf("%w: x", ErrVersionConflict): ExitConflict, ErrClaimed: ExitConflict,
		ErrInvalidTransition: ExitUsage, errors.New("boom"): ExitInternal,
	}
	for err, want := range cases {
		if _, got := Classify(err); got != want {
			t.Errorf("Classify(%v) = %d; want %d", err, got, want)
		}
	}
}

func ptr(i int) *int { return &i }
