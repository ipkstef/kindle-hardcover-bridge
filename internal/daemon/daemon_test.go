package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/readers"
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
	return &book.Local{Key: k, Title: k, Percent: f.m[k].Percent}, nil
}

type fakeSync struct {
	calls    []string
	fail     bool
	finished bool
}

func (f *fakeSync) Sync(_ context.Context, l *book.Local) (syncer.Outcome, error) {
	f.calls = append(f.calls, l.Key)
	if f.fail {
		return syncer.Outcome{}, errors.New("network down")
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
