package mongostore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// latestKeep is how many characters of its latest comment a ticket keeps,
// so list's --latest-prefix reads no comment unless the prefix is longer.
const latestKeep = 64

// ticketDoc is a ticket as the tickets collection holds it. Token is the
// lease number: it stays when the lease goes, so the next claim gets a
// larger one. Latest is the latest comment's first latestKeep characters,
// nil with no comment; LatestCut says the comment is longer.
type ticketDoc struct {
	ID            int64         `bson:"_id"`
	Title         string        `bson:"title"`
	Body          string        `bson:"body"`
	Type          string        `bson:"type"`
	Status        string        `bson:"status"`
	Role          string        `bson:"role"`
	Priority      int           `bson:"priority"`
	CreatedBy     string        `bson:"created_by"`
	Owner         string        `bson:"owner"`
	Tries         int           `bson:"tries"`
	LeaseExpiries int           `bson:"lease_expiries"`
	BlockedFrom   string        `bson:"blocked_from"`
	Parked        string        `bson:"parked"`
	Goal          string        `bson:"goal"`
	Tier          string        `bson:"tier"`
	Version       int64         `bson:"version"`
	Lease         *leaseDoc     `bson:"lease"`
	Token         int64         `bson:"token"`
	Deps          []int64       `bson:"deps"`
	Areas         []string      `bson:"areas"`
	Latest        *string       `bson:"latest"`
	LatestCut     bool          `bson:"latest_cut"`
	CreatedAt     bson.DateTime `bson:"created_at"`
	UpdatedAt     bson.DateTime `bson:"updated_at"`
}

type leaseDoc struct {
	Holder  string        `bson:"holder"`
	Expires bson.DateTime `bson:"expires"`
}

type commentDoc struct {
	ID        int64         `bson:"_id"`
	TicketID  int64         `bson:"ticket_id"`
	Author    string        `bson:"author"`
	Body      string        `bson:"body"`
	CreatedAt bson.DateTime `bson:"created_at"`
}

// eventDoc is one event. Data is the JSON text SQLite's store keeps, byte
// for byte; Token and SplitFrom copy the fields of it queries need.
type eventDoc struct {
	Seq       int64         `bson:"_id"`
	TicketID  int64         `bson:"ticket_id"`
	Type      string        `bson:"type"`
	Actor     string        `bson:"actor"`
	Data      string        `bson:"data"`
	CreatedAt bson.DateTime `bson:"created_at"`
	Token     int64         `bson:"token,omitempty"`
	SplitFrom int64         `bson:"split_from,omitempty"`
}

type idemDoc struct {
	Key         string        `bson:"_id"`
	Op          string        `bson:"op"`
	RequestHash string        `bson:"request_hash"`
	Result      string        `bson:"result"`
	CreatedAt   bson.DateTime `bson:"created_at"`
}

func byID(id ticket.ID) bson.D { return bson.D{{Key: "_id", Value: int64(id)}} }

// toTicket builds a ticket from its document at now.
func toTicket(d ticketDoc, now int64, revivals int) ticket.Ticket {
	t := ticket.Ticket{
		ID: ticket.ID(d.ID), Title: d.Title, Body: d.Body, Type: ticket.Type(d.Type), Status: ticket.Status(d.Status),
		Role: d.Role, Priority: d.Priority, CreatedBy: d.CreatedBy, Owner: d.Owner, Tries: d.Tries,
		LeaseExpiries: d.LeaseExpiries, BlockedFrom: ticket.Status(d.BlockedFrom), Parked: ticket.Parked(d.Parked),
		Goal: d.Goal, Tier: d.Tier, Version: d.Version,
		DependsOn: []ticket.ID{}, Areas: []string{},
		CreatedAt: msTime(int64(d.CreatedAt)), UpdatedAt: msTime(int64(d.UpdatedAt)),
	}
	deps := slices.Clone(d.Deps)
	slices.Sort(deps)
	for _, x := range deps {
		t.DependsOn = append(t.DependsOn, ticket.ID(x))
	}
	if len(d.Areas) > 0 {
		t.Areas = slices.Clone(d.Areas)
		slices.Sort(t.Areas)
	}
	if d.Lease != nil {
		exp := int64(d.Lease.Expires)
		t.Claim = &ticket.Claim{Holder: d.Lease.Holder, Token: d.Token, ExpiresAt: msTime(exp), Expired: exp <= now, Revivals: revivals}
	}
	return t
}

// toTickets builds tickets from docs, counting each held lease's
// revivals: the lease_revived events with its current token.
func (tx *txn) toTickets(docs []ticketDoc) ([]ticket.Ticket, error) {
	var held []int64
	for _, d := range docs {
		if d.Lease != nil {
			held = append(held, d.ID)
		}
	}
	type key struct{ id, token int64 }
	revived := map[key]int{}
	if len(held) > 0 {
		var evs []eventDoc
		err := tx.coll(cEvents).find(bson.D{
			{Key: "type", Value: ticket.EventLeaseRevived}, {Key: "ticket_id", Value: bson.D{{Key: "$in", Value: held}}},
		}, &evs, options.Find().SetProjection(bson.D{{Key: "ticket_id", Value: 1}, {Key: "token", Value: 1}}))
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			revived[key{e.TicketID, e.Token}]++
		}
	}
	out := make([]ticket.Ticket, len(docs))
	for i, d := range docs {
		out[i] = toTicket(d, tx.now, revived[key{d.ID, d.Token}])
	}
	return out, nil
}

func (tx *txn) asTicket(d ticketDoc) (ticket.Ticket, error) {
	ts, err := tx.toTickets([]ticketDoc{d})
	if err != nil {
		return ticket.Ticket{}, err
	}
	return ts[0], nil
}

func (tx *txn) getDoc(id ticket.ID) (ticketDoc, error) {
	var d ticketDoc
	err := tx.coll(cTickets).findOne(byID(id), &d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return d, fmt.Errorf("%w: ticket %s", ticket.ErrNotFound, id)
	}
	return d, err
}

func (tx *txn) getTicket(id ticket.ID) (ticket.Ticket, error) {
	d, err := tx.getDoc(id)
	if err != nil {
		return ticket.Ticket{}, err
	}
	return tx.asTicket(d)
}

// exists reports whether ticket id is there.
func (tx *txn) exists(id ticket.ID) (bool, error) {
	n, err := tx.coll(cTickets).count(byID(id))
	return n > 0, err
}

// appendEvent logs an event in the transaction, taking the next seq, so
// the change and its event commit together or not at all.
func (tx *txn) appendEvent(id ticket.ID, typ, actor string, data map[string]any) error {
	ev, err := tx.event(id, typ, actor, data)
	if err != nil {
		return err
	}
	return tx.coll(cEvents).insert(ev)
}

func (tx *txn) event(id ticket.ID, typ, actor string, data map[string]any) (eventDoc, error) {
	text, err := encodeData(data)
	if err != nil {
		return eventDoc{}, err
	}
	ev := eventDoc{Seq: tx.next(&tx.w.Events), TicketID: int64(id), Type: typ, Actor: actor, Data: text, CreatedAt: dt(tx.now)}
	if typ == ticket.EventLeaseRevived {
		ev.Token, _ = data["token"].(int64)
	}
	if sf, ok := data["split_from"].(ticket.ID); ok {
		ev.SplitFrom = int64(sf)
	}
	return ev, nil
}

// loadForWrite reads a ticket for a write. An expired lease on it is first
// freed and logged (expireLease), so no write clears or overwrites an
// expired lease without a lease_expired event.
func (tx *txn) loadForWrite(id ticket.ID) (ticket.Ticket, error) {
	t, err := tx.getTicket(id)
	if err != nil || t.Claim == nil || !t.Claim.Expired {
		return t, err
	}
	return tx.expireLease(t)
}

// expireLease frees t's expired lease, counts it and logs lease_expired with
// the holder that lost it. The store is the actor, not whoever noticed.
// Version doesn't change; the lease token is kept.
func (tx *txn) expireLease(t ticket.Ticket) (ticket.Ticket, error) {
	c := t.Claim
	res, err := tx.coll(cTickets).update(expiredFilter(t.ID, c.Holder, c.Token, tx.now), expireUpdate(tx.now))
	if err != nil {
		return t, err
	}
	if res.MatchedCount != 1 {
		return t, fmt.Errorf("%w: %s lease changed while expiring it", ticket.ErrVersionConflict, t.ID)
	}
	if err := tx.appendEvent(t.ID, ticket.EventLeaseExpired, ticket.SystemActor, expiredData(t)); err != nil {
		return t, err
	}
	t.Claim = nil
	t.LeaseExpiries++
	t.UpdatedAt = msTime(tx.now)
	return t, nil
}

func expiredFilter(id ticket.ID, holder string, token, now int64) bson.D {
	return bson.D{{Key: "_id", Value: int64(id)}, {Key: "lease.holder", Value: holder}, {Key: "token", Value: token},
		{Key: "lease.expires", Value: bson.D{{Key: "$lte", Value: dt(now)}}}}
}

func expireUpdate(now int64) bson.D {
	return bson.D{
		{Key: "$set", Value: bson.D{{Key: "lease", Value: nil}, {Key: "updated_at", Value: dt(now)}}},
		{Key: "$inc", Value: bson.D{{Key: "lease_expiries", Value: 1}}},
	}
}

func expiredData(t ticket.Ticket) map[string]any {
	c := t.Claim
	return map[string]any{"holder": c.Holder, "token": c.Token, "expired_at": c.ExpiresAt, "expiries": t.LeaseExpiries + 1}
}

// reapBatch caps how many leases one reap transaction frees, so a backlog of
// expired leases after an outage doesn't hold the writer for long.
const reapBatch = 100

// Reap implements ticket.Store. Each batch frees its leases in one bulk
// write and logs them in one insert, so a batch costs a few round trips
// however many leases it holds.
func (s *Store) Reap(ctx context.Context) (ticket.ReapResult, error) {
	res := ticket.ReapResult{Reaped: []ticket.ID{}}
	for {
		var batch []ticket.ID
		err := s.write(ctx, func(tx *txn) error {
			batch = nil // reset: a retry reruns this closure
			var docs []ticketDoc
			err := tx.coll(cTickets).find(bson.D{{Key: "lease.expires", Value: bson.D{{Key: "$lte", Value: dt(tx.now)}}}}, &docs,
				options.Find().SetSort(bson.D{{Key: "lease.expires", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(reapBatch).
					SetProjection(bson.D{{Key: "body", Value: 0}}))
			if err != nil || len(docs) == 0 {
				return err
			}
			models := make([]mongo.WriteModel, len(docs))
			events := make([]eventDoc, len(docs))
			ids := make([]ticket.ID, len(docs))
			for i, d := range docs {
				t := toTicket(d, tx.now, 0)
				models[i] = mongo.NewUpdateOneModel().SetFilter(expiredFilter(t.ID, t.Claim.Holder, t.Claim.Token, tx.now)).SetUpdate(expireUpdate(tx.now))
				if events[i], err = tx.event(t.ID, ticket.EventLeaseExpired, ticket.SystemActor, expiredData(t)); err != nil {
					return err
				}
				ids[i] = t.ID
			}
			r, err := tx.coll(cTickets).bulk(models)
			if err != nil {
				return err
			}
			if r.MatchedCount != int64(len(docs)) {
				return fmt.Errorf("%w: leases changed while reaping them", ticket.ErrVersionConflict)
			}
			if err := tx.coll(cEvents).insertMany(events); err != nil {
				return err
			}
			batch = ids
			return nil
		})
		if err != nil {
			return res, err
		}
		res.Reaped = append(res.Reaped, batch...)
		if len(batch) < reapBatch {
			return res, s.forgetKeys(ctx)
		}
	}
}

// keyBatch caps how many idempotency keys one transaction deletes, and
// keyBatches how many such transactions one reap runs: each key holds a
// whole result, so a backlog goes over many reaps, never one long
// transaction.
const (
	keyBatch   = 100
	keyBatches = 10
)

// forgetKeys deletes idempotency keys older than ticket.IdempotencyKeep,
// oldest first. The store's clock decides, not a TTL index on the
// server's, so both stores forget the same keys at the same time.
func (s *Store) forgetKeys(ctx context.Context) error {
	for range keyBatches {
		var n int64
		err := s.write(ctx, func(tx *txn) error {
			n = 0 // reset: a retry reruns this closure
			var docs []idemDoc
			err := tx.coll(cIdempotency).find(bson.D{{Key: "created_at", Value: bson.D{{Key: "$lt", Value: dt(tx.now - ticket.IdempotencyKeep.Milliseconds())}}}}, &docs,
				options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(keyBatch).SetProjection(bson.D{{Key: "_id", Value: 1}}))
			if err != nil || len(docs) == 0 {
				return err
			}
			keys := make([]string, len(docs))
			for i, d := range docs {
				keys[i] = d.Key
			}
			n, err = tx.coll(cIdempotency).deleteMany(bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: keys}}}})
			return err
		})
		if err != nil || n < keyBatch {
			return err
		}
	}
	return nil
}

// idempotent runs do once per key. A retry with the same op and request
// replays the stored result; a different request under the same key fails.
// When replay is set, it vets the stored result against the current state
// and returns what the caller gets instead, for results that can go stale.
func idempotent[T any](tx *txn, key, op string, req any, do func() (T, error), replay func(T) (T, error)) (T, error) {
	var zero T
	if key == "" {
		return do()
	}
	hash, err := ticket.RequestHash(op, req)
	if err != nil {
		return zero, err
	}
	var got idemDoc
	err = tx.coll(cIdempotency).findOne(bson.D{{Key: "_id", Value: key}}, &got)
	switch {
	case err == nil:
		if got.Op != op || got.RequestHash != hash {
			return zero, fmt.Errorf("%w: key %q was used for %s", ticket.ErrIdempotencyConflict, key, got.Op)
		}
		var out T
		if err := json.Unmarshal([]byte(got.Result), &out); err != nil {
			return zero, fmt.Errorf("decode stored result for key %q: %w", key, err)
		}
		if replay != nil {
			return replay(out)
		}
		return out, nil
	case !errors.Is(err, mongo.ErrNoDocuments):
		return zero, err
	}
	out, err := do()
	if err != nil {
		return zero, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return zero, err
	}
	err = tx.coll(cIdempotency).insert(idemDoc{Key: key, Op: op, RequestHash: hash, Result: string(b), CreatedAt: dt(tx.now)})
	return out, err
}

// Create implements ticket.Store.
func (s *Store) Create(ctx context.Context, req ticket.CreateRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *txn) error {
		var err error
		out, err = idempotent(tx, req.IdempotencyKey, "create", req, func() (ticket.Ticket, error) {
			blockedFrom := ""
			if req.Status == ticket.StatusBlocked {
				blockedFrom = string(ticket.StatusTodo)
			}
			// Check deps before taking an id, so a dep on the id this
			// ticket is about to get reads as missing, not as a cycle.
			for _, d := range req.DependsOn {
				ok, err := tx.exists(d)
				if err != nil {
					return ticket.Ticket{}, err
				}
				if !ok {
					return ticket.Ticket{}, fmt.Errorf("%w: dependency %s", ticket.ErrNotFound, d)
				}
			}
			tries, goal, tier := 0, req.Goal, req.Tier
			if req.SplitFrom != 0 {
				from, err := tx.getDoc(req.SplitFrom)
				if errors.Is(err, ticket.ErrNotFound) {
					return ticket.Ticket{}, fmt.Errorf("%w: split from %s", ticket.ErrNotFound, req.SplitFrom)
				}
				if err != nil {
					return ticket.Ticket{}, err
				}
				tries = from.Tries
				if goal == "" {
					goal = from.Goal // a split serves the goal its ticket served
				}
				if tier == "" {
					tier = from.Tier
				}
			}
			id := ticket.ID(tx.next(&tx.w.Tickets))
			deps, err := tx.addDeps(id, nil, req.DependsOn)
			if err != nil {
				return ticket.Ticket{}, err
			}
			doc := ticketDoc{
				ID: int64(id), Title: req.Title, Body: req.Body, Type: string(req.Type), Status: string(req.Status),
				Role: req.Role, Priority: *req.Priority, CreatedBy: req.Actor, Owner: req.Owner, Tries: tries,
				BlockedFrom: blockedFrom, Parked: string(req.Parked), Goal: goal, Tier: tier, Version: 1,
				Deps: deps, Areas: nonNil(req.Areas), CreatedAt: dt(tx.now), UpdatedAt: dt(tx.now),
			}
			if err := tx.coll(cTickets).insert(doc); err != nil {
				return ticket.Ticket{}, err
			}
			data := map[string]any{
				"title": req.Title, "type": req.Type, "status": req.Status, "role": req.Role,
				"priority": *req.Priority, "owner": req.Owner,
			}
			if len(req.DependsOn) > 0 {
				data["depends_on"] = req.DependsOn
			}
			if len(req.Areas) > 0 {
				data["areas"] = req.Areas
			}
			if req.SplitFrom != 0 {
				data["split_from"] = req.SplitFrom
				data["tries"] = tries
			}
			if req.Parked != "" {
				data["parked"] = req.Parked
			}
			if goal != "" {
				data["goal"] = goal
			}
			if tier != "" {
				data["tier"] = tier
			}
			if err := tx.appendEvent(id, ticket.EventCreated, req.Actor, data); err != nil {
				return ticket.Ticket{}, err
			}
			return toTicket(doc, tx.now, 0), nil
		}, func(prev ticket.Ticket) (ticket.Ticket, error) {
			// Return the ticket as it is now, not as it was made. A
			// cancelled one is not this work any more: refuse, so the
			// caller makes a new ticket instead of planning on a dead one.
			cur, err := tx.getTicket(prev.ID)
			if err != nil {
				return cur, err
			}
			if cur.Status == ticket.StatusCancelled {
				return ticket.Ticket{}, fmt.Errorf("%w: key %q made %s, which is now cancelled; retitle and use a new key",
					ticket.ErrIdempotencyConflict, req.IdempotencyKey, prev.ID)
			}
			return cur, nil
		})
		return err
	})
	return out, err
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// addDeps returns have with each of deps added, sorted. Every dependency
// must exist and not be cancelled, and none may already depend on id,
// directly or not: a cancelled dependency or a cycle would leave id
// waiting forever. The cycle check walks the graph up from each dep, one
// query per level.
func (tx *txn) addDeps(id ticket.ID, have []int64, deps []ticket.ID) ([]int64, error) {
	out := slices.Clone(have)
	for _, d := range deps {
		dep, err := tx.getDoc(d)
		if errors.Is(err, ticket.ErrNotFound) {
			return nil, fmt.Errorf("%w: dependency %s", ticket.ErrNotFound, d)
		}
		if err != nil {
			return nil, err
		}
		if ticket.Status(dep.Status) == ticket.StatusCancelled {
			return nil, fmt.Errorf("%w: dependency %s is cancelled and will never be done", ticket.ErrInvalid, d)
		}
		cycle, err := tx.reaches(dep, id)
		if err != nil {
			return nil, err
		}
		if cycle {
			return nil, fmt.Errorf("%w: %s already depends on %s; adding %s -> %s makes a cycle", ticket.ErrInvalid, d, id, id, d)
		}
		if !slices.Contains(out, int64(d)) {
			out = append(out, int64(d))
		}
	}
	slices.Sort(out)
	return nonNil(out), nil
}

// reaches reports whether from is target or depends on it, directly or
// not.
func (tx *txn) reaches(from ticketDoc, target ticket.ID) (bool, error) {
	seen := map[int64]bool{from.ID: true}
	if from.ID == int64(target) {
		return true, nil
	}
	frontier := from.Deps
	for len(frontier) > 0 {
		var next []int64
		for _, x := range frontier {
			if x == int64(target) {
				return true, nil
			}
			if !seen[x] {
				seen[x] = true
				next = append(next, x)
			}
		}
		if len(next) == 0 {
			break
		}
		var docs []ticketDoc
		err := tx.coll(cTickets).find(bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: next}}}}, &docs,
			options.Find().SetProjection(bson.D{{Key: "deps", Value: 1}}))
		if err != nil {
			return false, err
		}
		frontier = nil
		for _, d := range docs {
			frontier = append(frontier, d.Deps...)
		}
	}
	return false, nil
}

// Update implements ticket.Store. It doesn't change version: the claim
// rules read dependencies and areas live, and a holder's --expect-version
// shouldn't fail because someone added a dependency.
func (s *Store) Update(ctx context.Context, req ticket.UpdateRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *txn) error {
		var err error
		out, err = idempotent(tx, req.IdempotencyKey, "update", req, func() (ticket.Ticket, error) {
			t, err := tx.loadForWrite(req.ID)
			if err != nil {
				return t, err
			}
			if err := t.CheckFence(req.Holder, req.Token); err != nil {
				return t, err
			}
			tries, link, fromGoal := t.Tries, req.SplitFrom != 0, ""
			if link {
				var again bool
				if tries, fromGoal, again, err = tx.linkSplit(t, req.SplitFrom); err != nil {
					return t, err
				}
				// A retry of a link that went through changes nothing.
				link = !again
				if again && !req.ChangesBesidesLink() {
					return t, nil
				}
			}
			// A working claim already holds its areas: new ones must be free.
			if req.Areas != nil && t.LiveClaim() != nil && t.Status.WritesCode() {
				if err := tx.checkAreas(t.ID, *req.Areas); err != nil {
					return t, err
				}
			}
			var deps []int64
			for _, d := range t.DependsOn {
				if !slices.Contains(req.RemoveDeps, d) {
					deps = append(deps, int64(d))
				}
			}
			if deps, err = tx.addDeps(t.ID, deps, req.AddDeps); err != nil {
				return t, err
			}
			set := bson.D{{Key: "deps", Value: deps}}
			data := map[string]any{}
			if len(req.AddDeps) > 0 {
				data["add_deps"] = req.AddDeps
			}
			if len(req.RemoveDeps) > 0 {
				data["remove_deps"] = req.RemoveDeps
			}
			if req.Areas != nil {
				set = append(set, bson.E{Key: "areas", Value: nonNil(*req.Areas)})
				data["areas"] = *req.Areas
			}
			parked, goal, tier := t.Parked, t.Goal, t.Tier
			if req.Tier != nil {
				tier = *req.Tier
				data["tier"] = tier
			}
			if req.Parked != "" {
				if !ticket.ParkedSlot(t.Status, t.BlockedFrom) {
					return t, fmt.Errorf("%w: %s is %s; only a backlog ticket is parked", ticket.ErrInvalid, t.ID, t.Status)
				}
				parked = req.Parked
				data["parked"] = parked
			}
			if req.Goal != nil {
				goal = *req.Goal
				data["goal"] = goal
			} else if link && goal == "" && fromGoal != "" {
				goal = fromGoal // a re-file serves the goal its ticket served
				data["goal"] = goal
			}
			if link {
				data["split_from"] = req.SplitFrom
				data["tries"] = tries
			}
			set = append(set, bson.D{{Key: "parked", Value: string(parked)}, {Key: "goal", Value: goal}, {Key: "tier", Value: tier},
				{Key: "tries", Value: tries}, {Key: "updated_at", Value: dt(tx.now)}}...)
			var d ticketDoc
			if err := tx.coll(cTickets).findAndUpdate(byID(t.ID), bson.D{{Key: "$set", Value: set}}, &d); err != nil {
				return t, err
			}
			if err := tx.appendEvent(t.ID, ticket.EventUpdated, req.Actor, data); err != nil {
				return t, err
			}
			return tx.asTicket(d)
		}, nil)
		return err
	})
	return out, err
}

// Get implements ticket.Store.
func (s *Store) Get(ctx context.Context, id ticket.ID) (ticket.Detail, error) {
	var d ticket.Detail
	err := s.read(ctx, func(tx *txn) error {
		t, err := tx.getTicket(id)
		if err != nil {
			return err
		}
		var cs []commentDoc
		err = tx.coll(cComments).find(bson.D{{Key: "ticket_id", Value: int64(id)}}, &cs,
			options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
		if err != nil {
			return err
		}
		d = ticket.Detail{Ticket: t, Comments: make([]ticket.Comment, 0, len(cs))}
		for _, c := range cs {
			d.Comments = append(d.Comments, toComment(c))
		}
		return nil
	})
	return d, err
}

func toComment(c commentDoc) ticket.Comment {
	return ticket.Comment{ID: c.ID, TicketID: ticket.ID(c.TicketID), Author: c.Author, Body: c.Body, CreatedAt: msTime(int64(c.CreatedAt))}
}

var queueSort = bson.D{{Key: "priority", Value: 1}, {Key: "_id", Value: 1}}

// unclaimed matches tickets with no lease, or an expired one.
func unclaimed(now int64) bson.E {
	return bson.E{Key: "$or", Value: bson.A{
		bson.D{{Key: "lease", Value: nil}},
		bson.D{{Key: "lease.expires", Value: bson.D{{Key: "$lte", Value: dt(now)}}}},
	}}
}

func liveLease(now int64) bson.E {
	return bson.E{Key: "lease.expires", Value: bson.D{{Key: "$gt", Value: dt(now)}}}
}

func statusIn[S ~string](list []S) bson.E {
	a := bson.A{}
	for _, s := range list {
		a = append(a, string(s))
	}
	return bson.E{Key: "status", Value: bson.D{{Key: "$in", Value: a}}}
}

// List implements ticket.Store. Results are ordered by priority, then id.
func (s *Store) List(ctx context.Context, f ticket.ListFilter) ([]ticket.Ticket, error) {
	var out []ticket.Ticket
	err := s.read(ctx, func(tx *txn) error {
		out = []ticket.Ticket{} // reset: a retry reruns this closure
		// Each condition is its own document under $and, so two that test
		// one field (a holder and --claimed, --ready and --unclaimed) both
		// hold.
		var conds bson.A
		and := func(e ...bson.E) { conds = append(conds, bson.D(e)) }
		if len(f.Statuses) > 0 {
			and(statusIn(f.Statuses))
		}
		if f.Role != "" {
			and(bson.E{Key: "role", Value: f.Role})
		}
		if len(f.Parked) > 0 {
			a := bson.A{}
			for _, p := range f.Parked {
				a = append(a, string(p))
			}
			and(bson.E{Key: "parked", Value: bson.D{{Key: "$in", Value: a}}})
		}
		if f.Goal != "" {
			and(bson.E{Key: "goal", Value: f.Goal})
		}
		switch f.Tier {
		case "":
		case ticket.TierSenior:
			and(bson.E{Key: "tier", Value: bson.D{{Key: "$in", Value: bson.A{"", ticket.TierSenior}}}})
		default:
			and(bson.E{Key: "tier", Value: f.Tier})
		}
		if f.Holder != "" {
			and(bson.E{Key: "lease.holder", Value: f.Holder}, liveLease(tx.now))
		}
		if f.Claimed != nil {
			if *f.Claimed {
				and(liveLease(tx.now))
			} else {
				and(unclaimed(tx.now))
			}
		}
		var busy areaSet
		if f.Ready {
			and(unclaimed(tx.now))
			var err error
			if busy, err = tx.busyAreas(0); err != nil {
				return err
			}
		}
		filter := bson.D{}
		if len(conds) > 0 {
			filter = bson.D{{Key: "$and", Value: conds}}
		}
		// Filters run in Go after the query: the limit counts after them.
		inGo := f.Ready || f.LatestPrefix != "" || f.NotLatestPrefix != ""
		opts := options.Find().SetSort(queueSort)
		if f.Limit > 0 && !inGo {
			opts.SetLimit(int64(f.Limit))
		}
		var docs []ticketDoc
		if err := tx.coll(cTickets).find(filter, &docs, opts); err != nil {
			return err
		}
		if f.Ready {
			var err error
			if docs, err = tx.startable(docs, busy); err != nil {
				return err
			}
		}
		var keep []ticketDoc
		for _, d := range docs {
			if f.Limit > 0 && len(keep) == f.Limit {
				break
			}
			if f.LatestPrefix != "" {
				ok, err := tx.latestHas(d, f.LatestPrefix)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
			}
			if f.NotLatestPrefix != "" {
				ok, err := tx.latestHas(d, f.NotLatestPrefix)
				if err != nil {
					return err
				}
				if ok {
					continue
				}
			}
			keep = append(keep, d)
		}
		ts, err := tx.toTickets(keep)
		if err != nil {
			return err
		}
		out = append(out, ts...)
		return nil
	})
	return out, err
}

// latestHas reports whether d's latest comment starts with prefix; false
// with no comment. It reads the comment only when the prefix is longer
// than what the ticket keeps of it.
func (tx *txn) latestHas(d ticketDoc, prefix string) (bool, error) {
	if d.Latest == nil {
		return false, nil
	}
	if !d.LatestCut || len(prefix) <= len(*d.Latest) {
		return len(*d.Latest) >= len(prefix) && (*d.Latest)[:len(prefix)] == prefix, nil
	}
	var c commentDoc
	err := tx.coll(cComments).findOne(bson.D{{Key: "ticket_id", Value: d.ID}}, &c,
		options.FindOne().SetSort(bson.D{{Key: "_id", Value: -1}}))
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(c.Body) >= len(prefix) && c.Body[:len(prefix)] == prefix, nil
}

// latestOf is what a ticket keeps of its latest comment, body.
func latestOf(body string) (string, bool) {
	n := 0
	for i := range body {
		if n == latestKeep {
			return body[:i], true
		}
		n++
	}
	return body, false
}

// startable keeps the docs a claim could start now, as ClaimNext picks
// them: in todo or in_progress, every dep done and no area busy. The
// caller has already kept only unclaimed ones.
func (tx *txn) startable(docs []ticketDoc, busy areaSet) ([]ticketDoc, error) {
	pending, err := tx.notDone(docs)
	if err != nil {
		return nil, err
	}
	var out []ticketDoc
	for _, d := range docs {
		if !ticket.Status(d.Status).WritesCode() {
			out = append(out, d)
			continue
		}
		if slices.ContainsFunc(d.Deps, func(x int64) bool { return pending[x] }) {
			continue
		}
		if _, _, overlaps := ticket.AnyAreaOverlaps(d.Areas, busy.areas); overlaps {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// notDone returns which deps of docs in todo or in_progress are not done.
// A dep that isn't there doesn't count, as SQLite's join drops it.
func (tx *txn) notDone(docs []ticketDoc) (map[int64]bool, error) {
	var ids []int64
	for _, d := range docs {
		if ticket.Status(d.Status).WritesCode() {
			ids = append(ids, d.Deps...)
		}
	}
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var deps []ticketDoc
	err := tx.coll(cTickets).find(bson.D{
		{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}},
		{Key: "status", Value: bson.D{{Key: "$ne", Value: string(ticket.StatusDone)}}},
	}, &deps, options.Find().SetProjection(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	for _, d := range deps {
		out[d.ID] = true
	}
	return out, nil
}

// ClaimNext implements ticket.Store.
func (s *Store) ClaimNext(ctx context.Context, req ticket.ClaimRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	// Before the transaction too, so a claim under STOP fails at once
	// rather than wait on the writer and come back busy.
	if err := s.checkStop(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *txn) error {
		// Holding the writer: a claim that waited on it while STOP
		// appeared, or the halt was set, must not land.
		if err := s.checkStop(); err != nil {
			return err
		}
		if err := tx.halted(); err != nil {
			return err
		}
		var err error
		out, err = idempotent(tx, req.IdempotencyKey, "claim", req, func() (ticket.Ticket, error) {
			t, err := tx.pickClaimable(req)
			if err != nil {
				return t, err
			}
			return tx.applyClaim(t, req)
		}, func(prev ticket.Ticket) (ticket.Ticket, error) {
			// A replayed claim is only true while that lease is still live.
			cur, err := tx.getTicket(prev.ID)
			if err != nil {
				return cur, err
			}
			c := cur.LiveClaim()
			if prev.Claim == nil || c == nil || c.Holder != req.Holder || c.Token != prev.Claim.Token {
				return ticket.Ticket{}, fmt.Errorf("%w: claim %q of %s by %s is no longer live; claim again with a new key",
					ticket.ErrLeaseLost, req.IdempotencyKey, prev.ID, req.Holder)
			}
			return cur, nil
		})
		return err
	})
	return out, err
}

// pickBatch is how many queued tickets a claim reads at a time.
const pickBatch = 100

func (tx *txn) pickClaimable(req ticket.ClaimRequest) (ticket.Ticket, error) {
	if req.ID != 0 {
		t, err := tx.getTicket(req.ID)
		if err != nil {
			return t, err
		}
		if t.Status.Terminal() {
			return t, fmt.Errorf("%w: %s is %s", ticket.ErrStatusConflict, t.ID, t.Status)
		}
		if len(req.Statuses) > 0 && !slices.Contains(req.Statuses, t.Status) {
			return t, fmt.Errorf("%w: %s is %s", ticket.ErrStatusConflict, t.ID, t.Status)
		}
		if req.Role != "" && t.Role != req.Role {
			return t, fmt.Errorf("%w: %s is for role %q", ticket.ErrStatusConflict, t.ID, t.Role)
		}
		c := t.LiveClaim()
		if c != nil && c.Holder != req.Holder {
			return t, fmt.Errorf("%w: %s is held by %s until %s", ticket.ErrClaimed, t.ID, c.Holder, c.ExpiresAt.Format(time.RFC3339))
		}
		// Grooming, review and answering an escalation change
		// no code, and renewing a lease you hold starts nothing new.
		if !t.Status.WritesCode() || c != nil {
			return t, nil
		}
		return t, tx.checkStart(t)
	}
	busy, err := tx.busyAreas(0)
	if err != nil {
		return ticket.Ticket{}, err
	}
	filter := bson.D{statusIn(req.Statuses), unclaimed(tx.now)}
	if req.Role != "" {
		filter = append(filter, bson.E{Key: "role", Value: req.Role})
	}
	// No body: the claim's update returns the whole ticket.
	cur, err := tx.coll(cTickets).cursor(filter, options.Find().SetSort(queueSort).SetBatchSize(pickBatch).
		SetProjection(bson.D{{Key: "body", Value: 0}}))
	if err != nil {
		return ticket.Ticket{}, err
	}
	defer cur.Close(tx.ctx)
	for {
		var docs []ticketDoc
		for len(docs) < pickBatch && cur.Next(tx.ctx) {
			var d ticketDoc
			if err := cur.Decode(&d); err != nil {
				return ticket.Ticket{}, err
			}
			docs = append(docs, d)
		}
		if err := cur.Err(); err != nil {
			return ticket.Ticket{}, err
		}
		if len(docs) == 0 {
			return ticket.Ticket{}, ticket.ErrNoWork
		}
		ok, err := tx.startable(docs, busy)
		if err != nil {
			return ticket.Ticket{}, err
		}
		if len(ok) > 0 {
			return tx.asTicket(ok[0])
		}
	}
}

// checkStart refuses work on t while a dependency isn't done or one of its
// areas overlaps a live claim's.
func (tx *txn) checkStart(t ticket.Ticket) error {
	pending, err := tx.pendingDeps(t)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("%w: %s waits on %v, not done yet", ticket.ErrWaitingOnDeps, t.ID, pending)
	}
	return tx.checkAreas(t.ID, t.Areas)
}

// checkAreas refuses areas that overlap those of a live claim on another
// ticket.
func (tx *txn) checkAreas(id ticket.ID, areas []string) error {
	busy, err := tx.busyAreas(id)
	if err != nil {
		return err
	}
	if mine, theirs, ok := ticket.AnyAreaOverlaps(areas, busy.areas); ok {
		return fmt.Errorf("%w: %s area %s overlaps %s on %s, which is claimed", ticket.ErrAreaBusy, id, mine, theirs, busy.owner[theirs])
	}
	return nil
}

// pendingDeps lists the tickets t depends on that aren't done. Only done
// counts: a cancelled dependency keeps t waiting until someone removes it.
func (tx *txn) pendingDeps(t ticket.Ticket) ([]ticket.ID, error) {
	if len(t.DependsOn) == 0 {
		return nil, nil
	}
	ids := make([]int64, len(t.DependsOn))
	for i, d := range t.DependsOn {
		ids[i] = int64(d)
	}
	var docs []ticketDoc
	err := tx.coll(cTickets).find(bson.D{
		{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}},
		{Key: "status", Value: bson.D{{Key: "$ne", Value: string(ticket.StatusDone)}}},
	}, &docs, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetProjection(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var out []ticket.ID
	for _, d := range docs {
		out = append(out, ticket.ID(d.ID))
	}
	return out, nil
}

// openDependents lists the tickets not yet done or cancelled that depend on id.
func (tx *txn) openDependents(id ticket.ID) ([]ticket.ID, error) {
	var docs []ticketDoc
	err := tx.coll(cTickets).find(bson.D{
		{Key: "deps", Value: int64(id)},
		{Key: "status", Value: bson.D{{Key: "$nin", Value: bson.A{string(ticket.StatusDone), string(ticket.StatusCancelled)}}}},
	}, &docs, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetProjection(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var out []ticket.ID
	for _, d := range docs {
		out = append(out, ticket.ID(d.ID))
	}
	return out, nil
}

type areaSet struct {
	areas []string
	owner map[string]ticket.ID
}

// busyAreas lists the areas of tickets under a live claim in todo or
// in_progress, other than except. Claims in other statuses change no code,
// so they don't count.
func (tx *txn) busyAreas(except ticket.ID) (areaSet, error) {
	set := areaSet{owner: map[string]ticket.ID{}}
	var docs []ticketDoc
	err := tx.coll(cTickets).find(bson.D{
		liveLease(tx.now),
		statusIn([]ticket.Status{ticket.StatusTodo, ticket.StatusInProgress}),
		{Key: "_id", Value: bson.D{{Key: "$ne", Value: int64(except)}}},
		{Key: "areas.0", Value: bson.D{{Key: "$exists", Value: true}}},
	}, &docs, options.Find().SetProjection(bson.D{{Key: "areas", Value: 1}}))
	if err != nil {
		return set, err
	}
	for _, d := range docs {
		for _, a := range d.Areas {
			set.areas = append(set.areas, a)
			set.owner[a] = ticket.ID(d.ID)
		}
	}
	return set, nil
}

// applyClaim leases t to req.Holder. Every claim, including a holder
// re-claiming its own live lease, takes the next lease token and bumps the
// version, so writes carrying an older token are fenced out.
func (tx *txn) applyClaim(t ticket.Ticket, req ticket.ClaimRequest) (ticket.Ticket, error) {
	expires := tx.now + req.TTL.Milliseconds()
	if t.Claim != nil && t.Claim.Expired {
		var err error
		if t, err = tx.expireLease(t); err != nil {
			return t, err
		}
	}
	renewed := t.Claim != nil && t.Claim.Holder == req.Holder
	var d ticketDoc
	err := tx.coll(cTickets).findAndUpdate(bson.D{{Key: "_id", Value: int64(t.ID)}, {Key: "version", Value: t.Version}}, bson.D{
		{Key: "$set", Value: bson.D{{Key: "lease", Value: leaseDoc{Holder: req.Holder, Expires: dt(expires)}}, {Key: "updated_at", Value: dt(tx.now)}}},
		{Key: "$inc", Value: bson.D{{Key: "token", Value: int64(1)}, {Key: "version", Value: int64(1)}}},
	}, &d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return t, fmt.Errorf("%w: %s changed during claim", ticket.ErrVersionConflict, t.ID)
	}
	if err != nil {
		return t, err
	}
	// A new token has no revivals yet.
	got := toTicket(d, tx.now, 0)
	data := map[string]any{
		"holder": req.Holder, "token": got.Claim.Token, "expires_at": msTime(expires), "status": t.Status,
	}
	if renewed {
		data["renewed"] = true
	}
	if err := tx.appendEvent(t.ID, ticket.EventClaimed, req.Actor, data); err != nil {
		return got, err
	}
	return got, nil
}

// Heartbeat implements ticket.Store. It fails with ErrLeaseLost unless the
// lease is the caller's, with the caller's token. A lease that ran out with
// nothing written since comes back to life (lease_revived), unless STOP is
// on or another claim took one of its areas meanwhile.
func (s *Store) Heartbeat(ctx context.Context, req ticket.HeartbeatRequest) (ticket.Ticket, error) {
	t, _, err := s.HeartbeatHalt(ctx, req)
	return t, err
}

// HeartbeatHalt is Heartbeat, and reports the halt as its transaction
// read it, so a keepalive learns of stop --all at no extra round trip.
func (s *Store) HeartbeatHalt(ctx context.Context, req ticket.HeartbeatRequest) (ticket.Ticket, bool, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, false, err
	}
	var out ticket.Ticket
	var halted bool
	err := s.write(ctx, func(tx *txn) error {
		halted = tx.w != nil && tx.w.Halt
		t, err := tx.getTicket(req.ID)
		if err != nil {
			return err
		}
		c := t.Claim
		if c == nil || c.Holder != req.Holder || c.Token != req.Token {
			return fmt.Errorf("%w: %s has no live lease by %s with token %d", ticket.ErrLeaseLost, t.ID, req.Holder, req.Token)
		}
		if c.Expired {
			if err := s.checkStop(); err != nil {
				return fmt.Errorf("%w: %s lease ran out, and %w", ticket.ErrLeaseLost, t.ID, err)
			}
			if err := tx.halted(); err != nil {
				return fmt.Errorf("%w: %s lease ran out, and %w", ticket.ErrLeaseLost, t.ID, err)
			}
		}
		if c.Expired && t.Status.WritesCode() {
			if err := tx.checkAreas(t.ID, t.Areas); err != nil {
				return fmt.Errorf("%w: %s lease ran out, and meanwhile %w", ticket.ErrLeaseLost, t.ID, err)
			}
		}
		// Guard on the token too, so the extension can only land on the
		// lease that was checked. $max: a heartbeat never cuts a lease
		// short, so one with the default TTL can't take time from a
		// longer claim.
		res, err := tx.coll(cTickets).update(bson.D{
			{Key: "_id", Value: int64(t.ID)}, {Key: "lease.holder", Value: req.Holder}, {Key: "token", Value: req.Token},
		}, bson.D{{Key: "$max", Value: bson.D{{Key: "lease.expires", Value: dt(tx.now + req.TTL.Milliseconds())}}}})
		if err != nil {
			return err
		}
		if res.MatchedCount != 1 {
			return fmt.Errorf("%w: %s lease changed during heartbeat", ticket.ErrLeaseLost, t.ID)
		}
		if c.Expired {
			err := tx.appendEvent(t.ID, ticket.EventLeaseRevived, ticket.SystemActor, map[string]any{
				"holder": c.Holder, "token": c.Token, "expired_at": c.ExpiresAt,
			})
			if err != nil {
				return err
			}
		}
		out, err = tx.getTicket(t.ID)
		return err
	})
	return out, halted, err
}

// Release implements ticket.Store.
func (s *Store) Release(ctx context.Context, req ticket.ReleaseRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *txn) error {
		var err error
		out, err = idempotent(tx, req.IdempotencyKey, "release", req, func() (ticket.Ticket, error) {
			// An expired lease is freed and logged as lease_expired first,
			// even our own: it ran out, it wasn't released.
			t, err := tx.loadForWrite(req.ID)
			if err != nil {
				return t, err
			}
			switch {
			case t.Claim == nil:
				return t, nil // already gone: nothing of ours to release
			case t.Claim.Holder != req.Holder:
				return t, fmt.Errorf("%w: %s is held by %s", ticket.ErrClaimed, t.ID, t.Claim.Holder)
			case req.Token != t.Claim.Token:
				return t, fmt.Errorf("%w: %s lease token is %d, not %d; it was re-claimed", ticket.ErrLeaseLost, t.ID, t.Claim.Token, req.Token)
			}
			data := map[string]any{"holder": req.Holder, "token": req.Token}
			expiries := 0
			if req.Died {
				data["died"] = true
				data["expiries"] = t.LeaseExpiries + 1
				expiries = 1
			}
			var d ticketDoc
			err = tx.coll(cTickets).findAndUpdate(bson.D{{Key: "_id", Value: int64(t.ID)}, {Key: "version", Value: t.Version}}, bson.D{
				{Key: "$set", Value: bson.D{{Key: "lease", Value: nil}, {Key: "updated_at", Value: dt(tx.now)}}},
				{Key: "$inc", Value: bson.D{{Key: "lease_expiries", Value: expiries}, {Key: "version", Value: int64(1)}}},
			}, &d)
			if errors.Is(err, mongo.ErrNoDocuments) {
				return t, fmt.Errorf("%w: %s changed during release", ticket.ErrVersionConflict, t.ID)
			}
			if err != nil {
				return t, err
			}
			if err := tx.appendEvent(t.ID, ticket.EventReleased, req.Actor, data); err != nil {
				return t, err
			}
			return toTicket(d, tx.now, 0), nil
		}, nil)
		return err
	})
	return out, err
}

// Transition implements ticket.Store.
func (s *Store) Transition(ctx context.Context, req ticket.TransitionRequest) (ticket.Ticket, error) {
	if err := req.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	var out ticket.Ticket
	err := s.write(ctx, func(tx *txn) error {
		var err error
		out, err = idempotent(tx, req.IdempotencyKey, "transition", req.Keyed(), func() (ticket.Ticket, error) {
			return tx.applyTransition(req)
		}, nil)
		return err
	})
	return out, err
}

func (tx *txn) applyTransition(req ticket.TransitionRequest) (ticket.Ticket, error) {
	t, err := tx.loadForWrite(req.ID)
	if err != nil {
		return t, err
	}
	if req.From != "" && t.Status != req.From {
		return t, fmt.Errorf("%w: %s is %s, not %s", ticket.ErrStatusConflict, t.ID, t.Status, req.From)
	}
	if req.ExpectVersion != 0 && t.Version != req.ExpectVersion {
		return t, fmt.Errorf("%w: %s is at version %d, not %d", ticket.ErrVersionConflict, t.ID, t.Version, req.ExpectVersion)
	}
	if err := t.CheckFence(req.Holder, req.Token); err != nil {
		return t, err
	}
	if !t.CanMove(req.To) {
		return t, fmt.Errorf("%w: %s ticket %s -> %s (allowed: %v)", ticket.ErrInvalidTransition, t.Type, t.Status, req.To, t.NextStatuses())
	}
	if err := t.CheckGate(req.To, req.Actor, req.ActorRole); err != nil {
		return t, err
	}
	// A cancelled ticket is never done, so an open ticket that depends on
	// it would wait forever: its dependents move off it first.
	if req.To == ticket.StatusCancelled {
		dependents, err := tx.openDependents(t.ID)
		if err != nil {
			return t, err
		}
		if len(dependents) > 0 {
			return t, fmt.Errorf("%w: %v depend on %s; remove the dep or cancel them first", ticket.ErrHasDependents, dependents, t.ID)
		}
	}
	// A held ticket moving into work holds its areas from now on: check
	// what its claim in another status skipped.
	if t.LiveClaim() != nil && !req.Release && !t.Status.WritesCode() && req.To.WritesCode() {
		if err := tx.checkStart(t); err != nil {
			return t, err
		}
	}
	// Claimed or not, nothing starts work before its deps, even one added
	// after the claim. Done never waits on them.
	if t.Status == ticket.StatusTodo && req.To == ticket.StatusInProgress {
		pending, err := tx.pendingDeps(t)
		if err != nil {
			return t, err
		}
		if len(pending) > 0 {
			return t, fmt.Errorf("%w: %s waits on %v, not done yet", ticket.ErrWaitingOnDeps, t.ID, pending)
		}
	}
	role := t.Role
	if req.Role != "" {
		role = req.Role
	}
	blockedFrom := "" // cleared on every move except into blocked
	if req.To == ticket.StatusBlocked {
		blockedFrom = string(t.Status)
	}
	// Kept while the ticket is parked or blocked from there; a move into
	// backlog names its reason, except back from blocked, which keeps the
	// old one unless given another.
	var parked ticket.Parked
	switch {
	case req.To == ticket.StatusBacklog:
		if parked = req.Parked; parked == "" {
			parked = t.Parked
		}
		if parked == "" {
			return t, ticket.ErrParkedMissing()
		}
	case req.To == ticket.StatusBlocked && t.Status == ticket.StatusBacklog:
		parked = t.Parked
	}
	tries := t.Tries
	if t.Type == ticket.TypeShip && ticket.IsSendBack(t.Status, req.To) {
		tries++
	}
	release := t.Claim != nil && (req.Release || req.To.Terminal())
	set := bson.D{
		{Key: "status", Value: string(req.To)}, {Key: "role", Value: role}, {Key: "blocked_from", Value: blockedFrom},
		{Key: "parked", Value: string(parked)}, {Key: "tries", Value: tries}, {Key: "updated_at", Value: dt(tx.now)},
	}
	if release {
		set = append(set, bson.E{Key: "lease", Value: nil})
	}
	var d ticketDoc
	err = tx.coll(cTickets).findAndUpdate(bson.D{{Key: "_id", Value: int64(t.ID)}, {Key: "version", Value: t.Version}},
		bson.D{{Key: "$set", Value: set}, {Key: "$inc", Value: bson.D{{Key: "version", Value: int64(1)}}}}, &d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return t, fmt.Errorf("%w: %s changed during transition", ticket.ErrVersionConflict, t.ID)
	}
	if err != nil {
		return t, err
	}
	data := map[string]any{"from": t.Status, "to": req.To}
	if req.Note != "" {
		data["note"] = req.Note
	}
	if parked != "" && parked != t.Parked {
		data["parked"] = parked
	}
	if role != t.Role {
		data["role"] = role
	}
	if req.ActorRole != "" {
		data["actor_role"] = req.ActorRole
	}
	if tries != t.Tries {
		data["tries"] = tries
	}
	if release {
		data["released"] = t.Claim.Holder
	}
	if err := tx.appendEvent(t.ID, ticket.EventTransitioned, req.Actor, data); err != nil {
		return t, err
	}
	return tx.asTicket(d)
}

// Comment implements ticket.Store. The ticket keeps the comment's start,
// for list's --latest-prefix; its version and updated time stay.
func (s *Store) Comment(ctx context.Context, req ticket.CommentRequest) (ticket.Comment, error) {
	if err := req.Validate(); err != nil {
		return ticket.Comment{}, err
	}
	var out ticket.Comment
	err := s.write(ctx, func(tx *txn) error {
		var err error
		out, err = idempotent(tx, req.IdempotencyKey, "comment", req, func() (ticket.Comment, error) {
			t, err := tx.loadForWrite(req.ID)
			if err != nil {
				return ticket.Comment{}, err
			}
			if err := t.CheckFence(req.Holder, req.Token); err != nil {
				return ticket.Comment{}, err
			}
			c := commentDoc{ID: tx.next(&tx.w.Comments), TicketID: int64(req.ID), Author: req.Actor, Body: req.Body, CreatedAt: dt(tx.now)}
			if err := tx.coll(cComments).insert(c); err != nil {
				return ticket.Comment{}, err
			}
			latest, cut := latestOf(req.Body)
			_, err = tx.coll(cTickets).update(byID(req.ID), bson.D{{Key: "$set", Value: bson.D{
				{Key: "latest", Value: latest}, {Key: "latest_cut", Value: cut},
			}}})
			if err != nil {
				return ticket.Comment{}, err
			}
			if err := tx.appendEvent(req.ID, ticket.EventCommented, req.Actor, map[string]any{"comment_id": c.ID}); err != nil {
				return ticket.Comment{}, err
			}
			return toComment(c), nil
		}, nil)
		return err
	})
	return out, err
}

func toEvent(e eventDoc) ticket.Event {
	return ticket.Event{Seq: e.Seq, TicketID: ticket.ID(e.TicketID), Type: e.Type, Actor: e.Actor,
		Data: json.RawMessage(e.Data), CreatedAt: msTime(int64(e.CreatedAt))}
}

// Events implements ticket.Store.
func (s *Store) Events(ctx context.Context, f ticket.EventFilter) ([]ticket.Event, error) {
	var out []ticket.Event
	err := s.read(ctx, func(tx *txn) error {
		out = []ticket.Event{} // reset: a retry reruns this closure
		filter := bson.D{{Key: "_id", Value: bson.D{{Key: "$gt", Value: f.Since}}}}
		if f.TicketID != 0 {
			filter = append(filter, bson.E{Key: "ticket_id", Value: int64(f.TicketID)})
		}
		if f.Type != "" {
			filter = append(filter, bson.E{Key: "type", Value: f.Type})
		}
		opts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})
		if f.Limit > 0 {
			opts.SetLimit(int64(f.Limit))
		}
		var evs []eventDoc
		if err := tx.coll(cEvents).find(filter, &evs, opts); err != nil {
			return err
		}
		for _, e := range evs {
			out = append(out, toEvent(e))
		}
		return nil
	})
	return out, err
}

// LastSeq implements ticket.Store.
func (s *Store) LastSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := s.read(ctx, func(tx *txn) error {
		var e eventDoc
		err := tx.coll(cEvents).findOne(bson.D{}, &e, options.FindOne().SetSort(bson.D{{Key: "_id", Value: -1}}).
			SetProjection(bson.D{{Key: "_id", Value: 1}}))
		if errors.Is(err, mongo.ErrNoDocuments) {
			seq = 0
			return nil
		}
		seq = e.Seq
		return err
	})
	return seq, err
}

// splitParents maps each ticket made or linked with --split-from to the
// ticket it splits or rewrites.
func (tx *txn) splitParents() (map[ticket.ID]ticket.ID, error) {
	var evs []eventDoc
	err := tx.coll(cEvents).find(bson.D{
		{Key: "type", Value: bson.D{{Key: "$in", Value: bson.A{ticket.EventCreated, ticket.EventUpdated}}}},
		{Key: "split_from", Value: bson.D{{Key: "$gt", Value: int64(0)}}},
	}, &evs, options.Find().SetProjection(bson.D{{Key: "ticket_id", Value: 1}, {Key: "split_from", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := map[ticket.ID]ticket.ID{}
	for _, e := range evs {
		out[ticket.ID(e.TicketID)] = ticket.ID(e.SplitFrom)
	}
	return out, nil
}

// linkSplit checks that t may be linked to from, the cancelled ticket it
// replaces, and returns t's tries with from's added and from's goal; or,
// when t is linked to from already, as a retry finds it, again true.
func (tx *txn) linkSplit(t ticket.Ticket, from ticket.ID) (tries int, goal string, again bool, err error) {
	parents, err := tx.splitParents()
	if err != nil {
		return 0, "", false, err
	}
	if p, ok := parents[t.ID]; ok {
		if p == from {
			return t.Tries, "", true, nil
		}
		return 0, "", false, fmt.Errorf("%w: %s is already split from %s", ticket.ErrInvalid, t.ID, p)
	}
	f, err := tx.getDoc(from)
	if errors.Is(err, ticket.ErrNotFound) {
		return 0, "", false, fmt.Errorf("%w: split from %s", ticket.ErrNotFound, from)
	}
	if err != nil {
		return 0, "", false, err
	}
	if ticket.Status(f.Status) != ticket.StatusCancelled {
		return 0, "", false, fmt.Errorf("%w: %s is %s; link only a cancelled ticket to the one that replaced it (to split an open ticket, create the new ones with --split-from)",
			ticket.ErrInvalid, from, f.Status)
	}
	// A second replacement would take the first one's runs away from it,
	// and count from's tries twice.
	var kid ticket.ID
	for k, p := range parents {
		if p == from && (kid == 0 || k < kid) {
			kid = k
		}
	}
	if kid != 0 {
		return 0, "", false, fmt.Errorf("%w: %s already has %s split from it", ticket.ErrInvalid, from, kid)
	}
	for p, seen := from, map[ticket.ID]bool{}; p != 0 && !seen[p]; p = parents[p] {
		seen[p] = true
		if p == t.ID {
			return 0, "", false, fmt.Errorf("%w: %s comes from %s, so %s can't be split from it", ticket.ErrInvalid, from, t.ID, t.ID)
		}
	}
	return t.Tries + f.Tries, f.Goal, false, nil
}
