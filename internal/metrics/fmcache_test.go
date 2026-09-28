package metrics

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestRatings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fmcache.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Table layout as seen on the device.
	for _, q := range []string{
		`CREATE TABLE records (id INTEGER PRIMARY KEY, schema_name TEXT, schema_version INTEGER,
			app_session_id INTEGER, reading_session_id INTEGER, encoded_size INTEGER,
			sequence_number INTEGER, created_timestamp INTEGER, priority INTEGER, record TEXT)`,
		`INSERT INTO records VALUES (107,'goodreads_eink_availability',0,1,2,208,111,1790573268000,0,'{"available":0}')`,
		// Record copied from the device (rating research).
		`INSERT INTO records VALUES (108,'goodreads_book_ratings',0,2272,2,322,112,1790573268403,0,
			'{"action_id":"write_rating","book_asin":"dc1147a1-5833-48fb-bd35-fd496fcaabc3","context":"end_actions","context_id":"none","event_type":"change_rating","rating":"4"}')`,
		`INSERT INTO records VALUES (109,'goodreads_book_ratings',0,2272,2,322,113,1790573300000,0,
			'{"action_id":"write_rating","book_asin":"k2","event_type":"change_rating","rating":5}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	rs, err := Ratings(context.Background(), path, 0)
	if err != nil || len(rs) != 2 {
		t.Fatalf("got %+v %v", rs, err)
	}
	if rs[0].BookKey != "dc1147a1-5833-48fb-bd35-fd496fcaabc3" || rs[0].Stars != 4 || rs[0].Context != "end_actions" {
		t.Errorf("first %+v", rs[0])
	}
	if rs[1].Stars != 5 {
		t.Errorf("second %+v", rs[1])
	}
	rs, _ = Ratings(context.Background(), path, 1790573268403)
	if len(rs) != 1 || rs[0].BookKey != "k2" {
		t.Errorf("since: %+v", rs)
	}
}

func TestShelfChoice(t *testing.T) {
	cases := []struct {
		rec    string
		status int
	}{
		{`{"action_id":"write_shelf","book_asin":"k","shelf":"to-read"}`, ShelfWantToRead},
		{`{"book_asin":"k","shelf":{"name":"currently-reading"}}`, ShelfReading},
		{`{"book_asin":"k","new_shelf":"READ"}`, ShelfRead},
		{`{"book_asin":"k","shelf":{"name":"unshelved"}}`, ShelfNone},
		{`{"book_asin":"k","event_type":"read"}`, ShelfNone}, // not a shelf key
	}
	for _, c := range cases {
		st, key, _ := ShelfChoice(c.rec)
		if st != c.status || key != "k" {
			t.Errorf("%s → %d %q, want %d", c.rec, st, key, c.status)
		}
	}
}
