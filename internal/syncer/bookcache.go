package syncer

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/atomicfile"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/store"
)

// titleRule is the version of the title compare rule. Title-based matches
// (library, search) made by an older rule are looked up again: rule 1 cut
// subtitles and could pick a different book of the same series.
const titleRule = 2

// BookMap is one cached match: Kindle book key → Hardcover book. With it the
// match waterfall (ISBN, search, library) runs once per book.
type BookMap struct {
	BookID    int       `json:"book_id"`
	EditionID *int      `json:"edition_id,omitempty"`
	Pages     int       `json:"pages,omitempty"` // pages of EditionID, if known
	Title     string    `json:"title"`
	Method    string    `json:"method"`
	At        time.Time `json:"at"`
	Rule      int       `json:"rule,omitempty"` // titleRule when made
	// Last progress sent: when the Kindle percent maps to the same page of
	// the same edition, Sync sends nothing and makes no API call.
	LastPage  int `json:"last_page,omitempty"`
	LastPages int `json:"last_pages,omitempty"`
}

// byTitle reports if the match came from a title compare, not an ID.
func (m BookMap) byTitle() bool {
	switch m.Method {
	case "library", "search", "search+year", "search+readers":
		return true
	}
	return false
}

// BookCache stores matches in a JSON file (a local database is planned later,
// docs/roadmap.md).
type BookCache struct {
	Path string       // JSON file (used when DB is nil; imported into DB once)
	DB   *store.Store // optional
	mu   sync.Mutex
	m    map[string]BookMap
	t    *store.Table[BookMap]
}

func (c *BookCache) load() {
	if c.m != nil {
		return
	}
	c.m = map[string]BookMap{}
	if c.DB != nil {
		c.t = store.NewTable[BookMap](c.DB, store.TBookMap)
		_ = c.DB.ImportJSON("bookmap.json", c.Path, func(b []byte) error {
			var m map[string]BookMap
			if json.Unmarshal(b, &m) != nil {
				return nil
			}
			return c.t.Save(m)
		})
		if m, err := c.t.Load(); err == nil {
			c.m = m
		}
		return
	}
	if b, err := os.ReadFile(c.Path); err == nil {
		_ = json.Unmarshal(b, &c.m)
	}
}

// Get returns the cached match for a key.
func (c *BookCache) Get(key string) (BookMap, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	m, ok := c.m[key]
	if ok && m.byTitle() && m.Rule < titleRule {
		return BookMap{}, false
	}
	return m, ok
}

// Put saves a match.
func (c *BookCache) Put(key string, m BookMap) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	if m.At.IsZero() {
		m.At = time.Now()
	}
	m.Rule = titleRule
	if old, ok := c.m[key]; ok && old.BookID == m.BookID && m.LastPages == 0 {
		m.LastPage, m.LastPages = old.LastPage, old.LastPages // keep across re-matches
	}
	c.m[key] = m
	if c.t != nil {
		return c.t.Put(key, m) // one row
	}
	return atomicfile.GuardedJSON(c.Path, c.m, 0o600)
}

// SetSent records the page last sent for a book (no-op if not cached).
func (c *BookCache) SetSent(key string, page, pages int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	m, ok := c.m[key]
	if !ok || (m.LastPage == page && m.LastPages == pages) {
		return nil
	}
	m.LastPage, m.LastPages = page, pages
	c.m[key] = m
	if c.t != nil {
		return c.t.Put(key, m)
	}
	return atomicfile.GuardedJSON(c.Path, c.m, 0o600)
}
