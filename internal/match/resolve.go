// Package match finds the Hardcover book for a local Kindle book.
//
// Waterfall, most exact first; the first confident hit wins:
//  1. ASIN        → editions.asin
//  2. ISBN-13/10  → editions.isbn_13 / isbn_10
//  3. the user's own library, by title + author
//  4. catalog search, by title + author (one exact hit, or one hit with the
//     same year)
//  5. else: no match. Never guess.
//
// An ID hit is accepted only if it points to exactly one book. IDs found in
// non-dedicated fields (description, source, file name) also need a title
// match.
package match

import (
	"context"
	"errors"
	"fmt"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/hardcover"
)

// Catalog is the part of the Hardcover client the resolver needs.
type Catalog interface {
	EditionsBy(ctx context.Context, field, value string) ([]hardcover.EditionHit, error)
	SearchBooks(ctx context.Context, query string, limit int) ([]hardcover.BookHit, error)
}

// Result is a resolved book.
type Result struct {
	BookID    int
	EditionID *int // set for ID hits
	Pages     int  // pages of EditionID, if known
	Title     string
	Method    string // asin, isbn13, isbn10, library, search, search+year
	Via       string // the identifier or title used
}

// Library loads the user's shelves. It is called only when the waterfall
// reaches the library step (it downloads the whole library).
type Library func(ctx context.Context) ([]hardcover.UserBook, error)

// Resolve runs the waterfall. steps gets one line per attempt (for the log).
// It returns (nil, steps, nil) when nothing matches confidently, and an error
// when a step could not run (sign-in, no network): then nothing is known.
func Resolve(ctx context.Context, cat Catalog, id book.Identity, loadLibrary Library) (*Result, []string, error) {
	var steps []string
	logf := func(f string, a ...any) { steps = append(steps, fmt.Sprintf(f, a...)) }

	// 1 + 2: identifiers, in the order BuildIdentity made them.
	for _, pass := range []string{book.KindASIN, book.KindISBN13} {
		for _, x := range id.IDs {
			if x.Kind != pass {
				continue
			}
			for _, q := range lookups(x) {
				hits, err := cat.EditionsBy(ctx, q.field, q.value)
				if stop(err) {
					return nil, steps, err
				}
				if err != nil {
					logf("%s %s@%s: error: %v", q.field, q.value, x.Source, err)
					continue
				}
				books := map[int]hardcover.EditionHit{}
				for _, h := range hits {
					if _, ok := books[h.BookID]; !ok {
						books[h.BookID] = h
					}
				}
				switch {
				case len(books) == 0:
					logf("%s %s@%s: no edition", q.field, q.value, x.Source)
					continue
				case len(books) > 1:
					logf("%s %s@%s: %d different books, skipped", q.field, q.value, x.Source, len(books))
					continue
				}
				var h hardcover.EditionHit
				for _, v := range books {
					h = v
				}
				close := book.TitleClose(id.Titles, h.Book.Title)
				if !close && !x.Dedicated {
					logf("%s %s@%s: book %d %q, title does not match, skipped", q.field, q.value, x.Source, h.BookID, h.Book.Title)
					continue
				}
				if !close {
					logf("%s %s@%s: book %d %q, title differs (accepted: dedicated field)", q.field, q.value, x.Source, h.BookID, h.Book.Title)
				} else {
					logf("%s %s@%s: book %d %q", q.field, q.value, x.Source, h.BookID, h.Book.Title)
				}
				eid := h.ID
				method := x.Kind
				if q.field == "isbn_10" {
					method = "isbn10"
				}
				return &Result{BookID: h.BookID, EditionID: &eid, Pages: h.Pages, Title: h.Book.Title, Method: method, Via: q.value}, steps, nil
			}
		}
	}

	if len(id.Titles) == 0 {
		logf("no title, cannot search")
		return nil, steps, nil
	}

	// 3: the user's own library.
	var library []hardcover.UserBook
	if loadLibrary != nil {
		lib, err := loadLibrary(ctx)
		if stop(err) {
			return nil, steps, err
		}
		if err != nil {
			logf("library: error: %v", err)
		}
		library = lib
	}
	cands := make([]book.Candidate, len(library))
	for i := range library {
		cands[i] = book.Candidate{Title: library[i].Book.Title, Authors: library[i].Authors()}
	}
	switch m := book.MatchAll(id.Titles, id.Authors, cands); len(m) {
	case 1:
		ub := library[m[0]]
		logf("library: book %d %q (status %d)", ub.BookID, ub.Book.Title, ub.StatusID)
		return &Result{BookID: ub.BookID, Title: ub.Book.Title, Method: "library", Via: id.Titles[0]}, steps, nil
	case 0:
		logf("library: no title+author match in %d books", len(library))
	default:
		logf("library: %d matches, skipped", len(m))
	}

	// 4: catalog search.
	for _, t := range id.Titles {
		q := t
		if len(id.Authors) > 0 {
			q += " " + id.Authors[0]
		}
		hits, err := cat.SearchBooks(ctx, q, 10)
		if stop(err) {
			return nil, steps, err
		}
		if err != nil {
			logf("search %q: error: %v", q, err)
			continue
		}
		sc := make([]book.Candidate, len(hits))
		for i := range hits {
			sc[i] = book.Candidate{Title: hits[i].Title, Authors: hits[i].Authors()}
		}
		m := book.MatchAll(id.Titles, id.Authors, sc)
		switch {
		case len(m) == 1:
			h := hits[m[0]]
			logf("search %q: book %d %q", q, h.ID, h.Title)
			return &Result{BookID: h.ID, Title: h.Title, Method: "search", Via: q}, steps, nil
		case len(m) > 1 && id.Year > 0:
			var same []int
			for _, i := range m {
				if y := hits[i].ReleaseYear; y != nil && *y == id.Year {
					same = append(same, i)
				}
			}
			if len(same) == 1 {
				h := hits[same[0]]
				logf("search %q: %d matches, 1 with year %d: book %d %q", q, len(m), id.Year, h.ID, h.Title)
				return &Result{BookID: h.ID, Title: h.Title, Method: "search+year", Via: q}, steps, nil
			}
			logf("search %q: %d matches, %d with year %d, skipped", q, len(m), len(same), id.Year)
		case len(m) > 1:
			logf("search %q: %d matches and no year to choose, skipped", q, len(m))
		default:
			logf("search %q: %d results, no exact title+author match", q, len(hits))
		}
	}
	return nil, steps, nil
}

// stop reports errors that end the waterfall: sign-in problems and temporary
// failures (no network, 5xx, 429). A temporary failure must not fall through
// to a weaker step or end as "not found": the caller tries again later.
func stop(err error) bool {
	return errors.Is(err, hardcover.ErrUnauthorized) || hardcover.IsTransient(err)
}

type lookup struct{ field, value string }

func lookups(x book.ID) []lookup {
	switch x.Kind {
	case book.KindASIN:
		return []lookup{{"asin", x.Value}}
	case book.KindISBN13:
		l := []lookup{{"isbn_13", x.Value}}
		if i10 := book.ISBN13To10(x.Value); i10 != "" {
			l = append(l, lookup{"isbn_10", i10})
		}
		return l
	}
	return nil
}
