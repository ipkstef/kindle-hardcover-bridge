package syncer

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/atomicfile"
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
}

// byTitle reports if the match came from a title compare, not an ID.
func (m BookMap) byTitle() bool {
	switch m.Method {
	case "library", "search", "search+year":
		return true
	}
	return false
}

// BookCache stores matches in a JSON file (a local database is planned later,
// docs/roadmap.md).
type BookCache struct {
	Path string
	mu   sync.Mutex
	m    map[string]BookMap
}

func (c *BookCache) load() {
	if c.m != nil {
		return
	}
	c.m = map[string]BookMap{}
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
	c.m[key] = m
	return atomicfile.WriteJSON(c.Path, c.m, 0o600)
}
