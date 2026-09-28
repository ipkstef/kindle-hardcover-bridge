// Package mobi reads metadata (EXTH records) from MOBI / AZW / AZW3 files.
// KFX files use a different format and are not supported.
package mobi

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
)

// EXTH record types we use. See https://wiki.mobileread.com/wiki/MOBI#EXTH_Header
const (
	ExthAuthor    = 100
	ExthISBN      = 104
	ExthSource    = 112
	ExthASIN      = 113
	ExthCDEType   = 501
	ExthTitle     = 503
	ExthASIN2     = 504 // "original" ASIN in some KF8 files
	exthFlagIndex = 0x80
)

// Meta is the metadata we care about.
type Meta struct {
	ISBN    string
	ASIN    string
	Title   string
	Authors []string
	Source  string
}

// ErrNotMobi means the file is not a PalmDB/MOBI file.
var ErrNotMobi = errors.New("mobi: not a MOBI file")

// ReadFile reads metadata from a file. It reads only the first record.
func ReadFile(path string) (*Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Read(f)
}

// Read reads metadata from a MOBI stream.
func Read(r io.ReaderAt) (*Meta, error) {
	// PalmDB header: 78 bytes. Type/creator at 60: "BOOKMOBI".
	hdr := make([]byte, 86)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return nil, ErrNotMobi
	}
	if string(hdr[60:68]) != "BOOKMOBI" {
		return nil, ErrNotMobi
	}
	if binary.BigEndian.Uint16(hdr[76:78]) < 1 {
		return nil, ErrNotMobi
	}
	rec0 := int64(binary.BigEndian.Uint32(hdr[78:82]))

	// Record 0: PalmDOC header (16 bytes), then MOBI header.
	mh := make([]byte, 16+0x84)
	if _, err := r.ReadAt(mh, rec0); err != nil {
		return nil, ErrNotMobi
	}
	if string(mh[16:20]) != "MOBI" {
		return nil, ErrNotMobi
	}
	mobiLen := int64(binary.BigEndian.Uint32(mh[20:24]))
	flags := binary.BigEndian.Uint32(mh[16+exthFlagIndex : 16+exthFlagIndex+4])
	m := &Meta{}
	if flags&0x40 == 0 {
		return m, nil // no EXTH
	}
	exth := rec0 + 16 + mobiLen
	eh := make([]byte, 12)
	if _, err := r.ReadAt(eh, exth); err != nil || string(eh[0:4]) != "EXTH" {
		return m, nil
	}
	count := binary.BigEndian.Uint32(eh[8:12])
	off := exth + 12
	rh := make([]byte, 8)
	for i := uint32(0); i < count && i < 1000; i++ {
		if _, err := r.ReadAt(rh, off); err != nil {
			break
		}
		typ := binary.BigEndian.Uint32(rh[0:4])
		n := int64(binary.BigEndian.Uint32(rh[4:8]))
		if n < 8 || n > 1<<20 {
			break
		}
		data := make([]byte, n-8)
		if _, err := r.ReadAt(data, off+8); err != nil {
			break
		}
		v := strings.TrimSpace(string(data))
		switch typ {
		case ExthISBN:
			m.ISBN = v
		case ExthASIN, ExthASIN2:
			if m.ASIN == "" || typ == ExthASIN {
				m.ASIN = v
			}
		case ExthTitle:
			m.Title = v
		case ExthAuthor:
			m.Authors = append(m.Authors, v)
		case ExthSource:
			m.Source = v
		}
		off += n
	}
	return m, nil
}

// CleanISBN returns digits (and X) only, or "" if it is not a 10/13 ISBN.
func CleanISBN(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= '0' && r <= '9') || r == 'X' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) == 10 || len(out) == 13 {
		return out
	}
	return ""
}

// IsASIN reports if s looks like an Amazon ASIN (B0 + 8 chars).
func IsASIN(s string) bool {
	return len(s) == 10 && strings.HasPrefix(s, "B0")
}
