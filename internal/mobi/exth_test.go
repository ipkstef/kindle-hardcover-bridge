package mobi

import (
	"bytes"
	"encoding/binary"
	"testing"
)

type rec struct {
	typ uint32
	val []byte
}

// build makes a minimal MOBI file with a full name and EXTH records.
func build(fullName string, recs []rec, withEXTH bool) []byte {
	var exth bytes.Buffer
	for _, r := range recs {
		binary.Write(&exth, binary.BigEndian, r.typ)
		binary.Write(&exth, binary.BigEndian, uint32(8+len(r.val)))
		exth.Write(r.val)
	}
	const mobiLen = 0xE8
	var f bytes.Buffer
	pdb := make([]byte, 78)
	copy(pdb[60:], "BOOKMOBI")
	binary.BigEndian.PutUint16(pdb[76:], 1)
	f.Write(pdb)
	binary.Write(&f, binary.BigEndian, uint32(78+8)) // record 0 offset
	f.Write(make([]byte, 4))

	rec0 := make([]byte, 16+mobiLen)
	binary.BigEndian.PutUint32(rec0[4:], 933800) // text length
	copy(rec0[16:], "MOBI")
	binary.BigEndian.PutUint32(rec0[20:], mobiLen)
	binary.BigEndian.PutUint32(rec0[0x80:], 0x40) // EXTH flag
	exthLen := 0
	if withEXTH {
		exthLen = 12 + exth.Len()
	}
	binary.BigEndian.PutUint32(rec0[0x54:], uint32(16+mobiLen+exthLen)) // full name offset
	binary.BigEndian.PutUint32(rec0[0x58:], uint32(len(fullName)))
	f.Write(rec0)
	if withEXTH {
		f.WriteString("EXTH")
		binary.Write(&f, binary.BigEndian, uint32(exthLen))
		binary.Write(&f, binary.BigEndian, uint32(len(recs)))
		f.Write(exth.Bytes())
	}
	f.WriteString(fullName)
	return f.Bytes()
}

func TestRead(t *testing.T) {
	data := build("Parade of Horribles", []rec{
		{ExthISBN, []byte("978-0-593-82025-3")},
		{ExthASIN, []byte("f1638687-3e46-45b5-b4b2-8f37103c1743")},
		{ExthAuthor, []byte("Matt Dinniman")},
		{ExthAuthor, []byte("Second Author")},
		{ExthTitle, []byte("A Parade of Horribles")},
		{999, []byte{0, 0, 0, 1}}, // binary record: counted, not kept
	}, true)
	m, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if m.Records != 6 || len(m.EXTH) != 4 {
		t.Fatalf("records %d, text types %v", m.Records, m.Types())
	}
	if m.First(ExthISBN) != "978-0-593-82025-3" || len(m.EXTH[ExthAuthor]) != 2 || m.Title() != "A Parade of Horribles" {
		t.Fatalf("got %+v", m)
	}
	if m.TextLength != 933800 {
		t.Fatalf("text length %d", m.TextLength)
	}
	if m.FullName != "Parade of Horribles" {
		t.Fatalf("full name %q", m.FullName)
	}
}

func TestNoEXTH(t *testing.T) {
	m, err := Read(bytes.NewReader(build("Only A Name", nil, false)))
	if err != nil || m.Records != 0 || m.Title() != "Only A Name" {
		t.Fatalf("got %+v %v", m, err)
	}
}

func TestNotMobi(t *testing.T) {
	if _, err := Read(bytes.NewReader(make([]byte, 200))); err != ErrNotMobi {
		t.Fatalf("got %v", err)
	}
}
