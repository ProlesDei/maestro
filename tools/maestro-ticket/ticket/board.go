package ticket

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

// Board is a whole board as the rows a store keeps, for a move from one
// store to another: what Export leaves out, such as idempotency keys, a
// free ticket's lease token and the id counters, comes too. Times are
// milliseconds since the epoch, as both stores keep them, and 0 stands
// for none. Each table is in key order.
type Board struct {
	Tickets     []BoardTicket  `json:"tickets"`
	Comments    []BoardComment `json:"comments"`
	Events      []BoardEvent   `json:"events"`
	Idempotency []BoardIdem    `json:"idempotency"`
	Runs        []BoardRun     `json:"runs"`
	Launches    []BoardLaunch  `json:"launches"`
	ETAs        []BoardETA     `json:"etas"`
	Counters    Counters       `json:"counters"`
}

// BoardTicket is one ticket with its deps and areas. Token is the lease
// number, kept when the lease goes; Lease is nil with none.
type BoardTicket struct {
	ID            int64       `json:"id"`
	Title         string      `json:"title"`
	Body          string      `json:"body"`
	Type          string      `json:"type"`
	Status        string      `json:"status"`
	Role          string      `json:"role"`
	Priority      int         `json:"priority"`
	CreatedBy     string      `json:"created_by"`
	Owner         string      `json:"owner"`
	Tries         int         `json:"tries"`
	LeaseExpiries int         `json:"lease_expiries"`
	BlockedFrom   string      `json:"blocked_from"`
	Parked        string      `json:"parked"`
	Goal          string      `json:"goal"`
	Tier          string      `json:"tier"`
	Version       int64       `json:"version"`
	Lease         *BoardLease `json:"lease"`
	Token         int64       `json:"token"`
	Deps          []int64     `json:"deps"`  // ascending
	Areas         []string    `json:"areas"` // ascending
	CreatedAt     int64       `json:"created_at"`
	UpdatedAt     int64       `json:"updated_at"`
}

// BoardLease is a ticket's lease, live or run out.
type BoardLease struct {
	Holder  string `json:"holder"`
	Expires int64  `json:"expires"`
}

type BoardComment struct {
	ID        int64  `json:"id"`
	TicketID  int64  `json:"ticket_id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"created_at"`
}

// BoardEvent is one event. Data is the JSON text as the store keeps it,
// byte for byte.
type BoardEvent struct {
	Seq       int64  `json:"seq"`
	TicketID  int64  `json:"ticket_id"`
	Type      string `json:"type"`
	Actor     string `json:"actor"`
	Data      string `json:"data"`
	CreatedAt int64  `json:"created_at"`
}

// BoardIdem is one idempotency key and the result a retry replays.
type BoardIdem struct {
	Key         string `json:"key"`
	Op          string `json:"op"`
	RequestHash string `json:"request_hash"`
	Result      string `json:"result"`
	CreatedAt   int64  `json:"created_at"`
}

type BoardRun struct {
	Session          string  `json:"session"`
	TicketID         int64   `json:"ticket_id"`
	Actor            string  `json:"actor"`
	CostUSD          float64 `json:"cost_usd"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CreatedAt        int64   `json:"created_at"`
	UpdatedAt        int64   `json:"updated_at"`
}

// BoardLaunch is one launch. Host names the machine that started it, as
// HostID does; SQLite keeps none, since every launch in it is its own
// machine's.
type BoardLaunch struct {
	ID        int64  `json:"id"`
	Actor     string `json:"actor"`
	TicketID  int64  `json:"ticket_id"`
	PID       int    `json:"pid"`
	Stamp     string `json:"stamp"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
	Outcome   string `json:"outcome"`
	Session   string `json:"session"`
	Host      string `json:"host,omitempty"`
}

// BoardETA is one goal's ETA. Tickets keep the order they were set in.
type BoardETA struct {
	Goal    string  `json:"goal"`
	DueAt   int64   `json:"due_at"`
	Tickets []int64 `json:"tickets"`
	SetBy   string  `json:"set_by"`
	SetAt   int64   `json:"set_at"`
	MetAt   int64   `json:"met_at"`
}

// Counters are the last id each numbered table gave out, which may pass
// its largest row: an id handed out once, such as a launch's in a run
// record, is never given again.
type Counters struct {
	Tickets  int64 `json:"tickets"`
	Comments int64 `json:"comments"`
	Events   int64 `json:"events"`
	Launches int64 `json:"launches"`
}

// BoardTables names a Board's tables in the order Hashes and Diff report
// them.
var BoardTables = []string{"tickets", "comments", "events", "idempotency", "runs", "launches", "etas", "counters"}

// rows returns each table's rows as canonical JSON, one per row, with its
// key for a report.
func (b Board) rows() map[string][]keyed {
	out := map[string][]keyed{}
	add := func(table, key string, v any) {
		j, err := json.Marshal(v)
		if err != nil {
			// Every field is a plain value: this can't fail.
			panic(err)
		}
		out[table] = append(out[table], keyed{key, j})
	}
	// No list and an empty one are the same row.
	for _, t := range b.Tickets {
		t.Deps, t.Areas = nonNil(t.Deps), nonNil(t.Areas)
		add("tickets", ID(t.ID).String(), t)
	}
	for _, c := range b.Comments {
		add("comments", fmt.Sprintf("comment %d", c.ID), c)
	}
	for _, e := range b.Events {
		add("events", fmt.Sprintf("seq %d", e.Seq), e)
	}
	for _, k := range b.Idempotency {
		add("idempotency", fmt.Sprintf("key %q", k.Key), k)
	}
	for _, r := range b.Runs {
		add("runs", fmt.Sprintf("session %s", r.Session), r)
	}
	for _, l := range b.Launches {
		add("launches", fmt.Sprintf("launch %d", l.ID), l)
	}
	for _, e := range b.ETAs {
		e.Tickets = nonNil(e.Tickets)
		add("etas", fmt.Sprintf("goal %s", e.Goal), e)
	}
	add("counters", "counters", b.Counters)
	return out
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

type keyed struct {
	key string
	row []byte
}

// Counts returns how many rows each table holds.
func (b Board) Counts() map[string]int {
	return map[string]int{
		"tickets": len(b.Tickets), "comments": len(b.Comments), "events": len(b.Events),
		"idempotency": len(b.Idempotency), "runs": len(b.Runs), "launches": len(b.Launches), "etas": len(b.ETAs),
	}
}

// Empty reports whether the board holds no row and no counter has moved.
func (b Board) Empty() bool {
	for _, n := range b.Counts() {
		if n > 0 {
			return false
		}
	}
	return b.Counters == Counters{}
}

// LastSeq is the newest event's seq, 0 for none.
func (b Board) LastSeq() int64 {
	if len(b.Events) == 0 {
		return 0
	}
	return b.Events[len(b.Events)-1].Seq
}

// Hashes returns a SHA-256 of each table, over its rows' canonical JSON
// in key order, so two stores' copies match only if every row does.
func (b Board) Hashes() map[string]string {
	out := map[string]string{}
	rows := b.rows()
	for _, table := range BoardTables {
		h := sha256.New()
		for _, r := range rows[table] {
			h.Write(r.row)
			h.Write([]byte{'\n'})
		}
		out[table] = hex.EncodeToString(h.Sum(nil))
	}
	return out
}

// Diff compares b with other row by row and names the first table and
// row that differ; "" when they match.
func (b Board) Diff(other Board) string {
	mine, theirs := b.rows(), other.rows()
	for _, table := range BoardTables {
		a, o := mine[table], theirs[table]
		for i := 0; i < max(len(a), len(o)); i++ {
			switch {
			case i >= len(a):
				return fmt.Sprintf("%s: %s is only in the second", table, o[i].key)
			case i >= len(o):
				return fmt.Sprintf("%s: %s is only in the first", table, a[i].key)
			case a[i].key != o[i].key:
				return fmt.Sprintf("%s: %s in the first, %s in the second", table, a[i].key, o[i].key)
			case !slices.Equal(a[i].row, o[i].row):
				return fmt.Sprintf("%s: %s differs", table, a[i].key)
			}
		}
	}
	return ""
}
