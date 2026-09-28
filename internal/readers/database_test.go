package readers

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCurrentBook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cc.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Same column names as cc.db on FW 5.17.1 (only the ones we read).
	stmts := []string{
		`CREATE TABLE Entries (p_uuid PRIMARY KEY, p_type, p_cdeType, p_cdeKey, p_titles_0_nominal,
			j_credits, p_location, p_percentFinished, p_lastAccess, p_mimeType)`,
		`INSERT INTO Entries VALUES ('1','Entry:Item','PDOC','*9de','KUAL','[]','/mnt/us/documents/KUAL.sh',0,300,'text/x-shellscript')`,
		`INSERT INTO Entries VALUES ('2','Entry:Item','EBOK','f163','A Parade of Horribles',
			'[{"name":{"display":"Matt Dinniman"},"kind":"Author"}]','/mnt/us/documents/p.azw3',10.723627,200,'application/x-mobi8-ebook')`,
		`INSERT INTO Entries VALUES ('3','Entry:Item','EBOK','7f13','Old Book','[]','/mnt/us/documents/o.mobi',3.5,100,'application/x-mobipocket-ebook')`,
		`INSERT INTO Entries VALUES ('4','Entry:Item','EBOK','dddd','Never Opened','[]','/mnt/us/documents/n.mobi',NULL,400,'application/x-mobipocket-ebook')`,
		`INSERT INTO Entries VALUES ('5','Collection',NULL,NULL,'My Shelf','[]','c',NULL,500,NULL)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	b, err := (&Database{Path: path}).CurrentBook(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b.Key != "f163" || b.Percent != 10.723627 || len(b.Authors) != 1 || b.Authors[0] != "Matt Dinniman" {
		t.Fatalf("got %+v", b)
	}
}
