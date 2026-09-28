// Package clippings parses the Kindle's "My Clippings.txt" (highlights,
// notes, bookmarks). The file is plain text and the same on every Kindle:
//
//	Title (Author)
//	- Your Highlight on page 44 | Location 661-662 | Added on Monday, September 28, 2026 1:03:31 AM
//
//	text
//	==========
//
// The header wording depends on the Kindle language. Only English is
// supported now (UNVERIFIED for other languages and very old firmware).
package clippings

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultPath is where the Kindle keeps the file.
const DefaultPath = "/mnt/us/documents/My Clippings.txt"

// Kind of clipping.
type Kind string

const (
	Highlight Kind = "highlight"
	Note      Kind = "note"
	Bookmark  Kind = "bookmark"
	Unknown   Kind = "unknown"
)

// Clip is one entry.
type Clip struct {
	Title    string // book title as the Kindle shows it
	Author   string // text in the last "(...)" of the title line, may be ""
	Kind     Kind
	Page     int // Kindle page (0 if none); from a page map, not used for Hardcover
	LocStart int // Kindle location (0 if none)
	LocEnd   int
	Added    time.Time // zero if the date could not be read
	Text     string

	key string // ID fixed at parse time, so later text changes keep it
}

// ID is a stable hash of the entry as read from the file, to send it only once.
func (c Clip) ID() string {
	if c.key != "" {
		return c.key
	}
	return c.hash()
}

// hash uses the "Added on" time as written in the file (wall clock, no time
// zone), so the ID does not change when the Kindle's time zone changes.
func (c Clip) hash() string {
	return c.hashWith(c.Added.Format("2006-01-02T15:04:05"))
}

// LegacyID is the ID used before 2026-09-28 (time converted to UTC, so it
// depended on the time zone). Used once to carry over the sent list.
func (c Clip) LegacyID() string {
	return c.hashWith(c.Added.UTC().Format(time.RFC3339))
}

func (c Clip) hashWith(added string) string {
	h := sha1.Sum([]byte(strings.Join([]string{c.Title, c.Author, string(c.Kind),
		strconv.Itoa(c.LocStart), strconv.Itoa(c.LocEnd), added, c.Text}, "\x1f")))
	return hex.EncodeToString(h[:10])
}

const separator = "=========="

var (
	pageRe  = regexp.MustCompile(`(?i)\bpage\s+([0-9]+)`)
	locRe   = regexp.MustCompile(`(?i)\b(?:location|loc\.)\s+([0-9]+)(?:-([0-9]+))?`)
	addedRe = regexp.MustCompile(`(?i)\badded on\s+(.+)$`)
	dateFmt = []string{
		"Monday, January 2, 2006 3:04:05 PM", // US
		"Monday, 2 January 2006 15:04:05",    // UK
		"Monday, January 2, 2006, 3:04 PM",   // old firmware (UNVERIFIED)
		"Monday, 2 January 06 15:04:05",
	}
)

// Parse reads all entries. Broken entries are skipped.
func Parse(r io.Reader) ([]Clip, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20) // one very long note must not stop all clips
	var out []Clip
	var block []string
	flush := func() {
		if c, ok := parseBlock(block); ok {
			out = append(out, c)
		}
		block = block[:0]
	}
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == separator {
			flush()
			continue
		}
		block = append(block, line)
	}
	flush()
	return out, sc.Err()
}

func parseBlock(lines []string) (Clip, bool) {
	// Drop leading empty lines.
	for len(lines) > 0 && strings.TrimSpace(strings.TrimLeft(lines[0], "\ufeff")) == "" {
		lines = lines[1:]
	}
	if len(lines) < 2 {
		return Clip{}, false
	}
	var c Clip
	c.Title, c.Author = splitTitle(strings.TrimSpace(strings.TrimLeft(lines[0], "\ufeff")))
	meta := strings.TrimSpace(lines[1])
	if !strings.HasPrefix(meta, "-") {
		return Clip{}, false
	}
	low := strings.ToLower(meta)
	switch {
	case strings.Contains(low, "highlight"):
		c.Kind = Highlight
	case strings.Contains(low, "note"):
		c.Kind = Note
	case strings.Contains(low, "bookmark"):
		c.Kind = Bookmark
	default:
		c.Kind = Unknown
	}
	if m := pageRe.FindStringSubmatch(meta); m != nil {
		c.Page, _ = strconv.Atoi(m[1])
	}
	if m := locRe.FindStringSubmatch(meta); m != nil {
		c.LocStart, _ = strconv.Atoi(m[1])
		c.LocEnd = c.LocStart
		if m[2] != "" {
			c.LocEnd = expandEnd(m[1], m[2])
		}
	}
	if m := addedRe.FindStringSubmatch(meta); m != nil {
		for _, f := range dateFmt {
			if t, err := time.ParseInLocation(f, strings.TrimSpace(m[1]), time.Local); err == nil {
				c.Added = t
				break
			}
		}
	}
	c.Text = strings.TrimSpace(strings.Join(lines[2:], "\n"))
	if c.Title == "" {
		return Clip{}, false
	}
	c.key = c.hash()
	return c, true
}

// splitTitle takes the author from the last "(...)" at the end of the line.
func splitTitle(s string) (title, author string) {
	if !strings.HasSuffix(s, ")") {
		return s, ""
	}
	depth := 0
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1 : len(s)-1])
			}
		}
	}
	return s, ""
}

// expandEnd handles short range ends from old firmware: "1234-56" → 1256.
func expandEnd(start, end string) int {
	if len(end) < len(start) {
		end = start[:len(start)-len(end)] + end
	}
	n, _ := strconv.Atoi(end)
	return n
}
