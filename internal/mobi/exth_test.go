package mobi

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// build makes a minimal MOBI file with the given EXTH records.
func build(recs map[uint32]string) []byte {
	var exth bytes.Buffer
	for typ, v := range recs {
		binary.Write(&exth, binary.BigEndian, typ)
		binary.Write(&exth, binary.BigEndian, uint32(8+len(v)))
		exth.WriteString(v)
	}
	var f bytes.Buffer
	pdb := make([]byte, 78)
	copy(pdb[60:], "BOOKMOBI")
	binary.BigEndian.PutUint16(pdb[76:], 1)
	f.Write(pdb)
	binary.Write(&f, binary.BigEndian, uint32(78+8)) // record 0 offset
	f.Write(make([]byte, 4))
	const mobiLen = 0xE8
	rec0 := make([]byte, 16+mobiLen)
	copy(rec0[16:], "MOBI")
	binary.BigEndian.PutUint32(rec0[20:], mobiLen)
	binary.BigEndian.PutUint32(rec0[0x80:], 0x40) // EXTH flag, offset from record 0 start
	f.Write(rec0)
	f.WriteString("EXTH")
	binary.Write(&f, binary.BigEndian, uint32(12+exth.Len()))
	binary.Write(&f, binary.BigEndian, uint32(len(recs)))
	f.Write(exth.Bytes())
	return f.Bytes()
}

func TestRead(t *testing.T) {
	data := build(map[uint32]string{
		ExthISBN: "978-0-593-82025-3", ExthASIN: "B0CW1Q9K2F", ExthTitle: "A Parade of Horribles", ExthAuthor: "Matt Dinniman",
	})
	m, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if m.Records != 4 || CleanISBN(m.ISBN) != "9780593820253" || !IsASIN(m.ASIN) || m.Title != "A Parade of Horribles" || m.Authors[0] != "Matt Dinniman" {
		t.Fatalf("got %+v", m)
	}
}

func TestNotMobi(t *testing.T) {
	if _, err := Read(bytes.NewReader(make([]byte, 200))); err != ErrNotMobi {
		t.Fatalf("got %v", err)
	}
}

func TestCalibreUUIDIsNotASIN(t *testing.T) {
	if IsASIN("f1638687-3e46-45b5-b4b2-8f37103c1743") {
		t.Fatal("uuid taken as ASIN")
	}
}

func TestNoEXTH(t *testing.T) {
	data := build(nil)
	data = data[:len(data)-12] // drop the EXTH header
	m, err := Read(bytes.NewReader(data))
	if err != nil || m.Records != 0 {
		t.Fatalf("got %+v %v", m, err)
	}
}
