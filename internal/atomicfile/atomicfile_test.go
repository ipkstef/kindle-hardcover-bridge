package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "a.json")
	if err := WriteJSON(p, map[string]int{"a": 1}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "new" {
		t.Fatalf("got %q, %v", b, err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*.tmp*"))
	if len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestGuardedLowSpace(t *testing.T) {
	old := MinFree
	defer func() { MinFree = old }()
	MinFree = 1 << 62 // more than any disk
	p := filepath.Join(t.TempDir(), "s.json")
	if err := GuardedJSON(p, 1, 0o600); !errors.Is(err, ErrLowSpace) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("file written")
	}
	MinFree = 0
	if err := GuardedJSON(p, 1, 0o600); err != nil {
		t.Fatal(err)
	}
}
