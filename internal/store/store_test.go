package store

import (
	"os"
	"path/filepath"
	"testing"
)

type row struct {
	A int    `json:"a"`
	B string `json:"b"`
}

func TestTableDiff(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tb := NewTable[row](s, TProgress)
	if m, err := tb.Load(); err != nil || len(m) != 0 {
		t.Fatal(m, err)
	}
	m := map[string]row{"x": {1, "a"}, "y": {2, "b"}}
	if err := tb.Save(m); err != nil {
		t.Fatal(err)
	}
	// Unchanged: no writes.
	ops, _, _ := tb.Diff(m)
	if len(ops) != 0 {
		t.Fatalf("unchanged map gave %d ops", len(ops))
	}
	// One change, one delete.
	m["x"] = row{3, "c"}
	delete(m, "y")
	ops, _, _ = tb.Diff(m)
	if len(ops) != 2 {
		t.Fatalf("got %d ops", len(ops))
	}
	if err := tb.Save(m); err != nil {
		t.Fatal(err)
	}
	got, _ := NewTable[row](s, TProgress).Load()
	if len(got) != 1 || got["x"].A != 3 {
		t.Fatalf("reload: %v", got)
	}
	if !s.Has(TProgress, "x") || s.Has(TProgress, "y") || s.Count(TProgress) != 1 {
		t.Fatal("Has/Count")
	}
}

func TestKVAndImport(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJSON("c", map[string]int{"n": 5}); err != nil {
		t.Fatal(err)
	}
	var v map[string]int
	if !s.GetJSON("c", &v) || v["n"] != 5 {
		t.Fatal(v)
	}
	f := filepath.Join(dir, "old.json")
	os.WriteFile(f, []byte(`{"x":1}`), 0o600)
	calls := 0
	imp := func([]byte) error { calls++; return nil }
	if err := s.ImportJSON("old", f, imp); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportJSON("old", f, imp); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("import ran %d times", calls)
	}
	if _, err := os.Stat(f + ".migrated"); err != nil {
		t.Fatal("file not renamed")
	}
	s.Close()
	// Reopen: data kept.
	s, _ = Open(filepath.Join(dir, "s.db"))
	defer s.Close()
	if !s.GetJSON("c", &v) || v["n"] != 5 {
		t.Fatal("not kept")
	}
}
