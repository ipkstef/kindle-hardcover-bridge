// Package metrics reads the Kindle's metrics cache (fmcache.db).
//
// Found on FW 5.17.1 (rating research, 2026-09-28): a star tap in the stock
// end-of-book dialog ("Before you go…") adds a row to table `records` with
// schema_name "goodreads_book_ratings" and record
//
//	{"action_id":"write_rating","book_asin":"<cc.db p_cdeKey>",
//	 "context":"end_actions","event_type":"change_rating","rating":"4"}
//
// The row is written before the Kindle tries to post the rating, so it is
// there even when the post fails ("Rating Error" for sideloaded books).
// The cache is uploaded and emptied soon (seen 5 min later, on sleep), so it
// must be read quickly after it changes.
package metrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // pure Go
)

// DefaultPath of the metrics cache.
const DefaultPath = "/mnt/us/system/fmcache/fmcache.db"

// Rating is one star tap.
type Rating struct {
	ID        int64
	CreatedMS int64  // created_timestamp, unix ms
	BookKey   string // book_asin = cc.db p_cdeKey
	Stars     float64
	Context   string // "end_actions"
	Event     string // "change_rating"
}

// Ratings returns rating records created after sinceMS, oldest first.
func Ratings(ctx context.Context, path string, sinceMS int64) ([]Rating, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id, created_timestamp, record FROM records
		WHERE schema_name = 'goodreads_book_ratings' AND created_timestamp > ?
		ORDER BY created_timestamp, id`, sinceMS)
	if err != nil {
		return nil, fmt.Errorf("fmcache: %w", err)
	}
	defer rows.Close()
	var out []Rating
	for rows.Next() {
		var id, ts int64
		var rec string
		if err := rows.Scan(&id, &ts, &rec); err != nil {
			return out, fmt.Errorf("fmcache: %w", err)
		}
		var r struct {
			Action  string          `json:"action_id"`
			Key     string          `json:"book_asin"`
			Context string          `json:"context"`
			Event   string          `json:"event_type"`
			Rating  json.RawMessage `json:"rating"`
		}
		if json.Unmarshal([]byte(rec), &r) != nil || r.Action != "write_rating" || r.Key == "" {
			continue
		}
		out = append(out, Rating{ID: id, CreatedMS: ts, BookKey: r.Key, Stars: stars(r.Rating),
			Context: r.Context, Event: r.Event})
	}
	return out, rows.Err()
}

// stars reads "4" or 4.
func stars(raw json.RawMessage) float64 {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	var f float64
	_ = json.Unmarshal(raw, &f)
	return f
}

// Record is one raw metrics record.
type Record struct {
	ID        int64
	CreatedMS int64
	Schema    string
	JSON      string
}

// Records returns records created after sinceMS whose schema_name starts with
// one of prefixes, oldest first. Used to learn formats (research) and to read
// Goodreads shelf choices from the end-of-book dialog.
func Records(ctx context.Context, path string, sinceMS int64, prefixes ...string) ([]Record, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id, created_timestamp, schema_name, record FROM records
		WHERE created_timestamp > ? ORDER BY created_timestamp, id`, sinceMS)
	if err != nil {
		return nil, fmt.Errorf("fmcache: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.CreatedMS, &r.Schema, &r.JSON); err != nil {
			return out, fmt.Errorf("fmcache: %w", err)
		}
		for _, p := range prefixes {
			if strings.HasPrefix(r.Schema, p) {
				out = append(out, r)
				break
			}
		}
	}
	return out, rows.Err()
}

// Shelf status IDs (Hardcover), for Goodreads shelf names.
const (
	ShelfNone       = 0
	ShelfWantToRead = 1
	ShelfReading    = 2
	ShelfRead       = 3
)

// ShelfChoice finds a Goodreads shelf choice in a record: a string value of a
// key that names a shelf ("shelf", "shelf_name", "new_shelf", "name", ...).
// Returns the Hardcover status and the book key (book_asin). The record
// format is UNVERIFIED: this accepts the usual Goodreads shelf names only.
func ShelfChoice(recJSON string) (status int, bookKey, raw string) {
	var m map[string]any
	if json.Unmarshal([]byte(recJSON), &m) != nil {
		return ShelfNone, "", ""
	}
	bookKey, _ = m["book_asin"].(string)
	var walk func(v any, key string)
	walk = func(v any, key string) {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				walk(x, k)
			}
		case string:
			if status != ShelfNone || !strings.Contains(strings.ToLower(key), "shelf") && key != "name" {
				return
			}
			switch strings.ToLower(strings.NewReplacer("_", "-", " ", "-").Replace(t)) {
			case "to-read", "want-to-read":
				status, raw = ShelfWantToRead, t
			case "currently-reading":
				status, raw = ShelfReading, t
			case "read":
				status, raw = ShelfRead, t
			}
		}
	}
	walk(m, "")
	return status, bookKey, raw
}
