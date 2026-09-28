package clippings

import (
	"os"
	"strings"
	"testing"
	"time"
)

// testdata/My Clippings.txt is the user's test file from the device (FW 5.17.1).
func TestParseDeviceFile(t *testing.T) {
	f, err := os.Open("testdata/My Clippings.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	clips, err := Parse(f)
	if err != nil || len(clips) != 3 {
		t.Fatalf("got %d clips, %v", len(clips), err)
	}
	h, n := clips[0], clips[1]
	if h.Title != "Red Rising (The Red Rising Trilogy, Book 1)" || h.Author != "Pierce Brown" {
		t.Errorf("title/author %q / %q", h.Title, h.Author)
	}
	if h.Kind != Highlight || h.Page != 44 || h.LocStart != 661 || h.LocEnd != 662 ||
		!strings.HasPrefix(h.Text, "They make a show") {
		t.Errorf("highlight %+v", h)
	}
	want := time.Date(2026, 9, 28, 1, 3, 31, 0, time.Local)
	if !h.Added.Equal(want) {
		t.Errorf("added %v", h.Added)
	}
	if n.Kind != Note || n.LocStart != 648 || n.Text != "This is a test of a note" {
		t.Errorf("note %+v", n)
	}
	if clips[0].ID() == clips[2].ID() || clips[0].ID() != h.ID() {
		t.Error("ids")
	}
}

func TestOldFormats(t *testing.T) {
	in := "Book (Title) (Doe, Jane)\r\n- Highlight Loc. 1234-56  | Added on Monday, 2 January 2012 10:00:00\r\n\r\nline one\r\nline two\r\n==========\r\n" +
		"No Author\n- Your Bookmark at location 10 | Added on Monday, January 2, 2012 1:00:00 PM\n\n\n==========\n"
	clips, _ := Parse(strings.NewReader(in))
	if len(clips) != 2 {
		t.Fatalf("got %d", len(clips))
	}
	c := clips[0]
	if c.Title != "Book (Title)" || c.Author != "Doe, Jane" || c.LocStart != 1234 || c.LocEnd != 1256 ||
		c.Text != "line one\nline two" || c.Added.IsZero() {
		t.Errorf("got %+v", c)
	}
	if clips[1].Kind != Bookmark || clips[1].Author != "" {
		t.Errorf("got %+v", clips[1])
	}
}
