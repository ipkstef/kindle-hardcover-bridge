// Package mobi reads metadata from MOBI / AZW / AZW3 files: the MOBI header
// full name and all EXTH records. KFX files use another format (not supported).
//
// Offsets: https://wiki.mobileread.com/wiki/MOBI (offsets count from the start
// of record 0; the MOBI header starts at record 0 + 16).
package mobi

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// EXTH record types with a known meaning.
const (
	ExthAuthor      = 100
	ExthPublisher   = 101
	ExthDescription = 103
	ExthISBN        = 104
	ExthSubject     = 105
	ExthPubDate     = 106
	ExthContributor = 108
	ExthRights      = 109
	ExthSource      = 112
	ExthASIN        = 113
	ExthCDEType     = 501
	ExthTitle       = 503
	ExthASIN2       = 504 // original ASIN (KF8)
	ExthLanguage    = 524
)

// Meta is all text metadata found in the file.
type Meta struct {
	// FullName is the title from the MOBI header.
	FullName string
	// EXTH holds every EXTH record that is valid UTF-8 text, by type.
	EXTH map[uint32][]string
	// Records is the number of EXTH records (text or not). 0 = no EXTH.
	Records int
}

// First returns the first value of an EXTH type, or "".
func (m *Meta) First(typ uint32) string {
	if v := m.EXTH[typ]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// Types returns the EXTH types present, sorted.
func (m *Meta) Types() []uint32 {
	out := make([]uint32, 0, len(m.EXTH))
	for t := range m.EXTH {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Title returns EXTH 503, else the MOBI full name.
func (m *Meta) Title() string {
	if t := m.First(ExthTitle); t != "" {
		return t
	}
	return m.FullName
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
	// PalmDB header: 78 bytes, "BOOKMOBI" at 60, record count at 76,
	// then the record list (8 bytes each).
	hdr := make([]byte, 86)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return nil, ErrNotMobi
	}
	if string(hdr[60:68]) != "BOOKMOBI" || binary.BigEndian.Uint16(hdr[76:78]) < 1 {
		return nil, ErrNotMobi
	}
	rec0 := int64(binary.BigEndian.Uint32(hdr[78:82]))

	// Record 0 up to the full-name fields (offset 0x54, 0x58).
	mh := make([]byte, 0x5C)
	if _, err := r.ReadAt(mh, rec0); err != nil {
		return nil, ErrNotMobi
	}
	if string(mh[16:20]) != "MOBI" {
		return nil, ErrNotMobi
	}
	mobiLen := int64(binary.BigEndian.Uint32(mh[20:24]))
	m := &Meta{EXTH: map[uint32][]string{}}

	if off, n := binary.BigEndian.Uint32(mh[0x54:0x58]), binary.BigEndian.Uint32(mh[0x58:0x5C]); n > 0 && n < 4096 {
		name := make([]byte, n)
		if _, err := r.ReadAt(name, rec0+int64(off)); err == nil && utf8.Valid(name) {
			m.FullName = strings.TrimSpace(string(name))
		}
	}

	// EXTH follows the MOBI header. Look for the marker; do not trust the
	// EXTH flag bit.
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
		m.Records++
		if isText(data) {
			if v := strings.TrimSpace(string(data)); v != "" {
				m.EXTH[typ] = append(m.EXTH[typ], v)
			}
		}
		off += n
	}
	return m, nil
}

// isText reports if b is UTF-8 text with no control chars (other than space).
func isText(b []byte) bool {
	if len(b) == 0 || !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}
