package mongostore

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// ownDB is database name on a client of the test's own, apart from the
// stores'.
func ownDB(t *testing.T, uri, name string) *mongo.Database {
	t.Helper()
	c, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(selectTimeout))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Disconnect(context.Background()) })
	return c.Database(name)
}

func hasIndex(t *testing.T, db *mongo.Database, coll, name string) bool {
	t.Helper()
	specs, err := db.Collection(coll).Indexes().ListSpecifications(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

// A board this build set up opens with one read: no index build, which a
// role with no createIndex grant on events would refuse. A board an older
// build made gets its indexes on its next open.
func TestOpenBuildsOnlyABoardNotSetUp(t *testing.T) {
	uri := testURI(t)
	ctx := context.Background()
	name := freshDB(t, uri)
	s, err := Open(ctx, uri, Options{Database: name})
	if err != nil {
		t.Fatal(err)
	}
	s.Close() // and the process's client with it
	db := ownDB(t, uri, name)
	if err := db.Collection(cEvents).Indexes().DropOne(ctx, "events_type"); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, uri, Options{Database: name})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if hasIndex(t, db, cEvents, "events_type") {
		t.Fatal("open built the indexes of a board already set up")
	}

	if _, err := db.Collection(cMeta).UpdateOne(ctx, bson.D{{Key: "_id", Value: writerID}},
		bson.D{{Key: "$unset", Value: bson.D{{Key: "setup", Value: ""}}}}); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, uri, Options{Database: name})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !hasIndex(t, db, cEvents, "events_type") {
		t.Fatal("open left a board an older build made without its indexes")
	}
	if _, err := s.Create(ctx, ticket.CreateRequest{Title: "x"}); err != nil {
		t.Fatal(err)
	}
}

// keep errors imported for the tests below.
var _ = errors.Is
