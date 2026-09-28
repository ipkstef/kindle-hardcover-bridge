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
