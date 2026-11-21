package mongostore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/connstring"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// A move copies a whole board between this store and SQLite. While one
// runs, the writer document's moving field names the store it moves to,
// and every call but the mover's refuses, so the board can't change
// under the copy. A copy too big for one transaction is written outside
// one, and safe to run again: each run rewrites every collection but
// events, which it only adds to, since a move's server role may only
// insert and find there.

// The stores a board moves to.
const (
	MoveIn  = "mongodb" // migrate --to mongodb copies a SQLite board in
	MoveOut = "sqlite"  // migrate --to sqlite copies this one out
)

// dataCollections hold a board's rows: all but meta.
var dataCollections = []string{cTickets, cComments, cEvents, cIdempotency, cRuns, cLaunches, cETAs}

// Ping checks that uri names a replica set this machine reaches, with a
// client of its own. No error it returns holds the URI.
func Ping(ctx context.Context, uri string) error {
	if _, err := connstring.ParseAndValidate(uri); err != nil {
		return Redact(fmt.Errorf("%w: bad MongoDB URI: %v", ticket.ErrInvalid, err), uri)
	}
	c, err := mongo.Connect(options.Client().SetServerSelectionTimeout(selectTimeout).ApplyURI(uri))
	if err != nil {
		return Redact(fmt.Errorf("connect to MongoDB: %w", err), uri)
	}
	defer c.Disconnect(context.WithoutCancel(ctx))
	pctx, cancel := context.WithTimeout(ctx, selectTimeout+selectTimeout)
	defer cancel()
	if err := c.Ping(pctx, readpref.Primary()); err != nil {
		return Redact(fmt.Errorf("%w: can't reach the replica set's primary: %v", ticket.ErrInvalid, err), uri)
	}
	return nil
}

// Move is the board's move state.
type Move struct {
	To    string // the store a move under way copies the board to; "" for none
	Host  string // the host id of the machine that move runs on, or that set Moved
	Moved bool   // the tombstone: the board was handed back to SQLite
}

// State reads the move under way, if any, and the moved tombstone.
func (s *Store) State(ctx context.Context) (Move, error) {
	db, done, err := s.conn()
	if err != nil {
		return Move{}, err
	}
	defer done()
	var w writerDoc
	err = db.Collection(cMeta).FindOne(ctx, bson.D{{Key: "_id", Value: writerID}}).Decode(&w)
	return Move{To: w.Moving, Host: w.MoveHost, Moved: w.Moved}, Redact(err, s.client.uri)
}

// errNotMover refuses a move call on a store not opened as one.
var errNotMover = fmt.Errorf("%w: a move needs a store opened with Mover set to this machine's host id", ticket.ErrInvalid)

// mine refuses a move under way that another machine runs: two copies at
// once would write over each other.
func (s *Store) mine(w writerDoc) error {
	if w.Moving != "" && w.MoveHost != s.mover {
		return fmt.Errorf("%w: maestro-ticket migrate --to %s is under way on another machine (host id %s); finish it there",
			ticket.ErrInvalid, w.Moving, w.MoveHost)
	}
	return nil
}

// StartMove marks the board moving to the store to, MoveIn or MoveOut,
// from this machine, in a write transaction, so no write already in
// flight commits after it. Moving in needs an empty board. Starting this
// machine's move again does nothing more.
func (s *Store) StartMove(ctx context.Context, to string) error {
	if s.mover == "" {
		return errNotMover
	}
	return s.write(ctx, func(tx *txn) error {
		w := tx.w
		if err := s.mine(*w); err != nil {
			return err
		}
		switch {
		case w.Moved:
			return fmt.Errorf("%w: %s", ticket.ErrInvalid, movedMsg)
		case w.Moving == to:
			return nil
		case w.Moving != "":
			return fmt.Errorf("%w: maestro-ticket migrate --to %s is under way, or was cut short; run it again to finish it", ticket.ErrInvalid, w.Moving)
		}
		if to == MoveIn {
			if w.Tickets+w.Comments+w.Events+w.Launches > 0 {
				return fmt.Errorf("%w: database %s holds a board already", ticket.ErrInvalid, s.name)
			}
			for _, c := range dataCollections {
				n, err := tx.coll(c).count(bson.D{})
				if err != nil {
					return err
				}
				if n > 0 {
					return fmt.Errorf("%w: database %s holds a board already: %s has %d documents", ticket.ErrInvalid, s.name, c, n)
				}
			}
		}
		_, err := tx.coll(cMeta).update(bson.D{{Key: "_id", Value: writerID}}, bson.D{{Key: "$set", Value: bson.D{
			{Key: "moving", Value: to}, {Key: "moving_host", Value: s.mover},
		}}})
		return err
	})
}

// EndMove ends this machine's move; with moved, it also sets the moved
// tombstone, after which every call to the board fails. A move to SQLite
// that CallOffMove ended can't then set it: the board took writes since.
func (s *Store) EndMove(ctx context.Context, moved bool) error {
	if s.mover == "" {
		return errNotMover
	}
	return s.write(ctx, func(tx *txn) error {
		if err := s.mine(*tx.w); err != nil {
			return err
		}
		if moved && !tx.w.Moved && tx.w.Moving != MoveOut {
			return fmt.Errorf("%w: no move to sqlite is under way on database %s: it was called off, and the board may have taken writes since; nothing is marked moved, so run migrate --to sqlite again", ticket.ErrInvalid, s.name)
		}
		up := bson.D{{Key: "$unset", Value: bson.D{{Key: "moving", Value: ""}, {Key: "moving_host", Value: ""}}}}
		if moved {
			// The tombstone keeps the host that moved the board back, so
			// only it may finish the move.
			up = bson.D{{Key: "$unset", Value: bson.D{{Key: "moving", Value: ""}}},
				{Key: "$set", Value: bson.D{{Key: "moved", Value: true}, {Key: "moving_host", Value: s.mover}}}}
		}
		_, err := tx.coll(cMeta).update(bson.D{{Key: "_id", Value: writerID}}, up)
		return err
	})
}

// CallOffMove ends the move to SQLite that the machine host runs, from
// another machine: the owner's way out when host is gone, as a move held
// open would refuse every call for good. It changes nothing unless that
// move is still the one under way.
func (s *Store) CallOffMove(ctx context.Context, host string) error {
	if s.mover == "" {
		return errNotMover
	}
	return s.write(ctx, func(tx *txn) error {
		if w := tx.w; w.Moved || w.Moving != MoveOut || w.MoveHost != host {
			return fmt.Errorf("%w: the move to sqlite from host id %s is no longer under way on database %s; run migrate --to sqlite --abort again to see what is", ticket.ErrInvalid, host, s.name)
		}
		_, err := tx.coll(cMeta).update(bson.D{{Key: "_id", Value: writerID}},
			bson.D{{Key: "$unset", Value: bson.D{{Key: "moving", Value: ""}, {Key: "moving_host", Value: ""}}}})
		return err
	})
}

// Load writes b into a board this machine is moving in, as it is: ids,
// lease tokens, idempotency keys, event data and counters. Each
// collection but events is emptied and written whole; events already
// there must match b's, and the rest are added. So a Load cut short is
// finished by the next.
func (s *Store) Load(ctx context.Context, b ticket.Board) error {
	return Redact(s.load(ctx, b), s.client.uri)
}

func (s *Store) load(ctx context.Context, b ticket.Board) error {
	if s.mover == "" {
		return errNotMover
	}
	db, done, err := s.conn()
	if err != nil {
		return err
	}
	defer done()
	var w writerDoc
	if err := db.Collection(cMeta).FindOne(ctx, bson.D{{Key: "_id", Value: writerID}}).Decode(&w); err != nil {
		return err
	}
	if err := s.mine(w); err != nil {
		return err
	}
	if w.Moved || w.Moving != MoveIn {
		return fmt.Errorf("%w: Load needs a board moving in: StartMove(MoveIn) first", ticket.ErrInvalid)
	}
	latest := map[int64]string{}
	for _, c := range b.Comments {
		latest[c.TicketID] = c.Body // comments come in id order: the last is the latest
	}
	tickets := make([]any, len(b.Tickets))
	for i, t := range b.Tickets {
		d := ticketDoc{ID: t.ID, Title: t.Title, Body: t.Body, Type: t.Type, Status: t.Status, Role: t.Role, Priority: t.Priority,
			CreatedBy: t.CreatedBy, Owner: t.Owner, Tries: t.Tries, LeaseExpiries: t.LeaseExpiries, BlockedFrom: t.BlockedFrom,
			Parked: t.Parked, Goal: t.Goal, Tier: t.Tier, DirectQA: t.DirectQA, Version: t.Version, Token: t.Token,
			Deps: nonNil(slices.Clone(t.Deps)), Areas: nonNil(slices.Clone(t.Areas)),
			CreatedAt: dt(t.CreatedAt), UpdatedAt: dt(t.UpdatedAt)}
		slices.Sort(d.Deps)
		if t.Lease != nil {
			d.Lease = &leaseDoc{Holder: t.Lease.Holder, Expires: dt(t.Lease.Expires)}
		}
		if body, ok := latest[t.ID]; ok {
			l, cut := latestOf(body)
			d.Latest, d.LatestCut = &l, cut
		}
		tickets[i] = d
	}
	comments := make([]any, len(b.Comments))
	for i, c := range b.Comments {
		comments[i] = commentDoc{ID: c.ID, TicketID: c.TicketID, Author: c.Author, Body: c.Body, CreatedAt: dt(c.CreatedAt)}
	}
	idem := make([]any, len(b.Idempotency))
	for i, k := range b.Idempotency {
		idem[i] = idemDoc{Key: k.Key, Op: k.Op, RequestHash: k.RequestHash, Result: k.Result, CreatedAt: dt(k.CreatedAt)}
	}
	runs := make([]any, len(b.Runs))
	for i, r := range b.Runs {
		runs[i] = runDoc{Session: r.Session, TicketID: ref(r.TicketID), Actor: r.Actor, CostUSD: r.CostUSD, InputTokens: r.InputTokens,
			OutputTokens: r.OutputTokens, CacheWriteTokens: r.CacheWriteTokens, CacheReadTokens: r.CacheReadTokens,
			CreatedAt: dt(r.CreatedAt), UpdatedAt: dt(r.UpdatedAt)}
	}
	launches := make([]any, len(b.Launches))
	for i, l := range b.Launches {
		d := launchDoc{ID: l.ID, Actor: l.Actor, TicketID: ref(l.TicketID), PID: l.PID, Stamp: l.Stamp, StartedAt: dt(l.StartedAt),
			Outcome: l.Outcome, Session: l.Session, Host: l.Host}
		if l.EndedAt != 0 {
			e := dt(l.EndedAt)
			d.EndedAt = &e
		}
		launches[i] = d
	}
	etas := make([]any, len(b.ETAs))
	for i, e := range b.ETAs {
		d := etaDoc{Goal: e.Goal, DueAt: dt(e.DueAt), Tickets: nonNil(slices.Clone(e.Tickets)), SetBy: e.SetBy, SetAt: dt(e.SetAt)}
		if e.MetAt != 0 {
			m := dt(e.MetAt)
			d.MetAt = &m
		}
		etas[i] = d
	}
	for _, x := range []struct {
		coll string
		docs []any
	}{{cTickets, tickets}, {cComments, comments}, {cIdempotency, idem}, {cRuns, runs}, {cLaunches, launches}, {cETAs, etas}} {
		c := db.Collection(x.coll)
		if _, err := c.DeleteMany(ctx, bson.D{}); err != nil {
			return fmt.Errorf("empty %s: %w", x.coll, err)
		}
		if len(x.docs) == 0 {
			continue
		}
		if _, err := c.InsertMany(ctx, x.docs); err != nil {
			return fmt.Errorf("write %s: %w", x.coll, err)
		}
	}
	if err := s.loadEvents(ctx, db, b.Events); err != nil {
		return err
	}
	_, err = db.Collection(cMeta).UpdateOne(ctx, bson.D{{Key: "_id", Value: writerID}}, bson.D{{Key: "$set", Value: bson.D{
		{Key: "tickets", Value: b.Counters.Tickets}, {Key: "comments", Value: b.Counters.Comments},
		{Key: "events", Value: b.Counters.Events}, {Key: "launches", Value: b.Counters.Launches},
	}}})
	return err
}

// loadEvents adds the events not there yet, after checking that those
// there match.
func (s *Store) loadEvents(ctx context.Context, db *mongo.Database, evs []ticket.BoardEvent) error {
	c := db.Collection(cEvents)
	cur, err := c.Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return err
	}
	var have []eventDoc
	if err := cur.All(ctx, &have); err != nil {
		return err
	}
	want := map[int64]eventDoc{}
	docs := make([]eventDoc, len(evs))
	for i, e := range evs {
		d, err := eventFromBoard(e)
		if err != nil {
			return err
		}
		docs[i] = d
		want[e.Seq] = d
	}
	there := map[int64]bool{}
	for _, h := range have {
		if w, ok := want[h.Seq]; !ok || w != h {
			return fmt.Errorf("%w: database %s holds event %d, which the board being moved in doesn't have as it is", ticket.ErrInvalid, s.name, h.Seq)
		}
		there[h.Seq] = true
	}
	var add []any
	for _, d := range docs {
		if !there[d.Seq] {
			add = append(add, d)
		}
	}
	if len(add) == 0 {
		return nil
	}
	if _, err := c.InsertMany(ctx, add); err != nil {
		return fmt.Errorf("write events: %w", err)
	}
	return nil
}

// eventFromBoard is an event as this store keeps it, with the fields
// queries need copied out of its data, as event does.
func eventFromBoard(e ticket.BoardEvent) (eventDoc, error) {
	d := eventDoc{Seq: e.Seq, TicketID: e.TicketID, Type: e.Type, Actor: e.Actor, Data: e.Data, CreatedAt: dt(e.CreatedAt)}
	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(e.Data), &data); err != nil {
		return d, fmt.Errorf("event %d's data: %w", e.Seq, err)
	}
	if raw, ok := data["token"]; ok && e.Type == ticket.EventLeaseRevived {
		if err := json.Unmarshal(raw, &d.Token); err != nil {
			return d, fmt.Errorf("event %d's token: %w", e.Seq, err)
		}
	}
	if raw, ok := data["split_from"]; ok {
		var id ticket.ID
		if err := json.Unmarshal(raw, &id); err != nil {
			return d, fmt.Errorf("event %d's split_from: %w", e.Seq, err)
		}
		d.SplitFrom = int64(id)
	}
	return d, nil
}

func ref(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// Board reads every collection as rows, and checks that what each ticket
// and event copies from elsewhere matches it. Its reads span no
// transaction: run it while a move holds the board still.
func (s *Store) Board(ctx context.Context) (ticket.Board, error) {
	b, err := s.board(ctx)
	return b, Redact(err, s.client.uri)
}

func (s *Store) board(ctx context.Context) (ticket.Board, error) {
	b := ticket.Board{Tickets: []ticket.BoardTicket{}, Comments: []ticket.BoardComment{}, Events: []ticket.BoardEvent{},
		Idempotency: []ticket.BoardIdem{}, Runs: []ticket.BoardRun{}, Launches: []ticket.BoardLaunch{}, ETAs: []ticket.BoardETA{}}
	db, done, err := s.conn()
	if err != nil {
		return b, err
	}
	defer done()
	all := func(coll string, out any) error {
		cur, err := db.Collection(coll).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
		if err != nil {
			return err
		}
		return cur.All(ctx, out)
	}
	var cs []commentDoc
	if err := all(cComments, &cs); err != nil {
		return b, err
	}
	latest := map[int64]string{}
	for _, c := range cs {
		b.Comments = append(b.Comments, ticket.BoardComment{ID: c.ID, TicketID: c.TicketID, Author: c.Author, Body: c.Body, CreatedAt: int64(c.CreatedAt)})
		latest[c.TicketID] = c.Body
	}
	var ts []ticketDoc
	if err := all(cTickets, &ts); err != nil {
		return b, err
	}
	for _, d := range ts {
		t := ticket.BoardTicket{ID: d.ID, Title: d.Title, Body: d.Body, Type: d.Type, Status: d.Status, Role: d.Role, Priority: d.Priority,
			CreatedBy: d.CreatedBy, Owner: d.Owner, Tries: d.Tries, LeaseExpiries: d.LeaseExpiries, BlockedFrom: d.BlockedFrom,
			Parked: d.Parked, Goal: d.Goal, Tier: d.Tier, DirectQA: d.DirectQA, Version: d.Version, Token: d.Token,
			Deps: nonNil(slices.Clone(d.Deps)), Areas: nonNil(slices.Clone(d.Areas)),
			CreatedAt: int64(d.CreatedAt), UpdatedAt: int64(d.UpdatedAt)}
		slices.Sort(t.Deps)
		slices.Sort(t.Areas)
		if d.Lease != nil {
			t.Lease = &ticket.BoardLease{Holder: d.Lease.Holder, Expires: int64(d.Lease.Expires)}
		}
		body, has := latest[d.ID]
		l, cut := latestOf(body)
		if has != (d.Latest != nil) || has && (*d.Latest != l || d.LatestCut != cut) {
			return b, fmt.Errorf("%w: %s's latest comment start doesn't match its comments", ticket.ErrInvalid, ticket.ID(d.ID))
		}
		b.Tickets = append(b.Tickets, t)
	}
	var evs []eventDoc
	if err := all(cEvents, &evs); err != nil {
		return b, err
	}
	for _, e := range evs {
		be := ticket.BoardEvent{Seq: e.Seq, TicketID: e.TicketID, Type: e.Type, Actor: e.Actor, Data: e.Data, CreatedAt: int64(e.CreatedAt)}
		want, err := eventFromBoard(be)
		if err != nil {
			return b, err
		}
		if want != e {
			return b, fmt.Errorf("%w: event %d's token or split_from doesn't match its data", ticket.ErrInvalid, e.Seq)
		}
		b.Events = append(b.Events, be)
	}
	var ks []idemDoc
	if err := all(cIdempotency, &ks); err != nil {
		return b, err
	}
	for _, k := range ks {
		b.Idempotency = append(b.Idempotency, ticket.BoardIdem{Key: k.Key, Op: k.Op, RequestHash: k.RequestHash, Result: k.Result, CreatedAt: int64(k.CreatedAt)})
	}
	var rs []runDoc
	if err := all(cRuns, &rs); err != nil {
		return b, err
	}
	for _, r := range rs {
		x := ticket.BoardRun{Session: r.Session, Actor: r.Actor, CostUSD: r.CostUSD, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
			CacheWriteTokens: r.CacheWriteTokens, CacheReadTokens: r.CacheReadTokens, CreatedAt: int64(r.CreatedAt), UpdatedAt: int64(r.UpdatedAt)}
		if r.TicketID != nil {
			x.TicketID = *r.TicketID
		}
		b.Runs = append(b.Runs, x)
	}
	var ls []launchDoc
	if err := all(cLaunches, &ls); err != nil {
		return b, err
	}
	for _, d := range ls {
		l := ticket.BoardLaunch{ID: d.ID, Actor: d.Actor, PID: d.PID, Stamp: d.Stamp, StartedAt: int64(d.StartedAt), Outcome: d.Outcome, Session: d.Session, Host: d.Host}
		if d.TicketID != nil {
			l.TicketID = *d.TicketID
		}
		if d.EndedAt != nil {
			l.EndedAt = int64(*d.EndedAt)
		}
		b.Launches = append(b.Launches, l)
	}
	var es []etaDoc
	if err := all(cETAs, &es); err != nil {
		return b, err
	}
	for _, e := range es {
		x := ticket.BoardETA{Goal: e.Goal, DueAt: int64(e.DueAt), Tickets: nonNil(slices.Clone(e.Tickets)), SetBy: e.SetBy, SetAt: int64(e.SetAt)}
		if e.MetAt != nil {
			x.MetAt = int64(*e.MetAt)
		}
		b.ETAs = append(b.ETAs, x)
	}
	// Go's order, which the other store sorts by too.
	slices.SortFunc(b.Idempotency, func(x, y ticket.BoardIdem) int { return strings.Compare(x.Key, y.Key) })
	slices.SortFunc(b.Runs, func(x, y ticket.BoardRun) int { return strings.Compare(x.Session, y.Session) })
	slices.SortFunc(b.ETAs, func(x, y ticket.BoardETA) int { return strings.Compare(x.Goal, y.Goal) })
	var w writerDoc
	err = db.Collection(cMeta).FindOne(ctx, bson.D{{Key: "_id", Value: writerID}}).Decode(&w)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return b, fmt.Errorf("%w: database %s holds no maestro board", ticket.ErrInvalid, s.name)
	}
	b.Counters = ticket.Counters{Tickets: w.Tickets, Comments: w.Comments, Events: w.Events, Launches: w.Launches}
	return b, err
}
