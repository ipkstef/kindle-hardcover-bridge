// Package book holds local book identity and matching to Hardcover books.
package book

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Local is a book as the Kindle sees it, with its reading state.
type Local struct {
	Key        string   // p_cdeKey: ASIN for store books, UUID for sideloaded
	Title      string   // p_titles_0_nominal
	Authors    []string // from j_credits
	Path       string   // p_location
	Percent    float64  // p_percentFinished, 0–100
	LastAccess int64    // p_lastAccess, unix seconds
}

// NormTitle makes a title comparable: lower case, no accents, no punctuation,
// no leading article, no subtitle or series suffix.
//
//	"The Shadow Of What Was Lost (The Licanius Trilogy Book 1)" → "shadow of what was lost"
//	"The Practice Effect: A Novel"                              → "practice effect"
func NormTitle(s string) string {
	if i := strings.IndexAny(s, "(["); i > 0 {
		s = s[:i]
	}
	if i := strings.Index(s, ":"); i > 0 {
		s = s[:i]
	}
	s = normWords(s)
	for _, a := range []string{"the ", "a ", "an "} {
		s = strings.TrimPrefix(s, a)
	}
	return s
}

// NormName makes an author name comparable. Initials and dots are dropped so
// "James S. A. Corey" and "James S.A. Corey" both give "james corey".
func NormName(s string) string {
	var keep []string
	for _, w := range strings.Fields(normWords(strings.ReplaceAll(s, ".", ". "))) {
		if len(w) > 1 {
			keep = append(keep, w)
		}
	}
	return strings.Join(keep, " ")
}

func normWords(s string) string {
	s = norm.NFKD.String(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r):
			// drop accents
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		case r == '\'' || r == '’':
			// "Wizard's" → "wizards"
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// Candidate is a remote book to match against.
type Candidate struct {
	Title   string
	Authors []string
}

// Match returns the index of the one candidate whose normalized title equals
// the local title and (if both sides have authors) shares an author.
// It returns -1 when there is no match or more than one: we never guess.
func Match(local Local, cands []Candidate) int {
	want := NormTitle(local.Title)
	if want == "" {
		return -1
	}
	found := -1
	for i, c := range cands {
		if NormTitle(c.Title) != want || !authorsOverlap(local.Authors, c.Authors) {
			continue
		}
		if found >= 0 {
			return -1
		}
		found = i
	}
	return found
}

func authorsOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			if nx, ny := NormName(x), NormName(y); nx != "" && nx == ny {
				return true
			}
		}
	}
	return false
}

// PercentToPage converts 0–100 percent to a page in an edition of pages.
// The result is at least 1 when percent > 0, and at most pages.
func PercentToPage(percent float64, pages int) int {
	if pages <= 0 || percent <= 0 {
		return 0
	}
	p := int(percent / 100 * float64(pages))
	if p < 1 {
		p = 1
	}
	if p > pages {
		p = pages
	}
	return p
}
