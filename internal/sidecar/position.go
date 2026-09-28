package sidecar

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Position is the reading position from a sidecar.
type Position struct {
	LPR     string    // last position read, raw ("68089" for MOBI/AZW3)
	FPR     string    // furthest position read, raw
	LPRPos  int64     // LPR as a number, or -1 (KFX positions are not plain numbers)
	FPRPos  int64     // FPR as a number, or -1
	Saved   time.Time // time stored with lpr (zero if none)
	File    string    // sidecar file name (holds the title; do not log it)
	ModTime time.Time
}

// FromDoc gets lpr / fpr from a parsed KRDS document.
func FromDoc(d *Doc) (Position, bool) {
	p := Position{LPRPos: -1, FPRPos: -1}
	lpr := d.Find("lpr")
	if lpr == nil {
		return p, false
	}
	p.LPR, _ = lpr.FirstString()
	for _, v := range lpr.Values {
		// The save time is a long in milliseconds (1790568817322 on device).
		if ms, ok := v.(int64); ok && ms > 1e12 && ms < 1e14 {
			p.Saved = time.UnixMilli(ms)
			break
		}
	}
	if fpr := d.Find("fpr"); fpr != nil {
		p.FPR, _ = fpr.FirstString()
	}
	p.LPRPos = number(p.LPR)
	p.FPRPos = number(p.FPR)
	return p, p.LPR != ""
}

func number(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// SdrDir returns the sidecar folder of a book file: "<name without ext>.sdr".
func SdrDir(bookPath string) string {
	return strings.TrimSuffix(bookPath, filepath.Ext(bookPath)) + ".sdr"
}

// ForBook reads the newest KRDS file in the book's .sdr folder that holds an
// "lpr" object. It does not assume file names or extensions.
func ForBook(bookPath string) (Position, error) {
	dir := SdrDir(bookPath)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return Position{LPRPos: -1, FPRPos: -1}, err
	}
	var best Position
	found := false
	for _, e := range ents {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		fi, err := e.Info()
		if err != nil || fi.Size() > 4<<20 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		d, err := Parse(b)
		if d == nil || (err != nil && d.Find("lpr") == nil) {
			continue
		}
		pos, ok := FromDoc(d)
		if !ok {
			continue
		}
		pos.File, pos.ModTime = e.Name(), fi.ModTime()
		if !found || pos.ModTime.After(best.ModTime) {
			best, found = pos, true
		}
	}
	if !found {
		return Position{LPRPos: -1, FPRPos: -1}, os.ErrNotExist
	}
	return best, nil
}
