package mongostore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Marks are what the CLI keeps so that two sessions, on one machine or
// two, never do one job twice: a look's handouts, a run's .active mark,
// the pause. On SQLite they are files beside the store; here each is one
// document in marks, keyed by its space and name, so a create on one
// machine and a create on another can't both win.
//
// A mark is a single document, written by one operation, so marks need
// no transaction and never wait on the writer. Their times are the
// server's: a reader gets a mark's age, not a machine's clock, so two
// machines whose clocks differ agree on which mark is stale.

// cMarks holds the marks.
const cMarks = "marks"

type markDoc struct {
	ID    string        `bson:"_id"` // space/name
	Space string        `bson:"space"`
	Name  string        `bson:"name"`
	Body  string        `bson:"body"`
	At    bson.DateTime `bson:"at"`
	Ver   bson.ObjectID `bson:"ver"`
	Now   bson.DateTime `bson:"now"` // the server's clock as read, projected
}

// Mark is a mark as read.
type Mark struct {
	Name string
	Body string
	// Age is how long ago the mark was last written, by the server's
	// clock.
	Age time.Duration
	// Ver is new on every write, so a reader can tell the mark it read
	// from one written over it since.
	Ver string
}

func markID(space, name string) bson.D {
	return bson.D{{Key: "_id", Value: space + "/" + name}}
}

// markRead projects what a reader needs, and the server's clock.
func markRead() bson.D {
	return bson.D{{Key: "name", Value: 1}, {Key: "at", Value: 1}, {Key: "ver", Value: 1}, {Key: "now", Value: "$$NOW"}}
}

func toMark(d markDoc, body bool) Mark {
	m := Mark{Name: d.Name, Age: max(time.Duration(int64(d.Now)-int64(d.At))*time.Millisecond, 0), Ver: d.Ver.Hex()}
	if body {
		m.Body = d.Body
	}
	return m
}

// CreateMark makes a mark holding body, or fails with fs.ErrExist if one
// stands: of creates at once, on any machine, one wins.
func (s *Store) CreateMark(ctx context.Context, space, name, body string) error {
	// An upsert whose filter no mark matches: one that stands makes the
	// insert clash on _id, and the clock is the server's.
	filter := append(markID(space, name), bson.E{Key: "ver", Value: bson.D{{Key: "$exists", Value: false}}})
	_, err := s.db.Collection(cMarks).UpdateOne(ctx, filter, bson.D{
		{Key: "$setOnInsert", Value: bson.D{{Key: "space", Value: space}, {Key: "name", Value: name}, {Key: "body", Value: body}, {Key: "ver", Value: bson.NewObjectID()}}},
		{Key: "$currentDate", Value: bson.D{{Key: "at", Value: true}}},
	}, options.UpdateOne().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("mark %s/%s: %w", space, name, fs.ErrExist)
	}
	return s.markErr(err)
}

// ReadMark reads a mark, failing with fs.ErrNotExist if none stands.
func (s *Store) ReadMark(ctx context.Context, space, name string) (Mark, error) {
	var d markDoc
	err := s.db.Collection(cMarks).FindOne(ctx, markID(space, name),
		options.FindOne().SetProjection(append(markRead(), bson.E{Key: "body", Value: 1}))).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Mark{}, fmt.Errorf("mark %s/%s: %w", space, name, fs.ErrNotExist)
	}
	if err != nil {
		return Mark{}, s.markErr(err)
	}
	return toMark(d, true), nil
}

// WriteMark sets a mark's body whole, made or not.
func (s *Store) WriteMark(ctx context.Context, space, name, body string) error {
	_, err := s.db.Collection(cMarks).UpdateOne(ctx, markID(space, name), bson.D{
		{Key: "$set", Value: bson.D{{Key: "space", Value: space}, {Key: "name", Value: name}, {Key: "body", Value: body}, {Key: "ver", Value: bson.NewObjectID()}}},
		{Key: "$currentDate", Value: bson.D{{Key: "at", Value: true}}},
	}, options.UpdateOne().SetUpsert(true))
	return s.markErr(err)
}

// RemoveMark removes a mark, failing with fs.ErrNotExist if none stands.
func (s *Store) RemoveMark(ctx context.Context, space, name string) error {
	res, err := s.db.Collection(cMarks).DeleteOne(ctx, markID(space, name))
	if err != nil {
		return s.markErr(err)
	}
	if res.DeletedCount == 0 {
		return fmt.Errorf("mark %s/%s: %w", space, name, fs.ErrNotExist)
	}
	return nil
}

// RemoveMarkIf removes a mark only while it is still the copy ver was
// read from, and reports whether it did: of removers that all read one
// copy, one removes it.
func (s *Store) RemoveMarkIf(ctx context.Context, space, name, ver string) (bool, error) {
	oid, err := bson.ObjectIDFromHex(ver)
	if err != nil {
		return false, nil // no copy has it
	}
	res, err := s.db.Collection(cMarks).DeleteOne(ctx, append(markID(space, name), bson.E{Key: "ver", Value: oid}))
	if err != nil {
		return false, s.markErr(err)
	}
	return res.DeletedCount == 1, nil
}

// ListMarks names each mark in a space, with its age; it reads no
// bodies.
func (s *Store) ListMarks(ctx context.Context, space string) ([]Mark, error) {
	cur, err := s.db.Collection(cMarks).Find(ctx, bson.D{{Key: "space", Value: space}},
		options.Find().SetProjection(markRead()).SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, s.markErr(err)
	}
	var docs []markDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, s.markErr(err)
	}
	out := make([]Mark, 0, len(docs))
	for _, d := range docs {
		out = append(out, toMark(d, false))
	}
	return out, nil
}

func (s *Store) markErr(err error) error { return Redact(err, s.client.uri) }

// Halted reports the halt stop --all set: while it stands no claim lands
// and no lease that ran out comes back, on any machine.
func (s *Store) Halted(ctx context.Context) (bool, error) {
	var w struct {
		Halt bool `bson:"halt"`
	}
	err := s.db.Collection(cMeta).FindOne(ctx, bson.D{{Key: "_id", Value: writerID}},
		options.FindOne().SetProjection(bson.D{{Key: "halt", Value: 1}})).Decode(&w)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, fmt.Errorf("database %s holds no maestro board", s.name)
	}
	return w.Halt, s.markErr(err)
}

// SetHalt sets or lifts the halt, and reports whether it changed. It is a
// write like any other, on the writer document, so a claim already under
// way either commits before it or sees it.
func (s *Store) SetHalt(ctx context.Context, on bool) (bool, error) {
	var changed bool
	err := s.write(ctx, func(tx *txn) error {
		changed = tx.w.Halt != on
		if !changed {
			return nil
		}
		_, err := tx.coll(cMeta).update(bson.D{{Key: "_id", Value: writerID}}, bson.D{{Key: "$set", Value: bson.D{{Key: "halt", Value: on}}}})
		return err
	})
	return changed, err
}
