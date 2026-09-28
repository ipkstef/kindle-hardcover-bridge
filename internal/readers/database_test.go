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
			j_credits, p_location, p_percentFinished, p_lastAccess, p_mimeType,
			p_publisher, p_publicationDate, p_languages_0, p_lastAccessedPosition, p_readState)`,
		`INSERT INTO Entries VALUES ('1','Entry:Item','PDOC','*9de','KUAL','[]','/mnt/us/documents/KUAL.sh',0,300,'text/x-shellscript',NULL,NULL,NULL,NULL,NULL)`,
		`INSERT INTO Entries VALUES ('2','Entry:Item','EBOK','f163','A Parade of Horribles',
			'[{"name":{"display":"Matt Dinniman"},"kind":"Author"}]','/mnt/us/documents/p.azw3',10.723627,200,'application/x-mobi8-ebook','Penguin Group','2024-02-06T00:00:00+0000','en',NULL,NULL)`,
		`INSERT INTO Entries VALUES ('3','Entry:Item','EBOK','7f13','Old Book','[]','/mnt/us/documents/o.mobi',3.5,100,'application/x-mobipocket-ebook',NULL,NULL,NULL,NULL,NULL)`,
		`INSERT INTO Entries VALUES ('4','Entry:Item','EBOK','dddd','Never Opened','[]','/mnt/us/documents/n.mobi',NULL,400,'application/x-mobipocket-ebook',NULL,NULL,NULL,NULL,NULL)`,
		`INSERT INTO Entries VALUES ('5','Collection',NULL,NULL,'My Shelf','[]','c',NULL,500,NULL,NULL,NULL,NULL,NULL,NULL)`,
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
	if b.Key != "f163" || b.Percent != 10.723627 || len(b.Authors) != 1 || b.Authors[0] != "Matt Dinniman" ||
		b.Publisher != "Penguin Group" || b.Language != "en" || b.CDEType != "EBOK" {
		t.Fatalf("got %+v", b)
	}

	all, err := (&Database{Path: path}).AllProgress(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// KUAL script, never-opened book and collection are left out.
	if len(all) != 2 || all["f163"].Percent != 10.723627 || all["7f13"].Percent != 3.5 {
		t.Fatalf("AllProgress = %+v", all)
	}
	b, err = (&Database{Path: path}).BookByKey(context.Background(), "7f13")
	if err != nil || b.Title != "Old Book" {
		t.Fatalf("BookByKey = %+v %v", b, err)
	}
}
