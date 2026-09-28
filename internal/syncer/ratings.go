package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/metrics"
)

// BookByKey is the part of the cc.db reader the rating sync needs.
type BookByKey interface {
	BookByKey(ctx context.Context, key string) (*book.Local, error)
}

// RateState is saved between runs.
type RateState struct {
	LastMS  int64                     `json:"last_ms"` // newest record read from fmcache
	Pending map[string]metrics.Rating `json:"pending"` // book key → last tap, not yet sent
}

// RateSync sends star taps from the stock end-of-book dialog to Hardcover
// (user decision: reuse the native dialog). Taps are copied to our state
// first, because the Kindle empties fmcache.db soon after.
type RateSync struct {
	S         *Syncer
	Books     BookByKey
	Path      string // fmcache.db
	StatePath string
	mu        sync.Mutex
}

// Run copies new taps into the state, then sends pending ones.
func (r *RateSync) Run(ctx context.Context) (sent int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.load()

	if _, err := os.Stat(r.Path); err == nil {
		rs, err := metrics.Ratings(ctx, r.Path, st.LastMS)
		if err != nil {
			r.S.logf("ratings: read %v", err)
		}
		for _, x := range rs {
			st.Pending[x.BookKey] = x // last tap wins
			if x.CreatedMS > st.LastMS {
				st.LastMS = x.CreatedMS
			}
			r.S.logf("ratings: tap %.0f stars on book %s (%s)", x.Stars, short8(x.BookKey), x.Context)
		}
		if len(rs) > 0 {
			if err := r.save(st); err != nil {
				return 0, err
			}
		}
	}

	keys := make([]string, 0, len(st.Pending))
	for k := range st.Pending {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return st.Pending[keys[i]].CreatedMS < st.Pending[keys[j]].CreatedMS })
	for _, k := range keys {
		x := st.Pending[k]
		if err := r.send(ctx, x); err != nil {
			return sent, err // keep pending, retry later (e.g. no Wi-Fi)
		}
		delete(st.Pending, k)
		sent++
		if err := r.save(st); err != nil {
			return sent, err
		}
	}
	return sent, nil
}

func (r *RateSync) send(ctx context.Context, x metrics.Rating) error {
	if x.Stars <= 0 || x.Stars > 5 {
		r.S.logf("ratings: book %s: rating %.1f ignored", short8(x.BookKey), x.Stars)
		return nil
	}
	local, err := r.Books.BookByKey(ctx, x.BookKey)
	if err != nil {
		r.S.logf("ratings: book %s not in cc.db, dropped", short8(x.BookKey))
		return nil
	}
	_, ub, err := r.S.Identify(ctx, local)
	if errors.Is(err, ErrNotFound) {
		r.S.logf("ratings: %q not found on Hardcover, dropped", local.Title)
		return nil
	}
	if err != nil {
		return err
	}
	if ub == nil {
		// Not on the shelves yet: a normal sync adds it (and finishes it
		// at the end of the book), then rate it.
		if _, err := r.S.Sync(ctx, local); err != nil {
			return err
		}
		if _, ub, err = r.S.Identify(ctx, local); err != nil {
			return err
		}
		if ub == nil {
			r.S.logf("ratings: %q could not be added, dropped", local.Title)
			return nil
		}
	}
	if _, err := r.S.C.SetRating(ctx, ub.ID, x.Stars); err != nil {
		return err
	}
	r.S.logf("ratings: %q rated %.0f stars (user_book %d)", local.Title, x.Stars, ub.ID)
	return nil
}

func short8(k string) string {
	if len(k) > 8 {
		return k[:8]
	}
	return k
}

func (r *RateSync) load() *RateState {
	st := &RateState{}
	if b, err := os.ReadFile(r.StatePath); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Pending == nil {
		st.Pending = map[string]metrics.Rating{}
	}
	return st
}

func (r *RateSync) save(st *RateState) error {
	b, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.StatePath), 0o700); err != nil {
		return err
	}
	tmp := r.StatePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.StatePath)
}
