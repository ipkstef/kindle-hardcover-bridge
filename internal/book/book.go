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
	CDEType    string   // p_cdeType: EBOK, PDOC
	MimeType   string   // p_mimeType
	Publisher  string   // p_publisher
	PubDate    string   // p_publicationDate (raw)
	Language   string   // p_languages_0
	// LastPosition is p_lastAccessedPosition ("LPR", e.g. "#1234"). Empty on
	// FW 5.17.1; may be set on other firmware (UNVERIFIED).
	LastPosition string
	// ReadState is p_readState. Seen on device: NULL (not finished),
	// 1 (at 97–99 %), 2 (at 100 %, "read"), 3 (rare). Meaning UNVERIFIED.
	ReadState int
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
	return stripArticle(normWords(s))
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
	m := MatchAll([]string{local.Title}, local.Authors, cands)
	if len(m) != 1 {
		return -1
	}
	return m[0]
}

// MatchAll returns the indexes of all candidates whose title equals one of
// titles (TitleEqual) and (if both sides have authors) share an author.
func MatchAll(titles, authors []string, cands []Candidate) []int {
	var out []int
	for i, c := range cands {
		if !authorsOverlap(authors, c.Authors) {
			continue
		}
		for _, t := range titles {
			if TitleEqual(t, c.Title) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// TitleEqual reports if two titles name the same book. Series suffixes in
// "( )" or "[ ]" are ignored. A subtitle (after ":") must be the same on both
// sides, unless it only describes the book ("A Novel", "Book 1 of ...").
// So "Mistborn: Secret History" is not "Mistborn: The Final Empire", and
// "Red Rising: Sons of Ares" is not "Red Rising", but "Project Hail Mary: A
// Novel" is "Project Hail Mary". "Mistborn: The Final Empire" also equals
// "The Final Empire" (series name in front).
func TitleEqual(a, b string) bool {
	am, as := titleParts(a)
	bm, bs := titleParts(b)
	if am == "" || bm == "" {
		return false
	}
	switch {
	case am == bm && as == bs:
		return true
	case am == bm:
		return genericSubtitle(as) && genericSubtitle(bs)
	case as != "" && as == bm && genericSubtitle(bs):
		return true
	case bs != "" && bs == am && genericSubtitle(as):
		return true
	}
	return false
}

// titleParts returns the normalized main title and subtitle, without a
// series suffix in "( )" or "[ ]" and without a leading article.
func titleParts(s string) (main, sub string) {
	if i := strings.IndexAny(s, "(["); i > 0 {
		s = s[:i]
	}
	if i := strings.Index(s, ":"); i > 0 {
		s, sub = s[:i], s[i+1:]
	}
	return stripArticle(normWords(s)), stripArticle(normWords(sub))
}

func stripArticle(s string) string {
	for _, a := range []string{"the ", "a ", "an "} {
		s = strings.TrimPrefix(s, a)
	}
	return s
}

// genericWords make a subtitle a description, not a different book.
var genericWords = map[string]bool{
	"novel": true, "novella": true, "book": true, "books": true, "series": true,
	"trilogy": true, "duology": true, "saga": true, "volume": true, "vol": true,
	"edition": true, "memoir": true, "thriller": true, "mystery": true,
	"romance": true, "stories": true, "collection": true,
}

func genericSubtitle(sub string) bool {
	if sub == "" {
		return true
	}
	for _, w := range strings.Fields(sub) {
		if genericWords[w] {
			return true
		}
	}
	return false
}

// TitleClose reports if one normalized title contains the other.
// Used as a sanity check for identifier hits.
func TitleClose(titles []string, remote string) bool {
	r := NormTitle(remote)
	if r == "" {
		return false
	}
	for _, t := range titles {
		n := NormTitle(t)
		if n != "" && (strings.Contains(n, r) || strings.Contains(r, n)) {
			return true
		}
	}
	return false
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
