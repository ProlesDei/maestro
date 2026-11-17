package mongostore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// runDoc is one session's spend. TicketID is nil for none.
type runDoc struct {
	Session          string        `bson:"_id"`
	TicketID         *int64        `bson:"ticket_id"`
	Actor            string        `bson:"actor"`
	CostUSD          float64       `bson:"cost_usd"`
	InputTokens      int64         `bson:"input_tokens"`
	OutputTokens     int64         `bson:"output_tokens"`
	CacheWriteTokens int64         `bson:"cache_write_tokens"`
	CacheReadTokens  int64         `bson:"cache_read_tokens"`
	CreatedAt        bson.DateTime `bson:"created_at"`
	UpdatedAt        bson.DateTime `bson:"updated_at"`
}

func toRun(d runDoc) ticket.Run {
	r := ticket.Run{Session: d.Session, Actor: d.Actor, CostUSD: d.CostUSD, InputTokens: d.InputTokens,
		OutputTokens: d.OutputTokens, CacheWriteTokens: d.CacheWriteTokens, CacheReadTokens: d.CacheReadTokens,
		CreatedAt: msTime(int64(d.CreatedAt)), UpdatedAt: msTime(int64(d.UpdatedAt))}
	if d.TicketID != nil {
		r.Ticket = ticket.ID(*d.TicketID)
	}
	return r
}

type launchDoc struct {
	ID        int64          `bson:"_id"`
	Actor     string         `bson:"actor"`
	TicketID  *int64         `bson:"ticket_id"`
	PID       int            `bson:"pid"`
	Stamp     string         `bson:"stamp"`
	StartedAt bson.DateTime  `bson:"started_at"`
	EndedAt   *bson.DateTime `bson:"ended_at"`
	Outcome   string         `bson:"outcome"`
	Session   string         `bson:"session"`
}

func toLaunch(d launchDoc) ticket.Launch {
	l := ticket.Launch{ID: d.ID, Actor: d.Actor, PID: d.PID, Stamp: d.Stamp, StartedAt: msTime(int64(d.StartedAt)),
		Outcome: d.Outcome, Session: d.Session}
	if d.TicketID != nil {
		l.Ticket = ticket.ID(*d.TicketID)
	}
	if d.EndedAt != nil {
		l.EndedAt = msTime(int64(*d.EndedAt))
	}
	return l
}

type etaDoc struct {
	Goal    string         `bson:"_id"`
	DueAt   bson.DateTime  `bson:"due_at"`
	Tickets []int64        `bson:"tickets"`
	SetBy   string         `bson:"set_by"`
	SetAt   bson.DateTime  `bson:"set_at"`
	MetAt   *bson.DateTime `bson:"met_at"`
}

func toETA(d etaDoc) ticket.ETA {
	e := ticket.ETA{Goal: d.Goal, Due: msTime(int64(d.DueAt)), Tickets: []ticket.ID{}, SetBy: d.SetBy, SetAt: msTime(int64(d.SetAt))}
	for _, id := range d.Tickets {
		e.Tickets = append(e.Tickets, ticket.ID(id))
	}
	if d.MetAt != nil {
		e.MetAt = msTime(int64(*d.MetAt))
	}
	return e
}

// ticketRef checks ticket id is there and returns it for a run or launch
// document; nil for none.
func (tx *txn) ticketRef(id ticket.ID) (*int64, error) {
	if id == 0 {
		return nil, nil
	}
	ok, err := tx.exists(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", ticket.ErrNotFound, id)
	}
	n := int64(id)
	return &n, nil
}

// LogRun implements ticket.Store.
func (s *Store) LogRun(ctx context.Context, req ticket.LogRunRequest) (ticket.Run, error) {
	if err := req.Validate(); err != nil {
		return ticket.Run{}, err
	}
	var out ticket.Run
	err := s.write(ctx, func(tx *txn) error {
		tid, err := tx.ticketRef(req.Ticket)
		if err != nil {
			return err
		}
		runs := tx.coll(cRuns)
		var cur runDoc
		err = runs.findOne(bson.D{{Key: "_id", Value: req.Session}}, &cur)
		switch {
		case errors.Is(err, mongo.ErrNoDocuments):
			cur = runDoc{Session: req.Session, TicketID: tid, Actor: req.Actor, CostUSD: req.CostUSD,
				InputTokens: req.InputTokens, OutputTokens: req.OutputTokens, CacheWriteTokens: req.CacheWriteTokens,
				CacheReadTokens: req.CacheReadTokens, CreatedAt: dt(tx.now), UpdatedAt: dt(tx.now)}
			if err := runs.insert(cur); err != nil {
				return err
			}
		case err != nil:
			return err
		case req.CostUSD >= cur.CostUSD:
			// A session's total only grows, so a lower cost is an older
			// reply arriving late: keep what is there.
			if tid != nil {
				cur.TicketID = tid
			}
			cur.CostUSD, cur.InputTokens, cur.OutputTokens = req.CostUSD, req.InputTokens, req.OutputTokens
			cur.CacheWriteTokens, cur.CacheReadTokens, cur.UpdatedAt = req.CacheWriteTokens, req.CacheReadTokens, dt(tx.now)
			_, err := runs.update(bson.D{{Key: "_id", Value: req.Session}}, bson.D{{Key: "$set", Value: bson.D{
				{Key: "ticket_id", Value: cur.TicketID}, {Key: "cost_usd", Value: cur.CostUSD},
				{Key: "input_tokens", Value: cur.InputTokens}, {Key: "output_tokens", Value: cur.OutputTokens},
				{Key: "cache_write_tokens", Value: cur.CacheWriteTokens}, {Key: "cache_read_tokens", Value: cur.CacheReadTokens},
				{Key: "updated_at", Value: cur.UpdatedAt},
			}}})
			if err != nil {
				return err
			}
		}
		out = toRun(cur)
		return nil
	})
	return out, err
}

// Runs implements ticket.Store. A run counts toward the ticket that
// carries its work now, as ticket.RunRow says.
func (s *Store) Runs(ctx context.Context, f ticket.RunFilter) ([]ticket.RunRow, error) {
	out := []ticket.RunRow{}
	err := s.read(ctx, func(tx *txn) error {
		out = out[:0]
		parents, err := tx.splitParents()
		if err != nil {
			return err
		}
		var docs []ticketDoc
		err = tx.coll(cTickets).find(bson.D{}, &docs, options.Find().SetProjection(bson.D{
			{Key: "goal", Value: 1}, {Key: "created_by", Value: 1}, {Key: "tries", Value: 1}, {Key: "status", Value: 1},
		}))
		if err != nil {
			return err
		}
		tickets := map[ticket.ID]ticketDoc{}
		for _, d := range docs {
			tickets[ticket.ID(d.ID)] = d
		}
		kids := map[ticket.ID][]ticket.ID{}
		for kid, p := range parents {
			kids[p] = append(kids[p], kid)
		}
		carry := func(id ticket.ID) ticket.ID {
			for seen := map[ticket.ID]bool{}; !seen[id]; {
				seen[id] = true
				if k := kids[id]; len(k) == 1 && ticket.Status(tickets[id].Status) == ticket.StatusCancelled {
					id = k[0]
					continue
				}
				break
			}
			return id
		}
		filter := bson.D{}
		if !f.Since.IsZero() {
			filter = append(filter, bson.E{Key: "created_at", Value: bson.D{{Key: "$gte", Value: bson.NewDateTimeFromTime(f.Since)}}})
		}
		var runs []runDoc
		err = tx.coll(cRuns).find(filter, &runs, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}))
		if err != nil {
			return err
		}
		for _, d := range runs {
			r := ticket.RunRow{Run: toRun(d)}
			if r.Ticket != 0 {
				if to := carry(r.Ticket); to != r.Ticket {
					r.CarriedFrom, r.Ticket = r.Ticket, to
				}
			}
			if f.Ticket != 0 && r.Ticket != f.Ticket && r.CarriedFrom != f.Ticket {
				continue
			}
			if r.Ticket != 0 {
				t := tickets[r.Ticket]
				r.Goal, r.Origin, r.Tries = t.Goal, t.CreatedBy, t.Tries
				if _, ok := parents[r.Ticket]; ok {
					r.Origin = "split"
				}
			}
			out = append(out, r)
		}
		return nil
	})
	return out, err
}

// StartLaunch implements ticket.Store. A ticket id that names no ticket
// fails with ErrNotFound, as LogRun does.
func (s *Store) StartLaunch(ctx context.Context, req ticket.StartLaunchRequest) (ticket.Launch, error) {
	if err := req.Validate(); err != nil {
		return ticket.Launch{}, err
	}
	var out ticket.Launch
	err := s.write(ctx, func(tx *txn) error {
		tid, err := tx.ticketRef(req.Ticket)
		if err != nil {
			return err
		}
		d := launchDoc{ID: tx.next(&tx.w.Launches), Actor: strings.TrimSpace(req.Actor), TicketID: tid, PID: req.PID,
			Stamp: req.Stamp, StartedAt: dt(tx.now)}
		if err := tx.coll(cLaunches).insert(d); err != nil {
			return err
		}
		out = toLaunch(d)
		return nil
	})
	return out, err
}

// EndLaunch implements ticket.Store.
func (s *Store) EndLaunch(ctx context.Context, req ticket.EndLaunchRequest) (bool, error) {
	if err := req.Validate(); err != nil {
		return false, err
	}
	var ended bool
	err := s.write(ctx, func(tx *txn) error {
		res, err := tx.coll(cLaunches).update(bson.D{{Key: "_id", Value: req.ID}, {Key: "ended_at", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{
			{Key: "ended_at", Value: dt(tx.now)}, {Key: "outcome", Value: req.Outcome}, {Key: "session", Value: req.Session},
		}}})
		if err != nil {
			return err
		}
		ended = res.MatchedCount == 1
		return nil
	})
	return ended, err
}

// OpenLaunches implements ticket.Store.
func (s *Store) OpenLaunches(ctx context.Context) ([]ticket.Launch, error) {
	out := []ticket.Launch{}
	err := s.read(ctx, func(tx *txn) error {
		out = out[:0]
		var docs []launchDoc
		err := tx.coll(cLaunches).find(bson.D{{Key: "ended_at", Value: nil}}, &docs,
			options.Find().SetSort(bson.D{{Key: "started_at", Value: 1}, {Key: "_id", Value: 1}}))
		for _, d := range docs {
			out = append(out, toLaunch(d))
		}
		return err
	})
	return out, err
}

// SetETA implements ticket.Store.
func (s *Store) SetETA(ctx context.Context, req ticket.SetETARequest) (ticket.ETA, error) {
	if err := req.Validate(); err != nil {
		return ticket.ETA{}, err
	}
	var out ticket.ETA
	err := s.write(ctx, func(tx *txn) error {
		// A goal no ticket carries, such as a misspelt slug, would read as
		// met for ever and never alert.
		n, err := tx.coll(cTickets).count(bson.D{{Key: "goal", Value: req.Goal}})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: no ticket has goal %s", ticket.ErrNotFound, req.Goal)
		}
		ids := make([]int64, len(req.Tickets))
		for i, id := range req.Tickets {
			if _, err := tx.ticketRef(id); err != nil {
				return err
			}
			ids[i] = int64(id)
		}
		d := etaDoc{Goal: req.Goal, DueAt: bson.NewDateTimeFromTime(req.Due), Tickets: ids, SetBy: req.Actor, SetAt: dt(tx.now)}
		_, err = tx.coll(cETAs).update(bson.D{{Key: "_id", Value: req.Goal}}, bson.D{{Key: "$set", Value: bson.D{
			{Key: "due_at", Value: d.DueAt}, {Key: "tickets", Value: d.Tickets}, {Key: "set_by", Value: d.SetBy},
			{Key: "set_at", Value: d.SetAt}, {Key: "met_at", Value: nil},
		}}}, options.UpdateOne().SetUpsert(true))
		if err != nil {
			return err
		}
		out = toETA(d)
		return nil
	})
	return out, err
}

// ClearETA implements ticket.Store. Clearing a goal with no ETA fails with
// ErrNotFound.
func (s *Store) ClearETA(ctx context.Context, goal string) error {
	return s.write(ctx, func(tx *txn) error {
		n, err := tx.coll(cETAs).deleteOne(bson.D{{Key: "_id", Value: strings.TrimSpace(goal)}})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: no ETA for goal %q", ticket.ErrNotFound, goal)
		}
		return nil
	})
}

// MarkETAMet implements ticket.Store. The due and set times name the ETA a
// look judged, so a replacement set meanwhile is left alone.
func (s *Store) MarkETAMet(ctx context.Context, goal string, due, setAt time.Time) error {
	return s.write(ctx, func(tx *txn) error {
		_, err := tx.coll(cETAs).update(bson.D{
			{Key: "_id", Value: goal}, {Key: "due_at", Value: bson.NewDateTimeFromTime(due)},
			{Key: "set_at", Value: bson.NewDateTimeFromTime(setAt)}, {Key: "met_at", Value: nil},
		}, bson.D{{Key: "$set", Value: bson.D{{Key: "met_at", Value: dt(tx.now)}}}})
		return err
	})
}

// ETAs implements ticket.Store.
func (s *Store) ETAs(ctx context.Context) ([]ticket.ETA, error) {
	out := []ticket.ETA{}
	err := s.read(ctx, func(tx *txn) error {
		var err error
		out, err = tx.etas()
		return err
	})
	return out, err
}

func (tx *txn) etas() ([]ticket.ETA, error) {
	var docs []etaDoc
	err := tx.coll(cETAs).find(bson.D{}, &docs, options.Find().SetSort(bson.D{{Key: "due_at", Value: 1}, {Key: "_id", Value: 1}}))
	out := []ticket.ETA{}
	for _, d := range docs {
		out = append(out, toETA(d))
	}
	return out, err
}
