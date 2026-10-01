package readers

import (
	"context"
	"testing"
)

// Replay on a real cc.db from FW 5.17.1 (Entries table, 3 rows, collation
// removed): the queries must keep working on the device's real schema.
func TestRealCCDB(t *testing.T) {
	d := &Database{Path: "testdata/cc-fw5.17.1.db"}
	ctx := context.Background()
	all, err := d.AllProgress(ctx)
	if err != nil || len(all) == 0 {
		t.Fatalf("AllProgress: %v %v", all, err)
	}
	cur, err := d.CurrentBook(ctx)
	if err != nil || cur.Title != "A Parade of Horribles" || cur.Percent < 9.8 || len(cur.Authors) == 0 {
		t.Fatalf("CurrentBook: %+v %v", cur, err)
	}
	b, err := d.BookByKey(ctx, "244d84a9-33c9-428a-aa3b-8abd1180d2d6")
	if err != nil || b.Title != "Off to Be the Wizard" || b.ReadState != 2 || b.Percent != 100 {
		t.Fatalf("BookByKey: %+v %v", b, err)
	}
	if p := all["244d84a9-33c9-428a-aa3b-8abd1180d2d6"]; p.ReadState != 2 {
		t.Fatalf("progress %+v", p)
	}
}
