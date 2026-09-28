package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/hardcover"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/readers"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/store"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/syncer"
)

func pr(pct float64, last int64) readers.Progress {
	return readers.Progress{Percent: pct, LastAccess: last}
}

type fakeSrc struct{ m map[string]readers.Progress }

func (f *fakeSrc) AllProgress(context.Context) (map[string]readers.Progress, error) {
	out := map[string]readers.Progress{}
	for k, v := range f.m {
		out[k] = v
	}
	return out, nil
}

func (f *fakeSrc) BookByKey(_ context.Context, k string) (*book.Local, error) {
	return &book.Local{Key: k, Title: k, Percent: f.m[k].Percent, ReadState: f.m[k].ReadState}, nil
}

type fakeSync struct {
	calls      []string
	readStates []int
	fail       bool
	offline    bool
	finished   bool
}

func (f *fakeSync) Sync(_ context.Context, l *book.Local) (syncer.Outcome, error) {
	f.calls = append(f.calls, l.Key)
	f.readStates = append(f.readStates, l.ReadState)
	if f.fail {
		return syncer.Outcome{}, errors.New("graphql: bad request")
	}
	if f.offline {
		return syncer.Outcome{}, fmt.Errorf("%w: dial tcp: no route", hardcover.ErrTransient)
	}
	return syncer.Outcome{Kind: syncer.Sent, Page: 1, Pages: 10, Finished: f.finished}, nil
}

func newD(t *testing.T, src *fakeSrc, s *fakeSync) *Daemon {
	return &Daemon{Src: src, Sync: s, StatePath: filepath.Join(t.TempDir(), "state.json"),
		Logf: t.Logf, MaxAttempts: 3}
}

func TestFirstRunSyncsOnlyLatest(t *testing.T) {
	src := &fakeSrc{m: map[string]readers.Progress{
		"a": {Percent: 10, LastAccess: 100}, "b": {Percent: 50, LastAccess: 300}, "c": {Percent: 0, LastAccess: 400},
	}}
	s := &fakeSync{}
	d := newD(t, src, s)
	if err := d.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	// c is newest but has 0 %, so nothing for it; b is not the newest → no sync.
	if len(s.calls) != 0 {
		t.Fatalf("calls %v", s.calls)
	}
	src.m["c"] = readers.Progress{Percent: 5, LastAccess: 500}
	d.Scan(context.Background())
	if len(s.calls) != 1 || s.calls[0] != "c" {
		t.Fatalf("calls %v", s.calls)
	}
}

func TestChangedBooksOldestFirstAndNoRepeat(t *testing.T) {
	src := &fakeSrc{m: map[string]readers.Progress{"a": pr(10, 100), "b": pr(20, 200)}}
	s := &fakeSync{}
	d := newD(t, src, s)
	d.Scan(context.Background()) // baseline + latest (b)
	s.calls = nil
	// User read a, went Home, then opened b and went Home.
	src.m["a"] = readers.Progress{Percent: 12, LastAccess: 300}
	src.m["b"] = readers.Progress{Percent: 22, LastAccess: 400}
	d.Scan(context.Background())
	if len(s.calls) != 2 || s.calls[0] != "a" || s.calls[1] != "b" {
		t.Fatalf("calls %v", s.calls)
	}
	s.calls = nil
	d.Scan(context.Background())
	if len(s.calls) != 0 {
		t.Fatalf("repeat calls %v", s.calls)
	}
}

func TestRetryThenGiveUp(t *testing.T) {
	src := &fakeSrc{m: map[string]readers.Progress{"a": pr(10, 100)}}
	s := &fakeSync{fail: true}
	d := newD(t, src, s)
	for i := 0; i < 5; i++ {
		d.Scan(context.Background())
	}
	if len(s.calls) != 3 { // MaxAttempts
		t.Fatalf("calls %d", len(s.calls))
	}
	// Percent changes → try again, and Wi-Fi is back.
	s.fail, s.calls = false, nil
	src.m["a"] = readers.Progress{Percent: 11, LastAccess: 200}
	d.Scan(context.Background())
	if len(s.calls) != 1 {
		t.Fatalf("calls %v", s.calls)
	}
}

func TestStatePersists(t *testing.T) {
	src := &fakeSrc{m: map[string]readers.Progress{"a": pr(10, 100)}}
	s := &fakeSync{}
	d := newD(t, src, s)
	d.Scan(context.Background())
	// New daemon (restart), change made while it was down.
	src.m["a"] = readers.Progress{Percent: 15, LastAccess: 200}
	d2 := &Daemon{Src: src, Sync: s, StatePath: d.StatePath, Logf: t.Logf}
	s.calls = nil
	d2.Scan(context.Background())
	if len(s.calls) != 1 {
		t.Fatalf("calls %v", s.calls)
	}
}

// Device log 2026-09-28: finished at 100 %, user paged back to 98.22 % with
// read state still 2 → must not finish a second time.
func TestFinishOnlyOnce(t *testing.T) {
	src := &fakeSrc{m: map[string]readers.Progress{"a": pr(50, 100)}}
	s := &fakeSync{}
	d := newD(t, src, s)
	d.Scan(context.Background()) // baseline, syncs a
	s.calls, s.finished = nil, true
	src.m["a"] = readers.Progress{Percent: 100, LastAccess: 200, ReadState: 2}
	d.Scan(context.Background())
	if len(s.calls) != 1 {
		t.Fatalf("finish calls %v", s.calls)
	}
	s.calls = nil
	src.m["a"] = readers.Progress{Percent: 98.22, LastAccess: 300, ReadState: 2}
	d.Scan(context.Background())
	if len(s.calls) != 0 {
		t.Fatalf("finished again: %v", s.calls)
	}
	// Restart: back to the start → normal syncing again.
	s.finished = false
	src.m["a"] = readers.Progress{Percent: 1.2, LastAccess: 400}
	d.Scan(context.Background())
	if len(s.calls) != 1 {
		t.Fatalf("after restart: %v", s.calls)
	}
}

// Device log 2026-09-28: after a finish the Kindle keeps read state 2 when the
// user goes back to 8 %. A steady 2 must not reach the syncer as "finished".
func TestSteadyReadStateNotFinish(t *testing.T) {
	src := &fakeSrc{m: map[string]readers.Progress{"a": {Percent: 100, LastAccess: 100, ReadState: 2}}}
	s := &fakeSync{}
	d := newD(t, src, s)
	d.Scan(context.Background()) // baseline + latest
	s.calls, s.readStates = nil, nil
	src.m["a"] = readers.Progress{Percent: 8.39, LastAccess: 200, ReadState: 2}
	d.Scan(context.Background())
	if len(s.readStates) != 1 || s.readStates[0] != 0 {
		t.Fatalf("read states %v", s.readStates)
	}
	// A change to 2 is passed on.
	src.m["b"] = readers.Progress{Percent: 60, LastAccess: 300}
	d.Scan(context.Background())
	s.readStates = nil
	src.m["b"] = readers.Progress{Percent: 60, LastAccess: 400, ReadState: 2}
	d.Scan(context.Background())
	if len(s.readStates) != 1 || s.readStates[0] != 2 {
		t.Fatalf("change to 2: %v", s.readStates)
	}
}

// Offline errors never use up the attempts: the book stays pending until the
// network is back.
func TestOfflineNeverGivesUp(t *testing.T) {
	src := &fakeSrc{m: map[string]readers.Progress{"a": pr(10, 100)}}
	s := &fakeSync{}
	d := newD(t, src, s)
	d.Scan(context.Background())
	src.m["a"] = pr(20, 200)
	s.offline = true
	for i := 0; i < 10; i++ {
		d.Scan(context.Background())
	}
	s.offline, s.calls = false, nil
	d.Scan(context.Background())
	if len(s.calls) != 1 {
		t.Fatalf("after network back: calls %v", s.calls)
	}
	s.calls = nil
	d.Scan(context.Background())
	if len(s.calls) != 0 {
		t.Fatalf("sent again: %v", s.calls)
	}
}

// ClearFinished from another goroutine during a scan must not block (it used
// to wait for the scan lock while the scan waited for the caller's lock).
func TestClearFinishedDoesNotBlock(t *testing.T) {
	src := &fakeSrc{m: map[string]readers.Progress{"a": pr(10, 100)}}
	d := newD(t, src, &fakeSync{})
	done := make(chan struct{})
	d.After = func(context.Context) {
		d.mu.Lock() // the scan lock must be free here
		d.mu.Unlock()
	}
	d.mu.Lock()
	go func() { d.ClearFinished("a"); close(done) }()
	<-done
	d.mu.Unlock()
	if err := d.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A scan with no change does not write the state file again.
func TestNoWriteWhenUnchanged(t *testing.T) {
	src := &fakeSrc{m: map[string]readers.Progress{"a": pr(10, 100)}}
	d := newD(t, src, &fakeSync{})
	d.Scan(context.Background())
	if err := os.Remove(d.StatePath); err != nil {
		t.Fatal(err)
	}
	d.Scan(context.Background())
	if _, err := os.Stat(d.StatePath); err == nil {
		t.Fatal("unchanged state was written again")
	}
	src.m["a"] = pr(20, 200)
	d.Scan(context.Background())
	if _, err := os.Stat(d.StatePath); err != nil {
		t.Fatal("changed state not written")
	}
}

// A book deleted from the Kindle is removed from the state.
func TestDeletedBookForgotten(t *testing.T) {
	src := &fakeSrc{m: map[string]readers.Progress{"a": pr(10, 100), "b": pr(20, 200)}}
	d := newD(t, src, &fakeSync{})
	d.Scan(context.Background())
	delete(src.m, "a")
	d.Scan(context.Background())
	if _, ok := d.state.Snapshot["a"]; ok || len(d.state.Snapshot) != 1 {
		t.Fatalf("snapshot %v", d.state.Snapshot)
	}
}

// With the state database: state survives a restart, and the old
// state.json is imported once.
func TestDBStateRestartAndImport(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "state.json")
	os.WriteFile(jsonPath, []byte(`{"snapshot":{"a":{"percent":10,"last_access":100}},"pending":{},"finished":{"b":"2026-09-01"}}`), 0o600)
	db, err := store.Open(filepath.Join(dir, "hcbridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src := &fakeSrc{m: map[string]readers.Progress{"a": pr(10, 100), "b": pr(100, 50)}}
	s := &fakeSync{}
	d := &Daemon{Src: src, Sync: s, StatePath: jsonPath, DB: db, Logf: t.Logf}
	d.Scan(context.Background())
	if len(s.calls) != 0 {
		t.Fatalf("imported snapshot not used: %v", s.calls)
	}
	if _, err := os.Stat(jsonPath + ".migrated"); err != nil {
		t.Fatal("state.json not migrated")
	}
	src.m["a"] = pr(20, 200)
	d.Scan(context.Background())
	// Restart: a new daemon on the same DB sees no change.
	s2 := &fakeSync{}
	d2 := &Daemon{Src: src, Sync: s2, StatePath: jsonPath, DB: db, Logf: t.Logf}
	d2.Scan(context.Background())
	if len(s2.calls) != 0 {
		t.Fatalf("after restart: %v", s2.calls)
	}
	if d2.state.Finished["b"] != "2026-09-01" {
		t.Fatalf("finished lost: %v", d2.state.Finished)
	}
}
