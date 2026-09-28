package match

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/hardcover"
)

type fakeCat struct {
	editions map[string][]hardcover.EditionHit // key: field+"="+value
	search   map[string][]hardcover.BookHit
	calls    []string
}

func (f *fakeCat) EditionsBy(_ context.Context, field, value string) ([]hardcover.EditionHit, error) {
	f.calls = append(f.calls, field+"="+value)
	if field == "asin" && value == "B0ERRORERR" {
		return nil, errors.New("field 'asin' not found")
	}
	return f.editions[field+"="+value], nil
}

func (f *fakeCat) SearchBooks(_ context.Context, q string, _ int) ([]hardcover.BookHit, error) {
	f.calls = append(f.calls, "search="+q)
	return f.search[q], nil
}

func contrib(names ...string) json.RawMessage {
	var l []map[string]any
	for _, n := range names {
		l = append(l, map[string]any{"author": map[string]string{"name": n}})
	}
	b, _ := json.Marshal(l)
	return b
}

func ed(id, bookID int, title string) hardcover.EditionHit {
	return hardcover.EditionHit{ID: id, BookID: bookID, Book: hardcover.BookHit{ID: bookID, Title: title}}
}

func yr(y int) *int { return &y }

var parade = book.Identity{
	Titles:  []string{"A Parade of Horribles"},
	Authors: []string{"Matt Dinniman"},
}

func withIDs(ids ...book.ID) book.Identity {
	i := parade
	i.IDs = ids
	return i
}

func TestASINFirst(t *testing.T) {
	cat := &fakeCat{editions: map[string][]hardcover.EditionHit{
		"asin=B0CW1Q9K2F":       {ed(1, 100, "A Parade of Horribles")},
		"isbn_13=9780306406157": {ed(2, 200, "Other")},
	}}
	id := withIDs(
		book.ID{Kind: book.KindISBN13, Value: "9780306406157", Source: "exth104", Dedicated: true},
		book.ID{Kind: book.KindASIN, Value: "B0CW1Q9K2F", Source: "exth113", Dedicated: true},
	)
	r, _, err := Resolve(context.Background(), cat, id, nil)
	if err != nil || r == nil || r.BookID != 100 || r.Method != "asin" || *r.EditionID != 1 {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestFallThroughToISBN10(t *testing.T) {
	cat := &fakeCat{editions: map[string][]hardcover.EditionHit{
		"isbn_10=0306406152": {ed(3, 300, "Parade of Horribles")},
	}}
	id := withIDs(
		book.ID{Kind: book.KindASIN, Value: "B0ERRORERR", Source: "exth113", Dedicated: true},
		book.ID{Kind: book.KindISBN13, Value: "9780306406157", Source: "exth104", Dedicated: true},
	)
	r, steps, err := Resolve(context.Background(), cat, id, nil)
	if err != nil || r == nil || r.BookID != 300 || r.Method != "isbn10" {
		t.Fatalf("got %+v %v %v", r, err, steps)
	}
}

func TestNonDedicatedNeedsTitle(t *testing.T) {
	cat := &fakeCat{editions: map[string][]hardcover.EditionHit{
		"isbn_13=9780306406157": {ed(4, 400, "A Different Book")},
	}}
	id := withIDs(book.ID{Kind: book.KindISBN13, Value: "9780306406157", Source: "exth103", Dedicated: false})
	lib := []hardcover.UserBook{{ID: 9, BookID: 500, StatusID: 2}}
	lib[0].Book.Title = "A Parade of Horribles"
	lib[0].Book.Contributors = contrib("Matt Dinniman")
	r, steps, _ := Resolve(context.Background(), cat, id, func(context.Context) ([]hardcover.UserBook, error) { return lib, nil })
	if r == nil || r.BookID != 500 || r.Method != "library" {
		t.Fatalf("got %+v %v", r, steps)
	}
}

func TestAmbiguousIDSkipped(t *testing.T) {
	cat := &fakeCat{editions: map[string][]hardcover.EditionHit{
		"asin=B0CW1Q9K2F": {ed(1, 100, "A Parade of Horribles"), ed(2, 101, "A Parade of Horribles")},
	}}
	id := withIDs(book.ID{Kind: book.KindASIN, Value: "B0CW1Q9K2F", Source: "exth113", Dedicated: true})
	r, _, _ := Resolve(context.Background(), cat, id, nil)
	if r != nil {
		t.Fatalf("ambiguous ASIN accepted: %+v", r)
	}
}

func TestSearchYearTieBreak(t *testing.T) {
	q := "A Parade of Horribles Matt Dinniman"
	cat := &fakeCat{search: map[string][]hardcover.BookHit{q: {
		{ID: 10, Title: "A Parade of Horribles", ReleaseYear: yr(2023), Contributors: contrib("Matt Dinniman")},
		{ID: 11, Title: "A Parade of Horribles", ReleaseYear: yr(2024), Contributors: contrib("Matt Dinniman")},
		{ID: 12, Title: "Dungeon Crawler Carl", ReleaseYear: yr(2020), Contributors: contrib("Matt Dinniman")},
	}}}
	id := parade
	r, _, _ := Resolve(context.Background(), cat, id, nil)
	if r != nil {
		t.Fatalf("no year: must skip, got %+v", r)
	}
	id.Year = 2024
	r, _, _ = Resolve(context.Background(), cat, id, nil)
	if r == nil || r.BookID != 11 || r.Method != "search+year" {
		t.Fatalf("got %+v", r)
	}
}

func TestNoMatch(t *testing.T) {
	r, steps, err := Resolve(context.Background(), &fakeCat{}, parade, nil)
	if r != nil || err != nil || len(steps) == 0 {
		t.Fatalf("got %+v %v %v", r, err, steps)
	}
}
