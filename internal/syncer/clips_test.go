package syncer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/clippings"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/hardcover"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/store"
)

type fakeBooks struct{}

func (fakeBooks) BooksByTitle(_ context.Context, title string) ([]*book.Local, error) {
	if title != redRising.Title {
		return nil, nil
	}
	l := redRising
	return []*book.Local{&l}, nil
}

func TestClipSync(t *testing.T)   { testClipSync(t, false) }
func TestClipSyncDB(t *testing.T) { testClipSync(t, true) }

func testClipSync(t *testing.T, useDB bool) {
	dir := t.TempDir()
	path := filepath.Join(dir, "My Clippings.txt")
	dev, err := os.ReadFile("../clippings/testdata/My Clippings.txt")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, dev, 0o644)

	f := newFake(2)
	cs := &ClipSync{S: &Syncer{C: f.client(t), Logf: t.Logf}, Books: fakeBooks{},
		Path: path, StatePath: filepath.Join(dir, "clips.json")}
	if useDB {
		db, err := store.Open(filepath.Join(dir, "hcbridge.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		cs.DB = db
	}

	// First run: baseline only, nothing sent.
	if n, err := cs.Run(context.Background(), false); err != nil || n != 0 || len(f.ops) != 0 {
		t.Fatalf("baseline: %d %v %v", n, err, f.ops)
	}
	// A new highlight is added on the Kindle.
	add := "\ufeffRed Rising (The Red Rising Trilogy, Book 1) (Pierce Brown)\r\n" +
		"- Your Highlight on page 50 | Location 700-701 | Added on Tuesday, September 29, 2026 9:00:00 PM\r\n\r\nNew line.\r\n==========\r\n"
	fh, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	fh.WriteString(add)
	fh.Close()
	os.Chtimes(path, time.Now(), time.Now().Add(time.Second))

	n, err := cs.Run(context.Background(), false)
	if err != nil || n != 1 {
		t.Fatalf("new clip: %d %v", n, err)
	}
	var journal []map[string]any
	for i, op := range f.ops {
		if op == "insert_reading_journal" {
			journal = append(journal, f.vars[i]["object"].(map[string]any))
		}
	}
	if len(journal) != 1 {
		t.Fatalf("journal calls %d", len(journal))
	}
	j := journal[0]
	if j["event"] != "quote" || j["entry"] != "New line." || j["privacy_setting_id"] != float64(hardcover.PrivacyPrivate) ||
		j["action_at"] != "2026-09-29" || j["book_id"] != float64(500) {
		t.Errorf("entry %v", j)
	}
	// No change → no work.
	f.ops, f.vars = nil, nil
	if n, _ := cs.Run(context.Background(), false); n != 0 || len(f.ops) != 0 {
		t.Fatalf("repeat: %d %v", n, f.ops)
	}
	// Import all: 3 baseline clips = 1 quote + 1 note that carries its
	// highlight (same location 648, same time) → 2 journal entries.
	n, err = cs.Run(context.Background(), true)
	if err != nil || n != 2 {
		t.Fatalf("import: %d %v", n, err)
	}
	var note map[string]any
	quotes := 0
	for i, op := range f.ops {
		if op != "insert_reading_journal" {
			continue
		}
		o := f.vars[i]["object"].(map[string]any)
		switch o["event"] {
		case "note":
			note = o
		case "quote":
			quotes++
		}
	}
	want := "Highlight:\n“He does not speak to us. He speaks to another Gold,”\n\nNote:\nThis is a test of a note"
	if note == nil || note["entry"] != want || quotes != 1 {
		t.Errorf("note %v, quotes %d", note, quotes)
	}
	if n, _ := cs.Run(context.Background(), true); n != 0 {
		t.Fatalf("second import sent %d", n)
	}
}

func TestLocationToPage(t *testing.T) {
	// Device: location 661 in a book of ~933800 chars ≈ 10.6 %.
	if p := locationToPage(661, 933800, 401); p != 42 {
		t.Errorf("page %d", p)
	}
	if p := locationToPage(661, 0, 401); p != 0 {
		t.Errorf("no text length: %d", p)
	}
}

// Clips sent with the old (time-zone-based) ID are not sent again.
func TestMigrateIDs(t *testing.T) {
	cl, err := clippings.Parse(strings.NewReader("Red Rising (Pierce Brown)\n- Your Highlight on Location 6145-6146 | Added on Monday, September 28, 2026 1:03:31 AM\n\nText\n==========\n"))
	if err != nil || len(cl) != 1 {
		t.Fatal(cl, err)
	}
	st := &ClipState{Sent: map[string]int{cl[0].LegacyID(): 42}}
	if !migrateIDs(st, cl) || st.Sent[cl[0].ID()] != 42 || len(st.Sent) != 1 {
		t.Fatalf("sent %v", st.Sent)
	}
	if migrateIDs(st, cl) {
		t.Fatal("second run changed state")
	}
}

// Entries of clippings no longer in the file are removed; others stay.
func TestPruneSent(t *testing.T) {
	cl, _ := clippings.Parse(strings.NewReader("Red Rising (Pierce Brown)\n- Your Highlight on Location 1-2 | Added on Monday, September 28, 2026 1:03:31 AM\n\nText\n==========\n"))
	st := &ClipState{Sent: map[string]int{cl[0].ID(): 1, "gone": 2, cl[0].LegacyID(): 3}}
	if n := pruneSent(st, cl); n != 1 || len(st.Sent) != 2 {
		t.Fatalf("pruned %d, left %v", n, st.Sent)
	}
	if n := pruneSent(st, nil); n != 0 || len(st.Sent) != 2 {
		t.Fatalf("empty parse pruned %d", n)
	}
}
