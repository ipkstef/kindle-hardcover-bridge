// Package readers gets reading state from the stock Kindle reader.
package readers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"

	_ "modernc.org/sqlite" // pure Go, no cgo
)

// ErrNoBook means no matching book was found.
var ErrNoBook = errors.New("no recently opened book in cc.db")

// Database reads cc.db, read-only.
//
// Findings (FW 5.17.1): p_percentFinished is written when the user goes to
// Home, not on page turn and not on sleep. See docs/findings-bellatrix-5.17.1.md.
type Database struct {
	Path string
}

// Books only: ebooks and personal documents, no KUAL-style scripts.
const bookFilter = `p_type = 'Entry:Item' AND p_cdeType IN ('EBOK', 'PDOC')
	AND (p_mimeType IS NULL OR p_mimeType != 'text/x-shellscript')`

const bookColumns = `p_cdeKey, p_titles_0_nominal, j_credits, p_location,
	p_percentFinished, p_lastAccess, p_cdeType, p_mimeType,
	p_publisher, CAST(p_publicationDate AS TEXT), p_languages_0,
	CAST(p_lastAccessedPosition AS TEXT), p_readState`

func (d *Database) open() (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+d.Path+"?mode=ro&_pragma=busy_timeout(5000)")
}

// CurrentBook returns the opened book with the latest p_lastAccess.
func (d *Database) CurrentBook(ctx context.Context) (*book.Local, error) {
	return d.one(ctx, `SELECT `+bookColumns+` FROM Entries WHERE `+bookFilter+`
		AND p_percentFinished IS NOT NULL AND p_lastAccess IS NOT NULL
		ORDER BY p_lastAccess DESC LIMIT 1`)
}

// BookByKey returns the book with this p_cdeKey (latest access if the key
// appears more than once).
func (d *Database) BookByKey(ctx context.Context, key string) (*book.Local, error) {
	return d.one(ctx, `SELECT `+bookColumns+` FROM Entries WHERE `+bookFilter+`
		AND p_cdeKey = ? ORDER BY p_lastAccess DESC LIMIT 1`, key)
}

// Progress is one book's reading state.
type Progress struct {
	Percent    float64 `json:"percent"`
	LastAccess int64   `json:"last_access"`
	ReadState  int     `json:"read_state,omitempty"`
}

// AllProgress returns percent and last access for every opened book, by key.
func (d *Database) AllProgress(ctx context.Context) (map[string]Progress, error) {
	db, err := d.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT p_cdeKey, p_percentFinished, p_lastAccess, p_readState
		FROM Entries WHERE `+bookFilter+` AND p_cdeKey IS NOT NULL
		AND (p_percentFinished IS NOT NULL OR p_readState IS NOT NULL)`)
	if err != nil {
		return nil, fmt.Errorf("cc.db: %w", err)
	}
	defer rows.Close()
	out := map[string]Progress{}
	for rows.Next() {
		var key string
		var pct sql.NullFloat64
		var last, rs sql.NullInt64
		if err := rows.Scan(&key, &pct, &last, &rs); err != nil {
			return nil, fmt.Errorf("cc.db: %w", err)
		}
		// Same key twice (seen on device): keep the latest access.
		if old, ok := out[key]; ok && old.LastAccess >= last.Int64 {
			continue
		}
		out[key] = Progress{Percent: pct.Float64, LastAccess: last.Int64, ReadState: int(rs.Int64)}
	}
	return out, rows.Err()
}

func (d *Database) one(ctx context.Context, q string, args ...any) (*book.Local, error) {
	db, err := d.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var (
		key, title, credits, path               sql.NullString
		cdeType, mime, publisher, pubDate, lang sql.NullString
		lastPos                                 sql.NullString
		readState                               sql.NullInt64
		percent                                 sql.NullFloat64
		last                                    sql.NullInt64
	)
	err = db.QueryRowContext(ctx, q, args...).Scan(&key, &title, &credits, &path, &percent, &last,
		&cdeType, &mime, &publisher, &pubDate, &lang, &lastPos, &readState)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoBook
	}
	if err != nil {
		return nil, fmt.Errorf("cc.db: %w", err)
	}
	return &book.Local{
		Key:        key.String,
		Title:      title.String,
		Authors:    parseCredits(credits.String),
		Path:       path.String,
		Percent:    percent.Float64,
		LastAccess: last.Int64,
		CDEType:    cdeType.String,
		MimeType:   mime.String,
		Publisher:  publisher.String,
		PubDate:    pubDate.String,
		Language:   lang.String,

		LastPosition: lastPos.String,
		ReadState:    int(readState.Int64),
	}, nil
}

// parseCredits reads j_credits:
// [{"name":{"display":"Matt Dinniman",...},"kind":"Author"}]
func parseCredits(s string) []string {
	var list []struct {
		Name struct {
			Display string `json:"display"`
		} `json:"name"`
		Kind string `json:"kind"`
	}
	if json.Unmarshal([]byte(s), &list) != nil {
		return nil
	}
	var out []string
	for _, c := range list {
		if c.Name.Display != "" && (c.Kind == "" || c.Kind == "Author") {
			out = append(out, c.Name.Display)
		}
	}
	return out
}
