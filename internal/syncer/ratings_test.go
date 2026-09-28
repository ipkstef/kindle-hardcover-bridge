package syncer

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
)

func (fakeBooks) BookByKey(_ context.Context, key string) (*book.Local, error) {
	l := redRising
	l.Key, l.Percent = key, 100
	return &l, nil
}

func TestRateSyncSurvivesFlushAndOffline(t *testing.T) {
	dir := t.TempDir()
	fm := filepath.Join(dir, "fmcache.db")
	db, _ := sql.Open("sqlite", fm)
	db.Exec(`CREATE TABLE records (id INTEGER PRIMARY KEY, schema_name TEXT, created_timestamp INTEGER, record TEXT)`)
	db.Exec(`INSERT INTO records VALUES (1,'goodreads_book_ratings',1000,'{"action_id":"write_rating","book_asin":"k","event_type":"change_rating","rating":"3"}')`)
	db.Exec(`INSERT INTO records VALUES (2,'goodreads_book_ratings',2000,'{"action_id":"write_rating","book_asin":"k","event_type":"change_rating","rating":"4"}')`)

	f := newFake(2)
	ok := f.reply["update_user_book"]
	f.reply["update_user_book"] = `{"errors":[{"message":"network down"}]}`
	rs := &RateSync{S: &Syncer{C: f.client(t), Logf: t.Logf}, Books: fakeBooks{},
		Path: fm, StatePath: filepath.Join(dir, "ratings.json")}

	if n, err := rs.Run(context.Background()); err == nil || n != 0 {
		t.Fatalf("offline run: %d %v", n, err)
	}
	// The Kindle uploads and empties its cache.
	db.Exec(`DELETE FROM records`)
	db.Close()

	f.reply["update_user_book"] = ok
	f.ops, f.vars = nil, nil
	n, err := rs.Run(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("online run: %d %v", n, err)
	}
	var rating any
	for i, op := range f.ops {
		if op == "update_user_book" {
			rating = f.vars[i]["rating"]
		}
	}
	if rating != float64(4) { // last tap wins
		t.Fatalf("rating %v, ops %v", rating, f.ops)
	}
	f.ops = nil
	if n, _ := rs.Run(context.Background()); n != 0 || len(f.ops) != 0 {
		t.Fatalf("repeat: %d %v", n, f.ops)
	}
}

func TestShelfChoiceSent(t *testing.T) {
	dir := t.TempDir()
	fm := filepath.Join(dir, "fmcache.db")
	db, _ := sql.Open("sqlite", fm)
	db.Exec(`CREATE TABLE records (id INTEGER PRIMARY KEY, schema_name TEXT, created_timestamp INTEGER, record TEXT)`)
	// Record format as seen on the device.
	db.Exec(`INSERT INTO records VALUES (1,'goodreads_autoshelvings',1000,'{"action_id":"PerformManualShelving","context":"end_actions","kindle_asin":"k","shelf_status":"to-read"}')`)
	db.Close()
	f := newFake(2)
	rs := &RateSync{S: &Syncer{C: f.client(t), Logf: t.Logf}, Books: fakeBooks{},
		Path: fm, StatePath: filepath.Join(dir, "ratings.json")}
	n, err := rs.Run(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("got %d %v", n, err)
	}
	var status any
	for i, op := range f.ops {
		if op == "update_user_book" {
			status = f.vars[i]["status"]
		}
	}
	if status != float64(1) {
		t.Fatalf("status %v, ops %v", status, f.ops)
	}
}

// A rating on a known book: 1 shelf lookup + 1 rating (+ "me" once per run).
// Device log 2026-09-28 had ~15 calls and hit HTTP 429.
func TestRatingCallCount(t *testing.T) {
	dir := t.TempDir()
	fm := filepath.Join(dir, "fmcache.db")
	db, _ := sql.Open("sqlite", fm)
	db.Exec(`CREATE TABLE records (id INTEGER PRIMARY KEY, schema_name TEXT, created_timestamp INTEGER, record TEXT)`)
	db.Exec(`INSERT INTO records VALUES (1,'goodreads_book_ratings',1000,'{"action_id":"write_rating","book_asin":"k","rating":"4"}')`)
	f := newFake(3)
	cache := &BookCache{Path: filepath.Join(dir, "bookmap.json")}
	cache.Put("k", BookMap{BookID: 500, Title: "Red Rising", Method: "isbn13"})
	rs := &RateSync{S: &Syncer{C: f.client(t), Cache: cache, Logf: t.Logf}, Books: fakeBooks{},
		Path: fm, StatePath: filepath.Join(dir, "ratings.json")}
	if n, err := rs.Run(context.Background()); err != nil || n != 1 {
		t.Fatalf("got %d %v", n, err)
	}
	if len(f.ops) != 3 { // me, user_book, update
		t.Fatalf("first rating: %d calls %v", len(f.ops), f.ops)
	}
	db.Exec(`INSERT INTO records VALUES (2,'goodreads_book_ratings',2000,'{"action_id":"write_rating","book_asin":"k","rating":"5"}')`)
	db.Close()
	f.ops, f.vars = nil, nil
	if n, err := rs.Run(context.Background()); err != nil || n != 1 {
		t.Fatalf("got %d %v", n, err)
	}
	if len(f.ops) != 2 { // user_book, update ("me" cached)
		t.Fatalf("second rating: %d calls %v", len(f.ops), f.ops)
	}
}
