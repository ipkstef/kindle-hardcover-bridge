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
func FindISBNs(s string) []string {
	var out []string
	for _, m := range isbnRe.FindAllString(s, -1) {
		d := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(m))
		switch {
		case ValidISBN13(d):
			out = appendUniq(out, d)
		case ValidISBN10(d):
			out = appendUniq(out, ISBN10To13(d))
		}
	}
	return out
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
