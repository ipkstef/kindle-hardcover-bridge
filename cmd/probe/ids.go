package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/mobi"

	_ "modernc.org/sqlite"
)

// ids lists, per book, what identifiers are inside the book file.
// No titles are written, only the first 8 chars of the Kindle key.
func ids(args []string) int {
	fs := flag.NewFlagSet("ids", flag.ExitOnError)
	out := fs.String("out", "/mnt/us/hcprobe-ids.txt", "report file")
	dbPath := fs.String("db", "/var/local/cc.db", "cc.db path")
	_ = fs.Parse(args)

	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer f.Close()
	p := func(format string, a ...any) { fmt.Fprintf(f, format+"\n", a...) }

	db, err := sql.Open("sqlite", "file:"+*dbPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		p("db: %v", err)
		return 1
	}
	defer db.Close()
	rows, err := db.Query(`SELECT p_cdeKey, p_cdeType, p_location, p_readState, p_percentFinished
		FROM Entries WHERE p_type = 'Entry:Item' AND p_cdeType IN ('EBOK','PDOC')
		AND p_mimeType != 'text/x-shellscript' ORDER BY p_lastAccess DESC`)
	if err != nil {
		p("db: %v", err)
		return 1
	}
	defer rows.Close()

	p("hcprobe ids v0.1")
	p("cols: key | cdeType | ext | ISBN (EXTH 104) | ASIN (EXTH 113/504) | readState | percent")
	var total, isbn, asin, none, kfx, bad int
	for rows.Next() {
		var key, typ, loc sql.NullString
		var rs sql.NullInt64
		var pct sql.NullFloat64
		if err := rows.Scan(&key, &typ, &loc, &rs, &pct); err != nil {
			p("row: %v", err)
			continue
		}
		total++
		k := key.String
		if len(k) > 8 {
			k = k[:8]
		}
		ext := strings.ToLower(filepath.Ext(loc.String))
		rsS, pctS := "-", "-"
		if rs.Valid {
			rsS = fmt.Sprint(rs.Int64)
		}
		if pct.Valid {
			pctS = fmt.Sprintf("%.2f", pct.Float64)
		}
		if ext == ".kfx" || ext == ".azw8" || ext == ".kpf" {
			kfx++
			p("%s | %s | %s | kfx: not parsed | | %s | %s", k, typ.String, ext, rsS, pctS)
			continue
		}
		m, err := mobi.ReadFile(loc.String)
		if err != nil {
			bad++
			p("%s | %s | %s | error: %v | | %s | %s", k, typ.String, ext, err, rsS, pctS)
			continue
		}
		i, a := mobi.CleanISBN(m.ISBN), m.ASIN
		if !mobi.IsASIN(a) {
			a = ""
		}
		switch {
		case i != "":
			isbn++
		}
		if a != "" {
			asin++
		}
		if i == "" && a == "" {
			none++
		}
		p("%s | %s | %s | %s | %s | %s | %s", k, typ.String, ext, i, a, rsS, pctS)
	}
	p("")
	p("summary: %d books, %d with ISBN, %d with ASIN, %d with neither, %d kfx (not parsed), %d errors",
		total, isbn, asin, none, kfx, bad)
	return 0
}
