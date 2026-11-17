package mongostore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/storetest"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// coordStore opens a store on a database of its own, named for these
// tests, so runs beside other suites never share one.
func coordStore(t *testing.T, opts Options) *Store {
	t.Helper()
	uri := testURI(t)
	b := make([]byte, 6)
	rand.Read(b)
	opts.Database, opts.Create = "p3coord_"+hex.EncodeToString(b), true
	freshNamed(t, uri, opts.Database)
	s, err := Open(context.Background(), uri, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// freshNamed drops database name when t ends.
func freshNamed(t *testing.T, uri, name string) {
	t.Cleanup(func() {
		s, err := Open(context.Background(), uri, Options{Database: name})
		if err != nil {
			t.Errorf("drop %s: %v", name, err)
			return
		}
		defer s.Close()
		if err := testDB(t, s).Drop(context.Background()); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})
}

func TestMarks(t *testing.T) {
	s := coordStore(t, Options{})
	ctx := context.Background()
	if err := s.CreateMark(ctx, "handouts", "T-1.reviewer", "review T-1\n"); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.CreateMark(ctx, "handouts", "T-1.reviewer", "again\n"), fs.ErrExist)
	m, err := s.ReadMark(ctx, "handouts", "T-1.reviewer")
	if err != nil || m.Body != "review T-1\n" || m.Name != "T-1.reviewer" || m.Age < 0 || m.Age > time.Minute || m.Ver == "" {
		t.Fatalf("read: %+v %v", m, err)
	}
	_, err = s.ReadMark(ctx, "handouts", "T-2.reviewer")
	wantErr(t, err, fs.ErrNotExist)

	// A write is a new copy: a remove of the copy read before misses it.
	if err := s.WriteMark(ctx, "handouts", "T-1.reviewer", "b\n"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RemoveMarkIf(ctx, "handouts", "T-1.reviewer", m.Ver); ok || err != nil {
		t.Fatalf("removed a copy written over: %v %v", ok, err)
	}
	m2, _ := s.ReadMark(ctx, "handouts", "T-1.reviewer")
	if m2.Body != "b\n" || m2.Ver == m.Ver {
		t.Fatalf("after write: %+v", m2)
	}
	if err := s.WriteMark(ctx, "runs", "T-1.build.active", "R-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	ms, err := s.ListMarks(ctx, "handouts")
	if err != nil || len(ms) != 1 || ms[0].Name != "T-1.reviewer" || ms[0].Body != "" {
		t.Fatalf("list: %+v %v", ms, err)
	}
	if ok, err := s.RemoveMarkIf(ctx, "handouts", "T-1.reviewer", m2.Ver); !ok || err != nil {
		t.Fatalf("remove the copy read: %v %v", ok, err)
	}
	wantErr(t, s.RemoveMark(ctx, "handouts", "T-1.reviewer"), fs.ErrNotExist)
	if err := s.RemoveMark(ctx, "runs", "T-1.build.active"); err != nil {
		t.Fatal(err)
	}
}

// Of creates at once, from many clients, one wins.
func TestMarkCreateRace(t *testing.T) {
	s := coordStore(t, Options{})
	var wg sync.WaitGroup
	won := make(chan int, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.CreateMark(context.Background(), "runs", "T-1.build.active", "")
			if err == nil {
				won <- i
			} else if !errors.Is(err, fs.ErrExist) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(won)
	if n := len(won); n != 1 {
		t.Fatalf("%d creates won", n)
	}
}

// The halt refuses claims and revivals, as STOP does, until it lifts.
func TestHaltRefusesClaims(t *testing.T) {
	clk := storetest.NewClock()
	s := coordStore(t, Options{Now: clk.Now})
	ctx := context.Background()
	a, _ := s.Create(ctx, ticket.CreateRequest{Title: "a"})
	b, _ := s.Create(ctx, ticket.CreateRequest{Title: "b"})
	c, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: a.ID, Holder: "jse/s1", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := s.SetHalt(ctx, true); !changed || err != nil {
		t.Fatalf("halt: %v %v", changed, err)
	}
	if changed, err := s.SetHalt(ctx, true); changed || err != nil {
		t.Fatalf("halt again: %v %v", changed, err)
	}
	if h, err := s.Halted(ctx); !h || err != nil {
		t.Fatalf("halted: %v %v", h, err)
	}
	_, err = s.ClaimNext(ctx, ticket.ClaimRequest{ID: b.ID, Holder: "jse/s2"})
	wantErr(t, err, ticket.ErrStopped)
	if !strings.Contains(err.Error(), "stop --all") {
		t.Fatalf("halt error: %v", err)
	}
	// A live lease still beats; one that ran out is not revived.
	if _, err := s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: a.ID, Holder: "jse/s1", Token: c.Claim.Token, TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(2 * time.Minute)
	_, err = s.Heartbeat(ctx, ticket.HeartbeatRequest{ID: a.ID, Holder: "jse/s1", Token: c.Claim.Token, TTL: time.Minute})
	wantErr(t, err, ticket.ErrLeaseLost)
	wantErr(t, err, ticket.ErrStopped)
	if changed, err := s.SetHalt(ctx, false); !changed || err != nil {
		t.Fatalf("lift: %v %v", changed, err)
	}
	if _, err := s.ClaimNext(ctx, ticket.ClaimRequest{ID: b.ID, Holder: "jse/s2"}); err != nil {
		t.Fatal(err)
	}
}

// A write that read no halt holds the writer: a halt set meanwhile can't
// commit until it ends, so no claim that checked commits after a halt.
// One that begins after the halt sees it.
func TestHaltWaitsForAWriteInFlight(t *testing.T) {
	s := coordStore(t, Options{})
	ctx := context.Background()
	halted := make(chan error, 1)
	first := true
	err := s.write(ctx, func(tx *txn) error {
		if err := tx.halted(); err != nil {
			return err
		}
		if first {
			first = false
			go func() { _, err := s.SetHalt(ctx, true); halted <- err }()
			select {
			case err := <-halted:
				t.Errorf("the halt committed under a write in flight: %v", err)
			case <-time.After(500 * time.Millisecond):
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-halted; err != nil {
		t.Fatal(err)
	}
	err = s.write(ctx, func(tx *txn) error { return tx.halted() })
	wantErr(t, err, ticket.ErrStopped)
}

// A launch keeps whose run it is, and LaunchesSince lists the open ones
// and those ended lately.
func TestLaunchHost(t *testing.T) {
	clk := storetest.NewClock()
	s := coordStore(t, Options{Now: clk.Now})
	ctx := context.Background()
	tk, _ := s.Create(ctx, ticket.CreateRequest{Title: "x"})
	old, err := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "planner", PID: 9, Host: "aa", Job: "plan", Limit: "2h"})
	if err != nil {
		t.Fatal(err)
	}
	s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: old.ID, Outcome: ticket.LaunchResult})
	clk.Advance(time.Hour)
	l, err := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "planner", Ticket: tk.ID, PID: 7, Stamp: "b|n|1",
		Host: "aa", Job: "judge", Tickets: []ticket.ID{tk.ID}, Limit: "45m"})
	if err != nil {
		t.Fatal(err)
	}
	ended, _ := s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "reviewer", PID: 8, Host: "bb", Job: "review"})
	clk.Advance(time.Minute)
	s.EndLaunch(ctx, ticket.EndLaunchRequest{ID: ended.ID, Outcome: ticket.LaunchNoResult})
	open, err := s.OpenLaunches(ctx)
	if err != nil || len(open) != 1 {
		t.Fatalf("open: %+v %v", open, err)
	}
	if g := open[0]; g.Host != "aa" || g.Job != "judge" || g.Limit != "45m" || len(g.Tickets) != 1 || g.Tickets[0] != tk.ID || g.LimitOr(0) != 45*time.Minute {
		t.Fatalf("launch: %+v", g)
	}
	recent, err := s.LaunchesSince(ctx, clk.Now().Add(-10*time.Minute))
	if err != nil || len(recent) != 2 || recent[0].ID != l.ID || recent[1].ID != ended.ID || recent[1].EndedAt.IsZero() {
		t.Fatalf("since: %+v %v", recent, err)
	}
	_, err = s.StartLaunch(ctx, ticket.StartLaunchRequest{Actor: "planner", PID: 7, Limit: "soon"})
	wantErr(t, err, ticket.ErrInvalid)
}

// The writer document keeps the code each host wrote with, and doctor
// warns when they differ or this binary is older than one seen.
func TestCodeSkew(t *testing.T) {
	s := coordStore(t, Options{Host: "aaaa1111"})
	ctx := context.Background()
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	var w writerDoc
	testDB(t, s).Collection(cMeta).FindOne(ctx, bson.D{{Key: "_id", Value: writerID}}).Decode(&w)
	if w.Code != Code || w.Hosts["aaaa1111"] != Code {
		t.Fatalf("writer: code %d hosts %v", w.Code, w.Hosts)
	}
	binaries := func() ticket.Check {
		t.Helper()
		rep, err := s.Doctor(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range rep.Checks {
			if c.Name == "binaries" {
				return c
			}
		}
		t.Fatalf("no binaries check: %+v", rep.Checks)
		return ticket.Check{}
	}
	if c := binaries(); !c.OK || c.Warn {
		t.Fatalf("one host: %+v", c)
	}
	setMeta(t, s, bson.D{{Key: "hosts.bbbb2222", Value: Code - 1}})
	if c := binaries(); !c.Warn || !strings.Contains(c.Detail, "bbbb2222 code 1") {
		t.Fatalf("hosts differ: %+v", c)
	}
	setMeta(t, s, bson.D{{Key: "hosts.bbbb2222", Value: Code}, {Key: "code", Value: Code + 1}})
	if c := binaries(); !c.Warn || !strings.Contains(c.Detail, "older") {
		t.Fatalf("older binary: %+v", c)
	}
	if _, err := Open(ctx, testURI(t), Options{Database: s.Database(), Host: "a.b"}); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("a dotted host: %v", err)
	}
}
