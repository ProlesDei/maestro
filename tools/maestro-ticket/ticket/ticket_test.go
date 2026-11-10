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
	ship := func(s Status) Ticket { return Ticket{Type: TypeShip, Status: s} }
	scout := func(s Status) Ticket { return Ticket{Type: TypeScout, Status: s} }
	blocked := func(typ Type, from Status) Ticket { return Ticket{Type: typ, Status: StatusBlocked, BlockedFrom: from} }
	allowed := []struct {
		t  Ticket
		to Status
	}{
		{ship(StatusTodo), StatusInProgress}, {ship(StatusInProgress), StatusInReview}, {ship(StatusInReview), StatusDone},
		{ship(StatusInReview), StatusInProgress}, {blocked(TypeShip, StatusTodo), StatusTodo},
		{ship(StatusInProgress), StatusTodo}, {ship(StatusTodo), StatusCancelled},
		{ship(StatusBacklog), StatusTodo}, {ship(StatusTodo), StatusBacklog},
		{blocked(TypeShip, StatusInReview), StatusInReview}, {blocked(TypeShip, StatusInReview), StatusCancelled},
		{scout(StatusInProgress), StatusInReview}, {scout(StatusInReview), StatusDone}, {scout(StatusInReview), StatusInProgress},
		{blocked(TypeScout, StatusInReview), StatusInReview},
		{blocked(TypeShip, ""), StatusTodo}, // a blocked ticket with no origin returns to todo
	}
	for _, p := range allowed {
		if !p.t.CanMove(p.to) {
			t.Errorf("%s %s (from %q) -> %s should be allowed", p.t.Type, p.t.Status, p.t.BlockedFrom, p.to)
		}
	}
	denied := []struct {
		t  Ticket
		to Status
	}{
		{ship(StatusTodo), StatusDone}, {ship(StatusTodo), StatusInReview}, {ship(StatusInProgress), StatusInProgress},
		{ship(StatusDone), StatusTodo}, {ship(StatusCancelled), StatusTodo}, {ship(StatusTodo), StatusTodo},
		{ship(StatusInProgress), StatusDone}, // ship tickets reach done only through review
		{ship(StatusBacklog), StatusInProgress},
		{blocked(TypeShip, StatusTodo), StatusDone},
		// Blocked returns only to where it came from.
		{blocked(TypeShip, StatusInProgress), StatusInReview}, {blocked(TypeShip, StatusInReview), StatusDone},
		{blocked(TypeShip, StatusInProgress), StatusTodo}, {blocked(TypeShip, StatusInReview), StatusInProgress},
		{scout(StatusInProgress), StatusDone}, {blocked(TypeScout, StatusInReview), StatusDone},
		// Unknown statuses, such as removed ones, have no moves.
		{ship("released"), StatusDone}, {ship("released"), StatusCancelled},
		{Ticket{Status: StatusTodo}, StatusInProgress}, // unknown type has no moves
	}
	for _, p := range denied {
		if p.t.CanMove(p.to) {
			t.Errorf("%s %s (from %q) -> %s should be denied", p.t.Type, p.t.Status, p.t.BlockedFrom, p.to)
		}
	}
	// Every active status can be blocked and cancelled, blocked returns to
	// its origin, and terminals are terminal.
	for _, typ := range []Type{TypeShip, TypeScout} {
		for _, s := range Statuses {
			tk := Ticket{Type: typ, Status: s}
			if s.Terminal() {
				if len(tk.NextStatuses()) != 0 {
					t.Errorf("%s %s is terminal but has exits", typ, s)
				}
				continue
			}
			if s == StatusBlocked {
				continue
			}
			if !tk.CanMove(StatusBlocked) {
				t.Errorf("%s %s cannot be blocked", typ, s)
			}
			if !tk.CanMove(StatusCancelled) {
				t.Errorf("%s %s cannot be cancelled", typ, s)
			}
			if b := blocked(typ, s); len(b.NextStatuses()) != 2 || !b.CanMove(s) {
				t.Errorf("%s blocked from %s: next %v", typ, s, b.NextStatuses())
			}
		}
	}
	if !StatusDone.Terminal() || !StatusCancelled.Terminal() || StatusBlocked.Terminal() || StatusBacklog.Terminal() {
		t.Error("terminal set wrong")
	}
	if !IsSendBack(StatusInReview, StatusInProgress) || IsSendBack(StatusTodo, StatusInProgress) ||
		IsSendBack(StatusBlocked, StatusInProgress) {
		t.Error("send-back set wrong")
	}
}

func TestValidateRole(t *testing.T) {
	for _, r := range []string{"", "chief-of-staff", "planner", "engineer", "qa-lead", "reviewer"} {
		if err := ValidateRole(r); err != nil {
			t.Errorf("ValidateRole(%q) = %v", r, err)
		}
	}
	for _, r := range []string{"cos", "code-reviewer", "senior-software-engineer", "Engineer", "engineer "} {
		if err := ValidateRole(r); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidateRole(%q) = %v; want ErrInvalid", r, err)
		}
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
	if HolderBot("reviewer/abc") != "reviewer" {
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
	if r.Title != "x" || *r.Priority != DefaultPriority || r.Status != StatusTodo || r.Actor != "unknown" ||
		r.Type != TypeShip || r.Owner != "unknown" {
		t.Errorf("defaults not applied: %+v", r)
	}
	r = CreateRequest{Title: "x", Type: TypeScout, Status: StatusBacklog, Actor: "cos"}
	if err := r.Validate(); err != nil || r.Owner != "cos" {
		t.Errorf("scout owner should default to the actor: %+v, %v", r, err)
	}
	bad := []CreateRequest{
		{Title: ""}, {Title: "a\nb"}, {Title: "x", Role: "Bad Role"},
		{Title: "x", Status: StatusDone}, {Title: "x", Status: StatusInProgress}, {Title: "x", Priority: ptr(9)},
		{Title: "x", Type: "epic"}, {Title: "x", Type: TypeScout}, // a scout needs an owner
		{Title: "x", Owner: "a\nb"},
		// tabwriter breaks a line on \f and \v, so either forges a row in list.
		{Title: "a\fT-1\tship"}, {Title: "a\vb"}, {Title: "a\x1bb"}, {Title: "x", Owner: "a\fb"},
	}
	for _, r := range bad {
		if err := r.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("Validate(%+v) = %v", r, err)
		}
	}
}

func TestClaimValidate(t *testing.T) {
	r := ClaimRequest{Holder: "reviewer/s1"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(r.Statuses) != 1 || r.Statuses[0] != StatusTodo || r.TTL != DefaultTTL || r.Actor != "reviewer" {
		t.Errorf("defaults not applied: %+v", r)
	}
	r = ClaimRequest{Holder: "reviewer/s1", Statuses: []Status{StatusDone}}
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("claiming done should be invalid, got %v", err)
	}
	r = ClaimRequest{Holder: "cos/s1", Statuses: []Status{StatusTodo, StatusBacklog}}
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("claim --next from backlog should be invalid, got %v", err)
	}
	r = ClaimRequest{ID: 3, Holder: "cos/s1", Statuses: []Status{StatusBacklog}}
	if err := r.Validate(); err != nil {
		t.Errorf("claiming a backlog ticket by id: %v", err)
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

func TestParseType(t *testing.T) {
	if ty, err := ParseType(" scout "); err != nil || ty != TypeScout {
		t.Errorf("ParseType = %v, %v", ty, err)
	}
	if _, err := ParseType("epic"); !errors.Is(err, ErrInvalid) {
		t.Errorf("ParseType(epic) = %v", err)
	}
}

func ptr(i int) *int { return &i }

func TestCheckFence(t *testing.T) {
	live := Ticket{ID: 1, Claim: &Claim{Holder: "jse/s1", Token: 3, ExpiresAt: time.Now().Add(time.Hour)}}
	expired := Ticket{ID: 1, Claim: &Claim{Holder: "jse/s1", Token: 3, Expired: true}}
	free := Ticket{ID: 1}
	cases := []struct {
		name   string
		t      Ticket
		holder string
		token  int64
		want   error
	}{
		{"holder and token", live, "jse/s1", 3, nil},
		{"no holder", live, "", 0, ErrClaimed},
		{"other holder", live, "jse/s2", 3, ErrClaimed},
		{"missing token", live, "jse/s1", 0, ErrInvalid},
		{"stale token", live, "jse/s1", 2, ErrLeaseLost},
		{"unclaimed, anonymous", free, "", 0, nil},
		{"unclaimed, names a lease", free, "jse/s1", 3, ErrLeaseLost},
		{"unclaimed, token only", free, "", 3, ErrLeaseLost},
		{"expired, own lease", expired, "jse/s1", 3, ErrLeaseLost},
		{"expired, anonymous", expired, "", 0, nil},
	}
	for _, c := range cases {
		err := c.t.CheckFence(c.holder, c.token)
		if (c.want == nil) != (err == nil) || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: err = %v; want %v", c.name, err, c.want)
		}
	}
}

func TestCheckGate(t *testing.T) {
	ship := Ticket{Type: TypeShip, Owner: "chief-of-staff"}
	scout := Ticket{Type: TypeScout, Owner: "chief-of-staff"}
	cases := []struct {
		name        string
		t           Ticket
		to          Status
		actor, role string
		ok          bool
	}{
		{"reviewer sets ship done", ship, StatusDone, "rev-1", RoleReviewer, true},
		{"engineer can't", ship, StatusDone, "eng", RoleEngineer, false},
		{"owner of a ship ticket can't", ship, StatusDone, "chief-of-staff", RoleChiefOfStaff, false},
		{"planner sets ship done", ship, StatusDone, "plan", RolePlanner, true},
		{"qa lead can't", ship, StatusDone, "qa", RoleQALead, false},
		{"no role can't", ship, StatusDone, "rev", "", false},
		{"other moves ungated", ship, StatusCancelled, "jse", "", true},
		{"scout owner", scout, StatusDone, "chief-of-staff", "", true},
		{"reviewer can't close a scout", scout, StatusDone, "rev", RoleReviewer, false},
		{"scout with no owner", Ticket{Type: TypeScout, Owner: "unknown"}, StatusDone, "unknown", "", false},
	}
	for _, c := range cases {
		err := c.t.CheckGate(c.to, c.actor, c.role)
		if c.ok != (err == nil) || (err != nil && !errors.Is(err, ErrForbidden)) {
			t.Errorf("%s: err = %v; want ok=%v", c.name, err, c.ok)
		}
	}
	if code, exit := Classify(fmt.Errorf("%w: x", ErrForbidden)); code != "forbidden" || exit != ExitUsage {
		t.Errorf("Classify(forbidden) = %s, %d", code, exit)
	}
}

// The numeric path accepted 0 and negative ids.
func TestIDUnmarshalRejectsNonPositive(t *testing.T) {
	for _, in := range []string{`0`, `-3`, `"T-0"`, `"-1"`} {
		var id ID
		if err := json.Unmarshal([]byte(in), &id); err == nil {
			t.Errorf("%s: got %v, want an error", in, id)
		}
	}
	var id ID
	if err := json.Unmarshal([]byte(`7`), &id); err != nil || id != 7 {
		t.Fatalf("7: %v, %v", id, err)
	}
}

func TestAreas(t *testing.T) {
	got, err := NormalizeAreas([]string{"skills/", " tools/maestro-ticket ", "skills", ""})
	if err != nil || len(got) != 2 || got[0] != "skills" || got[1] != "tools/maestro-ticket" {
		t.Fatalf("normalize %v, %v", got, err)
	}
	// Scoped packages and framework route folders are real repo paths.
	for _, good := range []string{"packages/@acme/ui", "app/(marketing)/page", "src/routes/[id]", "lib/c++", "src/a~b"} {
		if got, err := NormalizeAreas([]string{good}); err != nil || len(got) != 1 || got[0] != good {
			t.Errorf("%q: %v, %v", good, got, err)
		}
	}
	for _, bad := range []string{"/abs", "..", "a/../b", "a//b", ".", "a b", "a,b",
		// A shell runs or expands these when an area lands on a command line.
		"a$b", "a`b", "a;b", "a*b", "a?b", "a|b", "a&b", "a'b", `a"b`, `a\b`, "a<b", "a{b",
		// Spellings of another path would slip past the overlap check.
		"./tools/x", "tools/x/..", "tools/x/.", "tools/./x", "../x"} {
		if _, err := NormalizeAreas([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"tools", "tools/maestro-ticket", true},
		{"tools/maestro-ticket/cli", "tools/maestro-ticket", true},
		{"tools/maestro-ticket", "tools/maestro-ticketing", false},
		{"skills", "skills", true},
		{"skills", "playbooks", false},
	} {
		if got := AreasOverlap(c.a, c.b); got != c.want {
			t.Errorf("AreasOverlap(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
