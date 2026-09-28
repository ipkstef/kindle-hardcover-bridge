package book

import (
	"regexp"
	"strings"
)

var (
	// Digits with optional hyphens/spaces, 10 or 13 digits long (last may be X).
	isbnRe = regexp.MustCompile(`[0-9][0-9\- ]{8,16}[0-9Xx]`)
	asinRe = regexp.MustCompile(`\bB0[0-9A-Z]{8}\b`)
)

// ValidISBN10 checks the ISBN-10 checksum. s must be 10 chars, digits + X.
func ValidISBN10(s string) bool {
	if len(s) != 10 {
		return false
	}
	sum := 0
	for i := 0; i < 10; i++ {
		c := s[i]
		var v int
		switch {
		case c >= '0' && c <= '9':
			v = int(c - '0')
		case (c == 'X' || c == 'x') && i == 9:
			v = 10
		default:
			return false
		}
		sum += v * (10 - i)
	}
	return sum%11 == 0
}

// ValidISBN13 checks the ISBN-13 checksum and the 978/979 prefix.
func ValidISBN13(s string) bool {
	if len(s) != 13 || !(strings.HasPrefix(s, "978") || strings.HasPrefix(s, "979")) {
		return false
	}
	sum := 0
	for i := 0; i < 13; i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
		v := int(c - '0')
		if i%2 == 1 {
			v *= 3
		}
		sum += v
	}
	return sum%10 == 0
}

// ISBN10To13 converts a valid ISBN-10 to ISBN-13.
func ISBN10To13(s string) string {
	core := "978" + s[:9]
	sum := 0
	for i := 0; i < 12; i++ {
		v := int(core[i] - '0')
		if i%2 == 1 {
			v *= 3
		}
		sum += v
	}
	return core + string(rune('0'+(10-sum%10)%10))
}

// ISBN13To10 converts a valid 978 ISBN-13 to ISBN-10, or "" for 979.
func ISBN13To10(s string) string {
	if !strings.HasPrefix(s, "978") {
		return ""
	}
	core := s[3:12]
	sum := 0
	for i := 0; i < 9; i++ {
		sum += int(core[i]-'0') * (10 - i)
	}
	c := (11 - sum%11) % 11
	if c == 10 {
		return core + "X"
	}
	return core + string(rune('0'+c))
}

// FindISBNs returns valid ISBN-13s found in s (ISBN-10s are converted).
// Use it for fields that are meant to hold an ISBN.
func FindISBNs(s string) []string { return findISBNs(s, false) }

// FindISBNsStrict is for free-text fields (description, rights, file name).
// A random 10-digit number passes the ISBN-10 checksum 1 time in 11, so here
// an ISBN-10 counts only right after the word "ISBN". ISBN-13 needs the
// 978/979 prefix and its checksum, so it is accepted anywhere.
func FindISBNsStrict(s string) []string { return findISBNs(s, true) }

func findISBNs(s string, strict bool) []string {
	var out []string
	lower := strings.ToLower(s)
	for _, loc := range isbnRe.FindAllStringIndex(s, -1) {
		d := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s[loc[0]:loc[1]]))
		switch {
		case ValidISBN13(d):
			out = appendUniq(out, d)
		case ValidISBN10(d) && (!strict || isbnLabelBefore(lower, loc[0])):
			out = appendUniq(out, ISBN10To13(d))
		}
	}
	return out
}

// isbnLabelBefore reports if "isbn" appears in the 12 chars before i.
func isbnLabelBefore(lower string, i int) bool {
	start := i - 12
	if start < 0 {
		start = 0
	}
	return strings.Contains(lower[start:i], "isbn")
}

// FindASINs returns Kindle-style ASINs (B0 + 8) found in s.
func FindASINs(s string) []string {
	var out []string
	for _, m := range asinRe.FindAllString(strings.ToUpper(s), -1) {
		out = appendUniq(out, m)
	}
	return out
}

func appendUniq(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
