// Package sidecar reads the stock reader's sidecar files in <book>.sdr/.
//
// Files like .azw3f / .azw3r (and .yjf / .yjr for KFX) use Amazon's "KRDS"
// format: an 8-byte signature, then typed values and named objects.
// Seen on FW 5.17.1 (docs/findings-bellatrix-5.17.1.md, "Sleep research"):
// .azw3f holds "lpr" (last position read) and "fpr" (furthest position
// read); the reader rewrites it on open, sleep and leaving the book.
package sidecar

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Signature starts every KRDS file.
var Signature = []byte{0, 0, 0, 0, 0, 0x1a, 0xb1, 0x26}

// KRDS value types.
const (
	tBool   = 0x00
	tInt    = 0x01
	tLong   = 0x02
	tUTF    = 0x03
	tDouble = 0x04
	tShort  = 0x05
	tFloat  = 0x06
	tByte   = 0x07
	tChar   = 0x09
	tBegin  = 0xfe
	tEnd    = 0xff
)

// Object is a named KRDS object with its values. A value is one of: bool,
// int32, int64, *string (nil = null string), float64, int16, float32, byte,
// uint16 (char) or *Object.
type Object struct {
	Name   string
	Values []any
}

// Doc is a parsed KRDS file.
type Doc struct {
	Values []any // top-level values (header numbers and objects)
}

// ErrNotKRDS means the data has no KRDS signature.
var ErrNotKRDS = errors.New("sidecar: not a KRDS file")

// Parse reads a KRDS document.
func Parse(b []byte) (*Doc, error) {
	if !bytes.HasPrefix(b, Signature) {
		return nil, ErrNotKRDS
	}
	p := &parser{b: b, i: len(Signature)}
	var d Doc
	for p.i < len(p.b) {
		v, err := p.value()
		if err != nil {
			return &d, err
		}
		d.Values = append(d.Values, v)
	}
	return &d, nil
}

// Find returns the first object with this name (depth-first), or nil.
func (d *Doc) Find(name string) *Object {
	return find(d.Values, name)
}

func find(vals []any, name string) *Object {
	for _, v := range vals {
		o, ok := v.(*Object)
		if !ok {
			continue
		}
		if o.Name == name {
			return o
		}
		if f := find(o.Values, name); f != nil {
			return f
		}
	}
	return nil
}

// FirstString returns the first non-null string value of the object.
func (o *Object) FirstString() (string, bool) {
	for _, v := range o.Values {
		if s, ok := v.(*string); ok && s != nil {
			return *s, true
		}
	}
	return "", false
}

type parser struct {
	b     []byte
	i     int
	depth int // nested objects; capped so a bad file cannot use much memory
}

const maxDepth = 64

var errShort = errors.New("sidecar: truncated KRDS data")

func (p *parser) take(n int) ([]byte, error) {
	if n < 0 || p.i+n > len(p.b) {
		return nil, errShort
	}
	s := p.b[p.i : p.i+n]
	p.i += n
	return s, nil
}

// utf reads a string body: 1-byte null flag, then uint16 length + bytes.
func (p *parser) utf() (*string, error) {
	f, err := p.take(1)
	if err != nil {
		return nil, err
	}
	if f[0] != 0 {
		return nil, nil
	}
	l, err := p.take(2)
	if err != nil {
		return nil, err
	}
	s, err := p.take(int(binary.BigEndian.Uint16(l)))
	if err != nil {
		return nil, err
	}
	str := string(s)
	return &str, nil
}

func (p *parser) value() (any, error) {
	t, err := p.take(1)
	if err != nil {
		return nil, err
	}
	switch t[0] {
	case tBool:
		b, err := p.take(1)
		if err != nil {
			return nil, err
		}
		return b[0] != 0, nil
	case tInt:
		b, err := p.take(4)
		if err != nil {
			return nil, err
		}
		return int32(binary.BigEndian.Uint32(b)), nil
	case tLong:
		b, err := p.take(8)
		if err != nil {
			return nil, err
		}
		return int64(binary.BigEndian.Uint64(b)), nil
	case tUTF:
		return p.utf()
	case tDouble:
		b, err := p.take(8)
		if err != nil {
			return nil, err
		}
		return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
	case tShort:
		b, err := p.take(2)
		if err != nil {
			return nil, err
		}
		return int16(binary.BigEndian.Uint16(b)), nil
	case tFloat:
		b, err := p.take(4)
		if err != nil {
			return nil, err
		}
		return math.Float32frombits(binary.BigEndian.Uint32(b)), nil
	case tByte:
		b, err := p.take(1)
		if err != nil {
			return nil, err
		}
		return b[0], nil
	case tChar:
		b, err := p.take(2)
		if err != nil {
			return nil, err
		}
		return binary.BigEndian.Uint16(b), nil
	case tBegin:
		if p.depth++; p.depth > maxDepth {
			return nil, errors.New("sidecar: KRDS nesting too deep")
		}
		defer func() { p.depth-- }()
		name, err := p.utf()
		if err != nil {
			return nil, err
		}
		o := &Object{}
		if name != nil {
			o.Name = *name
		}
		for {
			if p.i >= len(p.b) {
				return o, errShort
			}
			if p.b[p.i] == tEnd {
				p.i++
				return o, nil
			}
			v, err := p.value()
			if err != nil {
				return o, err
			}
			o.Values = append(o.Values, v)
		}
	default:
		return nil, fmt.Errorf("sidecar: unknown KRDS type 0x%02x at %d", t[0], p.i-1)
	}
}
