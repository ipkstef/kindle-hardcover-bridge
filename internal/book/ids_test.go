package book

import (
	"testing"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/mobi"
)

func TestISBN(t *testing.T) {
	if !ValidISBN13("9780306406157") || ValidISBN13("9780306406158") {
		t.Error("isbn13 checksum")
	}
	if !ValidISBN10("0306406152") || !ValidISBN10("080442957X") || ValidISBN10("0306406153") {
		t.Error("isbn10 checksum")
	}
	if got := ISBN10To13("0306406152"); got != "9780306406157" {
		t.Errorf("10to13 = %s", got)
	}
	if got := ISBN13To10("9780306406157"); got != "0306406152" {
		t.Errorf("13to10 = %s", got)
	}
	got := FindISBNs("urn:isbn:978-0-306-40615-7; print 0-8044-2957-X; phone 555-123-4567-890")
	if len(got) != 2 || got[0] != "9780306406157" || got[1] != ISBN10To13("080442957X") {
		t.Errorf("FindISBNs = %v", got)
	}
}

func TestASIN(t *testing.T) {
	if got := FindASINs("amazon B0CW1Q9K2F and f1638687-3e46"); len(got) != 1 || got[0] != "B0CW1Q9K2F" {
		t.Errorf("FindASINs = %v", got)
	}
}

func TestBuildIdentityOrder(t *testing.T) {
	meta := &mobi.Meta{FullName: "Parade of Horribles", EXTH: map[uint32][]string{
		mobi.ExthASIN:        {"f1638687-3e46-45b5-b4b2-8f37103c1743"}, // tool UUID, not an ASIN
		mobi.ExthISBN:        {"978-0-306-40615-7"},
		mobi.ExthDescription: {"Also by the author: ISBN 0-8044-2957-X"},
		mobi.ExthSource:      {"calibre:f1638687, amzn B0CW1Q9K2F"},
		mobi.ExthTitle:       {"A Parade of Horribles"},
		mobi.ExthAuthor:      {"Matt Dinniman"},
		mobi.ExthPubDate:     {"2024-02-06"},
	}}
	l := Local{Key: "f1638687-3e46-45b5-b4b2-8f37103c1743", Title: "A Parade of Horribles",
		Authors: []string{"Matt Dinniman"}, Path: "/mnt/us/documents/x.azw3"}
	id := BuildIdentity(l, meta)

	want := []ID{
		{KindISBN13, "9780306406157", "exth104", true},
		{KindISBN13, ISBN10To13("080442957X"), "exth103", false},
		{KindASIN, "B0CW1Q9K2F", "exth112", false},
	}
	if len(id.IDs) != len(want) {
		t.Fatalf("ids %+v", id.IDs)
	}
	for i := range want {
		if id.IDs[i] != want[i] {
			t.Errorf("id[%d] = %+v, want %+v", i, id.IDs[i], want[i])
		}
	}
	if len(id.Titles) != 2 || id.Year != 2024 || id.Authors[0] != "Matt Dinniman" {
		t.Errorf("identity %+v", id)
	}
}
