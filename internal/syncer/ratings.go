package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"sync"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/atomicfile"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/hardcover"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/metrics"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/store"
)

// BookByKey is the part of the cc.db reader the rating sync needs.
type BookByKey interface {
	BookByKey(ctx context.Context, key string) (*book.Local, error)
}

// RateState is saved between runs.
type RateState struct {
	LastMS  int64                     `json:"last_ms"` // newest record read from fmcache
	Pending map[string]metrics.Rating `json:"pending"` // book key → last tap, not yet sent
	// OtherMS: newest non-rating record read (shelf choices, research log).
	OtherMS int64 `json:"other_ms"`
	// Shelves: book key → Hardcover status chosen in the dialog, not yet sent.
	Shelves map[string]int `json:"shelves,omitempty"`
}

// RateSync sends star taps from the stock end-of-book dialog to Hardcover
// (user decision: reuse the native dialog). Taps are copied to our state
// first, because the Kindle empties fmcache.db soon after.
type RateSync struct {
	S         *Syncer
	Books     BookByKey
	Path      string // fmcache.db
	StatePath string
	// OnTap is called with new star taps as soon as they are read.
	OnTap func(taps []metrics.Rating)
	// OnResult reports what happened to a tap (daemon: tell the user; the
	// Kindle shows a Goodreads error for it). Called once per tap.
	OnResult func(title string, stars float64, res RateResult)
	// OnReread is called when a shelf choice starts a re-read (daemon: clear
	// its "finished" mark for the book).
	OnReread func(key string)
	// SyncShelves sends the Goodreads shelf choice from the end-of-book
	// dialog as a Hardcover status. Off in the first release (user decision
	// 2026-09-28: default logic only); the code stays for a later release.
	SyncShelves bool

	DB *store.Store // optional state database (else StatePath JSON)

	pend  *store.Table[metrics.Rating]
	mu    sync.Mutex // guards st, told
	st    *RateState
	told  map[string]RateResult // last result reported per tap
	runMu sync.Mutex            // one Run at a time
}

// RateResult is the outcome of one star tap.
type RateResult int

const (
	RateSaved    RateResult = iota // saved on Hardcover
	RateNotFound                   // book not found: not saved
	RateQueued                     // no network or sign-in: sent later
)

// researchSchemas are logged in full to learn their format (shelf choice,
// dialogs). Rating records are handled on their own.
var researchSchemas = []string{"goodreads", "ereader_dialog", "eink_end_actions"}

// quietSchemas are known and not useful in the log (seen on device).
var quietSchemas = map[string]bool{
	"goodreads_eink_availability":           true,
	"goodreads_eink_error_count_with_label": true,
	"eink_end_actions_class_instance":       true,
	"eink_end_actions_general":              true,
	"goodreads_eink_count":                  true,
	"goodreads_eink_latency":                true,
	"ereader_dialog_display_metrics":        true,
}

// Collect copies new taps and shelf choices from fmcache.db into the state.
// It makes no network calls and never waits for a send, so the file watcher
// can call it at once (the Kindle empties fmcache.db soon). It returns the
// number of new items.
func (r *RateSync) Collect(ctx context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.collect(ctx, r.state())
}

// Run collects new taps, then sends pending ones. Only the daemon's scan
// loop calls Run, so Hardcover writes never overlap. The state lock is held
// only for short changes, never during a network call.
func (r *RateSync) Run(ctx context.Context) (sent int, err error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	r.mu.Lock()
	st := r.state()
	_, err = r.collect(ctx, st)
	if !r.SyncShelves {
		clear(st.Shelves) // choices saved by an older version
	}
	shelves := make(map[string]int, len(st.Shelves))
	for k, v := range st.Shelves {
		shelves[k] = v
	}
	pending := make([]metrics.Rating, 0, len(st.Pending))
	for _, x := range st.Pending {
		pending = append(pending, x)
	}
	r.mu.Unlock()
	if err != nil {
		return 0, err
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedMS < pending[j].CreatedMS })

	// done removes an item that was sent, unless a newer tap replaced it
	// meanwhile, and saves.
	done := func(drop func(*RateState)) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		drop(r.st)
		return r.save(r.st)
	}
	for k, status := range shelves {
		if err := r.sendShelf(ctx, k, status); err != nil {
			return sent, err
		}
		sent++
		if err := done(func(st *RateState) {
			if st.Shelves[k] == status {
				delete(st.Shelves, k)
			}
		}); err != nil {
			return sent, err
		}
	}
	for _, x := range pending {
		saved, err := r.sendRating(ctx, x)
		if saved {
			sent++ // only ratings really saved (not ignored or not found)
		}
		if err != nil {
			return sent, err // keep pending, retry later (e.g. no Wi-Fi)
		}
		if err := done(func(st *RateState) {
			if st.Pending[x.BookKey].CreatedMS == x.CreatedMS {
				delete(st.Pending, x.BookKey)
			}
		}); err != nil {
			return sent, err
		}
	}
	return sent, nil
}

// state returns the state, loaded once. The caller holds r.mu.
func (r *RateSync) state() *RateState {
	if r.st == nil {
		r.st = r.load()
	}
	return r.st
}

func (r *RateSync) collect(ctx context.Context, st *RateState) (n int, err error) {
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
			r.S.logf("ratings: tap %g stars on book %s (%s, %s, %s)", x.Stars, short8(x.BookKey), x.Context, x.Action, x.Event)
		}
		if len(rs) > 0 && r.OnTap != nil {
			r.OnTap(rs)
		}
		other, err := metrics.Records(ctx, r.Path, st.OtherMS, researchSchemas...)
		if err != nil {
			r.S.logf("ratings: read records %v", err)
		}
		for _, x := range other {
			if x.CreatedMS > st.OtherMS {
				st.OtherMS = x.CreatedMS
			}
			if x.Schema == "goodreads_book_ratings" {
				continue
			}
			if !quietSchemas[x.Schema] {
				r.S.logf("research: %s %s", x.Schema, x.JSON)
			}
			if status, key, raw := metrics.ShelfChoice(x.JSON); status != metrics.ShelfNone && key != "" {
				if !r.SyncShelves {
					r.S.logf("shelf: book %s → %q ignored (shelf sync is off)", short8(key), raw)
					continue
				}
				st.Shelves[key] = status
				n++
				r.S.logf("shelf: book %s → %q (Hardcover status %d)", short8(key), raw, status)
			}
		}
		n += len(rs)
		if len(rs) > 0 || len(other) > 0 {
			if err := r.save(st); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

func (r *RateSync) sendRating(ctx context.Context, x metrics.Rating) (saved bool, err error) {
	if x.Stars <= 0 || x.Stars > 5 {
		r.S.logf("ratings: book %s: rating %.1f ignored", short8(x.BookKey), x.Stars)
		return false, nil
	}
	local, lerr := r.Books.BookByKey(ctx, x.BookKey)
	if lerr != nil {
		r.S.logf("ratings: book %s not in cc.db, dropped", short8(x.BookKey))
		return false, nil
	}
	err = r.rate(ctx, local, x)
	switch {
	case err == nil:
		r.report(local.Title, x, RateSaved)
	case errors.Is(err, ErrNotFound):
		r.S.logf("ratings: %q not found on Hardcover, dropped", local.Title)
		r.report(local.Title, x, RateNotFound)
		return false, nil
	case Waiting(err):
		r.report(local.Title, x, RateQueued)
	}
	return err == nil, err
}

// report calls OnResult once per tap and result: a tap first reported as
// "sent later" (offline) is reported again with its final result.
func (r *RateSync) report(title string, x metrics.Rating, res RateResult) {
	if r.OnResult == nil {
		return
	}
	id := x.BookKey + "@" + strconv.FormatInt(x.CreatedMS, 10)
	r.mu.Lock()
	if r.told == nil {
		r.told = map[string]RateResult{}
	}
	prev, seen := r.told[id]
	r.told[id] = res
	r.mu.Unlock()
	if !seen || prev != res {
		r.OnResult(title, x.Stars, res)
	}
}

func (r *RateSync) rate(ctx context.Context, local *book.Local, x metrics.Rating) error {
	res, ub, err := r.S.Identify(ctx, local)
	if err != nil {
		return err
	}
	if ub == nil {
		// Not on the shelves yet: a rated book is usually finished, so
		// finish it (adds it too); else add it as Currently Reading.
		if IsFinished(local.Percent, local.ReadState) {
			_, nub, err := r.S.finish(ctx, local, res, nil)
			if err != nil {
				return err
			}
			ub = nub
		} else if ub, err = r.S.addUserBook(ctx, res, hardcover.StatusReading); err != nil {
			return err
		}
		if ub == nil {
			r.S.logf("ratings: %q could not be added", local.Title)
			return ErrNotFound
		}
	}
	if _, err := r.S.C.SetRating(ctx, ub.ID, x.Stars); err != nil {
		return err
	}
	r.S.logf("ratings: %q rated %g stars (user_book %d)", local.Title, x.Stars, ub.ID)
	return nil
}

// sendShelf applies a Goodreads shelf choice from the end-of-book dialog.
// Read uses the normal finish flow (never a second finished read the same day).
func (r *RateSync) sendShelf(ctx context.Context, key string, status int) error {
	local, err := r.Books.BookByKey(ctx, key)
	if err != nil {
		r.S.logf("shelf: book %s not in cc.db, dropped", short8(key))
		return nil
	}
	res, ub, err := r.S.Identify(ctx, local)
	if errors.Is(err, ErrNotFound) {
		r.S.logf("shelf: %q not found on Hardcover, dropped", local.Title)
		return nil
	}
	if err != nil {
		return err
	}
	if status == metrics.ShelfRead {
		out, _, err := r.S.finish(ctx, local, res, ub)
		if err == nil {
			r.S.logf("shelf: %q → Read (%v)", local.Title, out.Kind)
		}
		return err
	}
	if status == metrics.ShelfReading && (ub == nil || ub.StatusID == hardcover.StatusRead) {
		// "Currently Reading" at the end of a book = re-read (user decision).
		if _, _, err := r.S.Reread(ctx, local, res, ub, 0); err != nil {
			return err
		}
		if r.OnReread != nil {
			r.OnReread(key)
		}
		r.S.logf("shelf: %q → Currently Reading (re-read)", local.Title)
		return nil
	}
	// Forward only (like progress): Want to Read → Currently Reading is an
	// upgrade; anything else that would move the book back, or override
	// Paused / Did Not Finish, is not sent.
	switch {
	case ub == nil:
		if _, err := r.S.addUserBook(ctx, res, status); err != nil {
			return err
		}
	case ub.StatusID == status:
	case status == hardcover.StatusReading && ub.StatusID == hardcover.StatusWantToRead:
		if _, err := r.S.C.SetStatus(ctx, ub.ID, status); err != nil {
			return err
		}
	default:
		r.S.logf("shelf: %q: %s → %s not sent (forward only)", local.Title, StatusName(ub.StatusID), StatusName(status))
		return nil
	}
	r.S.logf("shelf: %q → %s", local.Title, StatusName(status))
	return nil
}

func short8(k string) string {
	if len(k) > 8 {
		return k[:8]
	}
	return k
}

const kvRatings = "ratings.cursors"

// rateCursors is the small part of RateState kept in kv.
type rateCursors struct {
	LastMS  int64 `json:"last_ms"`
	OtherMS int64 `json:"other_ms"`
}

func (r *RateSync) load() *RateState {
	st := &RateState{}
	if r.DB != nil {
		r.pend = store.NewTable[metrics.Rating](r.DB, store.TRatings)
		_ = r.DB.ImportJSON("ratings.json", r.StatePath, func(b []byte) error {
			var old RateState
			if json.Unmarshal(b, &old) != nil {
				return nil
			}
			if err := r.pend.Save(old.Pending); err != nil {
				return err
			}
			return r.DB.SetJSON(kvRatings, rateCursors{old.LastMS, old.OtherMS})
		})
		var c rateCursors
		r.DB.GetJSON(kvRatings, &c)
		st.LastMS, st.OtherMS = c.LastMS, c.OtherMS
		st.Pending, _ = r.pend.Load()
	} else if b, err := os.ReadFile(r.StatePath); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Pending == nil {
		st.Pending = map[string]metrics.Rating{}
	}
	if st.Shelves == nil {
		st.Shelves = map[string]int{}
	}
	return st
}

// save writes the state; with the DB, the changed taps and the cursors in
// one transaction. Shelf choices (sync off in this release) are not kept.
func (r *RateSync) save(st *RateState) error {
	if r.DB == nil {
		return atomicfile.GuardedJSON(r.StatePath, st, 0o600)
	}
	ops, commit, err := r.pend.Diff(st.Pending)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(rateCursors{st.LastMS, st.OtherMS})
	if err := r.DB.Apply(append(ops, store.SetOp(kvRatings, string(b)))...); err != nil {
		return err
	}
	commit()
	return nil
}
