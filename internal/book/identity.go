package book

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/mobi"
)

// ID kinds, in match order.
const (
	KindASIN   = "asin"
	KindISBN13 = "isbn13"
)

// ID is one identifier and where it came from.
type ID struct {
	Kind   string
	Value  string
	Source string // e.g. "exth113", "cc.cdeKey", "exth112"
	// Dedicated is true when the field is meant for this ID (EXTH 104 for
	// ISBN, 113/504 or the Kindle key for ASIN). IDs found in other fields
	// (description, source, rights, file name) need a title check.
	Dedicated bool
}

// Identity is everything we know to find the book on Hardcover.
type Identity struct {
	IDs       []ID
	Titles    []string // cc.db title, EXTH 503, MOBI full name (unique)
	Authors   []string // cc.db credits, EXTH 100 (unique)
	Publisher string
	Year      int
	Language  string
	Notes     []string // parse problems, for the log
}

// freeTextTypes are EXTH records that may mention an ID in free text.
var freeTextTypes = []uint32{mobi.ExthDescription, mobi.ExthSubject, mobi.ExthRights, mobi.ExthSource}

var yearRe = regexp.MustCompile(`\b(1[5-9]\d\d|20\d\d)\b`)

// BuildIdentity merges cc.db data with the book file's metadata.
// meta may be nil (KFX, unreadable file).
func BuildIdentity(l Local, meta *mobi.Meta) Identity {
	var id Identity
	add := func(kind, v, src string, dedicated bool) {
		for _, x := range id.IDs {
			if x.Kind == kind && x.Value == v {
				return // keep the first (more granular) source
			}
		}
		id.IDs = append(id.IDs, ID{Kind: kind, Value: v, Source: src, Dedicated: dedicated})
	}

	// 1. ASINs, dedicated fields first.
	if meta != nil {
		for _, t := range []uint32{mobi.ExthASIN, mobi.ExthASIN2} {
			for _, v := range meta.EXTH[t] {
				for _, a := range FindASINs(v) {
					add(KindASIN, a, fmt.Sprintf("exth%d", t), true)
				}
			}
		}
	}
	for _, a := range FindASINs(l.Key) {
		add(KindASIN, a, "cc.cdeKey", true)
	}

	// 2. ISBNs, dedicated field first, then any other text field.
	if meta != nil {
		for _, v := range meta.EXTH[mobi.ExthISBN] {
			for _, i := range FindISBNs(v) {
				add(KindISBN13, i, "exth104", true)
			}
		}
		// Old store books use an ISBN-10 as ASIN.
		for _, t := range []uint32{mobi.ExthASIN, mobi.ExthASIN2} {
			for _, v := range meta.EXTH[t] {
				for _, i := range FindISBNs(v) {
					add(KindISBN13, i, fmt.Sprintf("exth%d", t), true)
				}
			}
		}
		// Free-text fields only. Structured fields (dates, language, tool
		// names, build numbers) gave false ISBNs on the user's Kindle, e.g.
		// EXTH 106 "2023-05-23..." → 9782023052303.
		for _, t := range freeTextTypes {
			for _, v := range meta.EXTH[t] {
				for _, i := range FindISBNsStrict(v) {
					add(KindISBN13, i, fmt.Sprintf("exth%d", t), false)
				}
				for _, a := range FindASINs(v) {
					add(KindASIN, a, fmt.Sprintf("exth%d", t), false)
				}
			}
		}
	}
	// The key is an ISBN only when the whole key is one: a Calibre UUID
	// holds ISBN-like digit runs by chance (0.37 % of keys, review
	// 2026-09-28), and a dedicated-field hit is accepted even when the
	// title differs.
	if k := strings.ReplaceAll(strings.TrimSpace(l.Key), "-", ""); ValidISBN13(k) {
		add(KindISBN13, k, "cc.cdeKey", true)
	} else if ValidISBN10(k) {
		add(KindISBN13, ISBN10To13(k), "cc.cdeKey", true)
	}
	base := filepath.Base(l.Path)
	for _, i := range FindISBNsStrict(base) {
		add(KindISBN13, i, "filename", false)
	}
	for _, a := range FindASINs(base) {
		add(KindASIN, a, "filename", false)
	}

	// 3. Title / author / tie-breakers.
	id.Titles = uniq(l.Title)
	id.Authors = uniq(l.Authors...)
	if meta != nil {
		id.Titles = uniq(append(id.Titles, meta.First(mobi.ExthTitle), meta.FullName)...)
		id.Authors = uniq(append(id.Authors, meta.EXTH[mobi.ExthAuthor]...)...)
		id.Language = meta.First(mobi.ExthLanguage)
	}
	id.Publisher = l.Publisher
	if id.Publisher == "" && meta != nil {
		id.Publisher = meta.First(mobi.ExthPublisher)
	}
	if y := yearRe.FindString(l.PubDate); y != "" {
		id.Year, _ = strconv.Atoi(y)
	} else if meta != nil {
		if y := yearRe.FindString(meta.First(mobi.ExthPubDate)); y != "" {
			id.Year, _ = strconv.Atoi(y)
		}
	}
	if id.Language == "" {
		id.Language = l.Language
	}
	return id
}

// uniq drops empty and duplicate (by NormTitle/NormName-free compare) strings.
func uniq(in ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		k := strings.ToLower(s)
		if s == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	return out
}
