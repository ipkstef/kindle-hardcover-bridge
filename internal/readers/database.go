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

// ErrNoBook means no opened book was found.
var ErrNoBook = errors.New("no recently opened book in cc.db")

// Database reads cc.db, read-only.
//
// Findings (FW 5.17.1): p_percentFinished is written when the user goes to
// Home, not on page turn and not on sleep. See docs/findings-bellatrix-5.17.1.md.
type Database struct {
	Path string
}

// CurrentBook returns the ebook with the latest p_lastAccess.
func (d *Database) CurrentBook(ctx context.Context) (*book.Local, error) {
	db, err := sql.Open("sqlite", "file:"+d.Path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		SELECT p_cdeKey, p_titles_0_nominal, j_credits, p_location,
		       p_percentFinished, p_lastAccess
		FROM Entries
		WHERE p_type = 'Entry:Item' AND p_cdeType IN ('EBOK', 'PDOC')
		  AND p_percentFinished IS NOT NULL AND p_lastAccess IS NOT NULL
		  AND p_mimeType != 'text/x-shellscript'
		ORDER BY p_lastAccess DESC
		LIMIT 1`)
	var (
		key, title, credits, path sql.NullString
		percent                   sql.NullFloat64
		last                      sql.NullInt64
	)
	if err := row.Scan(&key, &title, &credits, &path, &percent, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoBook
		}
		return nil, fmt.Errorf("cc.db: %w", err)
	}
	return &book.Local{
		Key:        key.String,
		Title:      title.String,
		Authors:    parseCredits(credits.String),
		Path:       path.String,
		Percent:    percent.Float64,
		LastAccess: last.Int64,
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
