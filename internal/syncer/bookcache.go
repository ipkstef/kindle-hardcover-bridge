package syncer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// BookMap is one cached match: Kindle book key → Hardcover book. With it the
// match waterfall (ISBN, search, library) runs once per book.
type BookMap struct {
	BookID    int       `json:"book_id"`
	EditionID *int      `json:"edition_id,omitempty"`
	Pages     int       `json:"pages,omitempty"` // pages of EditionID, if known
	Title     string    `json:"title"`
	Method    string    `json:"method"`
	At        time.Time `json:"at"`
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
	c.m[key] = m
	b, err := json.MarshalIndent(c.m, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return err
	}
	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.Path)
}
