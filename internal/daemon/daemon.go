// Package daemon finds books whose progress changed in cc.db and syncs them.
//
// It keeps a snapshot of every book's percent. On each scan it compares the
// DB with the snapshot, syncs changed books (oldest access first), and keeps
// failed ones as pending for the next trigger (e.g. Wi-Fi back).
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/readers"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/syncer"
)

// Source is the local reading state (cc.db).
type Source interface {
	AllProgress(ctx context.Context) (map[string]readers.Progress, error)
	BookByKey(ctx context.Context, key string) (*book.Local, error)
}

// Syncer sends one book.
type Syncer interface {
	Sync(ctx context.Context, local *book.Local) (syncer.Outcome, error)
}

// State is saved to disk between runs.
type State struct {
	Snapshot   map[string]readers.Progress `json:"snapshot"`
	Pending    map[string]int              `json:"pending"` // key → failed attempts
	LastScan   time.Time                   `json:"last_scan"`
	LastResult string                      `json:"last_result"`
}

// Daemon runs scans.
type Daemon struct {
	Src         Source
	Sync        Syncer
	StatePath   string
	Logf        func(string, ...any)
	MaxAttempts int // give up on one percent value after this many errors
	// After runs at the end of each scan (e.g. highlight/note sync).
	After func(ctx context.Context)

	mu    sync.Mutex
	state *State
}

// LoadState reads the state file (empty state if missing).
func LoadState(path string) (*State, error) {
	st := &State{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return &State{}, nil // corrupt: start again
	}
	return st, nil
}

func (d *Daemon) save() error {
	b, err := json.MarshalIndent(d.state, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.StatePath), 0o700); err != nil {
		return err
	}
	tmp := d.StatePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.StatePath)
}

// Scan compares cc.db with the snapshot and syncs changed books.
func (d *Daemon) Scan(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state == nil {
		st, err := LoadState(d.StatePath)
		if err != nil {
			return err
		}
		d.state = st
	}
	st := d.state
	if st.Pending == nil {
		st.Pending = map[string]int{}
	}
	maxAttempts := d.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	cur, err := d.Src.AllProgress(ctx)
	if err != nil {
		return err
	}

	changed := map[string]bool{}
	if st.Snapshot == nil {
		// First run: take a baseline. Sync only the most recent book.
		st.Snapshot = map[string]readers.Progress{}
		for k, p := range cur {
			st.Snapshot[k] = p
		}
		if k := latest(cur); k != "" && cur[k].Percent > 0 {
			changed[k] = true
		}
		d.Logf("daemon: first run, baseline of %d books", len(cur))
	} else {
		for k, p := range cur {
			old, ok := st.Snapshot[k]
			if (!ok && (p.Percent > 0 || p.ReadState == 2)) ||
				(ok && (p.Percent != old.Percent || p.ReadState != old.ReadState)) {
				changed[k] = true
			}
			if !ok && p.Percent <= 0 && p.ReadState != 2 {
				st.Snapshot[k] = p
			}
		}
	}
	for k := range st.Pending {
		if _, ok := cur[k]; ok {
			changed[k] = true
		} else {
			delete(st.Pending, k) // book removed from the Kindle
		}
	}

	keys := make([]string, 0, len(changed))
	for k := range changed {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return cur[keys[i]].LastAccess < cur[keys[j]].LastAccess })

	for _, k := range keys {
		if ctx.Err() != nil {
			break
		}
		p := cur[k]
		local, err := d.Src.BookByKey(ctx, k)
		if err != nil {
			d.Logf("daemon: book %s: %v", short(k), err)
			st.Snapshot[k] = p
			continue
		}
		d.Logf("daemon: %q changed to %.2f%%", local.Title, p.Percent)
		out, err := d.Sync.Sync(ctx, local)
		if err != nil {
			n := st.Pending[k] + 1
			if n >= maxAttempts {
				d.Logf("daemon: %q: error (%v), attempt %d, giving up until the percent changes", local.Title, err, n)
				delete(st.Pending, k)
				st.Snapshot[k] = p
			} else {
				d.Logf("daemon: %q: error (%v), attempt %d, will retry", local.Title, err, n)
				st.Pending[k] = n
			}
			st.LastResult = "error: " + err.Error()
			continue
		}
		delete(st.Pending, k)
		st.Snapshot[k] = p
		st.LastResult = describe(out)
		d.Logf("daemon: %q: %s", local.Title, st.LastResult)
	}
	st.LastScan = time.Now()
	err = d.save()
	if d.After != nil && ctx.Err() == nil {
		d.After(ctx)
	}
	return err
}

// Status returns a copy of the state (after at least one scan or load).
func (d *Daemon) Status() State {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state == nil {
		return State{}
	}
	return *d.state
}

func describe(o syncer.Outcome) string {
	s := ""
	switch o.Kind {
	case syncer.Sent:
		s = "synced page " + itoa(o.Page) + "/" + itoa(o.Pages)
	case syncer.Unchanged:
		s = "already at page " + itoa(o.Page)
	case syncer.Skipped:
		s = "not sent: " + o.Reason
	}
	if o.Finished && o.Kind == syncer.Sent {
		s = "finished: status Read"
	}
	if o.Added != "" {
		s = o.Added + ", " + s
	}
	return s
}

func latest(m map[string]readers.Progress) string {
	best, bestT := "", int64(-1)
	for k, p := range m {
		if p.LastAccess > bestT {
			best, bestT = k, p.LastAccess
		}
	}
	return best
}

func short(k string) string {
	if len(k) > 8 {
		return k[:8]
	}
	return k
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
