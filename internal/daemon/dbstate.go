package daemon

import (
	"encoding/json"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/readers"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/store"
)

// dbState keeps State in the store: one row per book in progress, pending
// and finished; scan time and result in kv. Only changed rows are written.
type dbState struct {
	s        *store.Store
	progress *store.Table[readers.Progress]
	pending  *store.Table[int]
	finished *store.Table[string]
}

const (
	kvBaseline   = "daemon.baseline" // set after the first scan (Snapshot not nil)
	kvLastScan   = "daemon.last_scan"
	kvLastResult = "daemon.last_result"
)

func newDBState(s *store.Store) *dbState {
	return &dbState{s: s,
		progress: store.NewTable[readers.Progress](s, store.TProgress),
		pending:  store.NewTable[int](s, store.TPending),
		finished: store.NewTable[string](s, store.TFinished)}
}

// load reads the state; on first use it imports the old state.json once.
func (b *dbState) load(jsonPath string) (*State, error) {
	err := b.s.ImportJSON("state.json", jsonPath, func(raw []byte) error {
		var st State
		if json.Unmarshal(raw, &st) != nil {
			return nil // corrupt old file: start fresh
		}
		return b.save(&st)
	})
	if err != nil {
		return nil, err
	}
	st := &State{}
	if _, ok := b.s.Get(kvBaseline); ok {
		if st.Snapshot, err = b.progress.Load(); err != nil {
			return nil, err
		}
	} else {
		b.progress.Load() // prime the diff copy
	}
	if st.Pending, err = b.pending.Load(); err != nil {
		return nil, err
	}
	if st.Finished, err = b.finished.Load(); err != nil {
		return nil, err
	}
	if v, ok := b.s.Get(kvLastScan); ok {
		st.LastScan, _ = time.Parse(time.RFC3339, v)
	}
	st.LastResult, _ = b.s.Get(kvLastResult)
	return st, nil
}

// save writes the changed rows and the scalars in one transaction.
func (b *dbState) save(st *State) error {
	var ops []store.Op
	var commits []func()
	add := func(o []store.Op, c func(), err error) error {
		if err != nil {
			return err
		}
		ops = append(ops, o...)
		commits = append(commits, c)
		return nil
	}
	if st.Snapshot != nil {
		if err := add(b.progress.Diff(st.Snapshot)); err != nil {
			return err
		}
		ops = append(ops, store.SetOp(kvBaseline, "1"))
	}
	if err := add(b.pending.Diff(st.Pending)); err != nil {
		return err
	}
	if err := add(b.finished.Diff(st.Finished)); err != nil {
		return err
	}
	ops = append(ops, store.SetOp(kvLastScan, st.LastScan.UTC().Format(time.RFC3339)),
		store.SetOp(kvLastResult, st.LastResult))
	if err := b.s.Apply(ops...); err != nil {
		return err
	}
	for _, c := range commits {
		c()
	}
	return nil
}

// ReadStatus reads the scan time, last result and pending count from the
// store, for the status screen (another process; WAL allows it).
func ReadStatus(s *store.Store) (last time.Time, result string, pending int) {
	if v, ok := s.Get(kvLastScan); ok {
		last, _ = time.Parse(time.RFC3339, v)
	}
	result, _ = s.Get(kvLastResult)
	return last, result, s.Count(store.TPending)
}
