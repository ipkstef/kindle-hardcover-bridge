package sidecar

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// Real .azw3f from the user's Kindle (FW 5.17.1), after the 2nd sleep in the
// sleep research. It holds only reading positions and timers, no title.
const realAZW3F = "00000000001ab1260200000000000000010100000006fe00000b74696d65722e6d6f64656c020000000000000000020000000000001a4602000000000000001d043f350c89f83058c0fe00001874696d65722e617665726167652e63616c63756c61746f7201000000010440702b2955a34864010000000001000000000100000000fffffe00000366707203000005363830383902ffffffffffffffff02ffffffffffffffff03010301fffe00000f626f6f6b2e696e666f2e73746f7265020000000000000303043f78567f86f7e6b0fffe000012706167652e686973746f72792e73746f72650100000000fffe00001d7768697370657273746f72652e6d6967726174696f6e2e73746174757300000000fffe0000036c7072070203000005363830383902000001a0e637e2aaff"

func TestRealAZW3F(t *testing.T) {
	b, _ := hex.DecodeString(realAZW3F)
	d, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"timer.model", "timer.average.calculator", "fpr", "book.info.store", "page.history.store", "whisperstore.migration.status", "lpr"} {
		if d.Find(name) == nil {
			t.Errorf("object %q not found", name)
		}
	}
	p, ok := FromDoc(d)
	if !ok || p.LPRPos != 68089 || p.FPRPos != 68089 {
		t.Fatalf("got %+v", p)
	}
	// Save time matches cc.db p_lastAccess 1790568817 (same moment).
	if p.Saved.Unix() != 1790568817 {
		t.Errorf("saved %v (%d)", p.Saved, p.Saved.Unix())
	}
}

func TestForBook(t *testing.T) {
	dir := t.TempDir()
	book := filepath.Join(dir, "Some Book.azw3")
	sdr := SdrDir(book)
	os.MkdirAll(sdr, 0o755)
	b, _ := hex.DecodeString(realAZW3F)
	os.WriteFile(filepath.Join(sdr, "Some Book123.azw3f"), b, 0o644)
	os.WriteFile(filepath.Join(sdr, "Some Book123.apnx"), []byte("not krds"), 0o644)
	os.WriteFile(filepath.Join(sdr, "Some Book123.azw3f.tmp"), []byte("partial"), 0o644)
	p, err := ForBook(book)
	if err != nil || p.LPRPos != 68089 {
		t.Fatalf("got %+v %v", p, err)
	}
}

func TestNotKRDS(t *testing.T) {
	if _, err := Parse([]byte("hello")); err != ErrNotKRDS {
		t.Fatal(err)
	}
	b, _ := hex.DecodeString(realAZW3F)
	if _, err := Parse(b[:100]); err == nil {
		t.Fatal("truncated data accepted")
	}
}

func TestNumberWithFields(t *testing.T) {
	if n := number("224199:224198:83:REFUQQAAAHxFQkFS"); n != 224199 {
		t.Fatalf("got %d", n)
	}
	if n := number("68089"); n != 68089 {
		t.Fatalf("got %d", n)
	}
	if n := number("abc"); n != -1 {
		t.Fatalf("got %d", n)
	}
}
