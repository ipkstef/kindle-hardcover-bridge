package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/mobi"

	_ "modernc.org/sqlite"
)

// ids lists, per book, which metadata fields exist and which identifiers the
// matcher would use, in match order. No titles or authors are written.
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
	rows, err := db.Query(`SELECT p_cdeKey, p_cdeType, p_location, p_mimeType,
			p_titles_0_nominal, j_credits, p_publisher, CAST(p_publicationDate AS TEXT), p_languages_0
		FROM Entries WHERE p_type = 'Entry:Item' AND p_cdeType IN ('EBOK','PDOC')
		AND p_mimeType != 'text/x-shellscript' AND p_location IS NOT NULL AND p_location != ''
		ORDER BY p_lastAccess DESC`)
	if err != nil {
		p("db: %v", err)
		return 1
	}
	defer rows.Close()

	p("hcprobe ids v0.3")
	p("per book: key | cdeType | ext | cc.db fields present | EXTH records (text types) | IDs in match order (kind:value@source, * = dedicated field) | titles/authors found")
	var total, withASIN, withISBN, withNone, kfx, bad, noExth int
	exthTypes := map[uint32]int{}
	for rows.Next() {
		var key, typ, loc, mime, title, credits, pub, date, lang sql.NullString
		if err := rows.Scan(&key, &typ, &loc, &mime, &title, &credits, &pub, &date, &lang); err != nil {
			p("row: %v", err)
			continue
		}
		total++
		k := key.String
		if len(k) > 8 {
			k = k[:8]
		}
		ext := strings.ToLower(filepath.Ext(loc.String))
		present := fieldsPresent(map[string]sql.NullString{
			"title": title, "credits": credits, "publisher": pub, "pubDate": date, "lang": lang,
		})

		l := book.Local{Key: key.String, Title: title.String, Authors: creditNames(credits.String),
			Path: loc.String, CDEType: typ.String, MimeType: mime.String,
			Publisher: pub.String, PubDate: date.String, Language: lang.String}

		var meta *mobi.Meta
		exthInfo := "-"
		switch {
		case ext == ".kfx" || ext == ".azw8" || ext == ".kpf" || strings.Contains(mime.String, "kfx"):
			kfx++
			exthInfo = "kfx: not parsed"
		default:
			m, err := mobi.ReadFile(loc.String)
			if err != nil {
				bad++
				exthInfo = "error: " + errNoPath(err)
				break
			}
			meta = m
			types := make([]string, 0, len(m.EXTH))
			for _, t := range m.Types() {
				types = append(types, fmt.Sprint(t))
				exthTypes[t]++
			}
			if m.Records == 0 {
				noExth++
			}
			exthInfo = fmt.Sprintf("%d (%s)", m.Records, strings.Join(types, ","))
		}

		id := book.BuildIdentity(l, meta)
		var idList []string
		hasA, hasI := false, false
		for _, x := range id.IDs {
			star := ""
			if x.Dedicated {
				star = "*"
			}
			idList = append(idList, fmt.Sprintf("%s:%s@%s%s", x.Kind, x.Value, x.Source, star))
			hasA = hasA || x.Kind == book.KindASIN
			hasI = hasI || x.Kind == book.KindISBN13
		}
		if hasA {
			withASIN++
		}
		if hasI {
			withISBN++
		}
		if !hasA && !hasI {
			withNone++
			idList = []string{"none"}
		}
		p("%s | %s | %s | %s | %s | %s | titles %d, authors %d, year %d",
			k, typ.String, ext, present, exthInfo, strings.Join(idList, " "),
			len(id.Titles), len(id.Authors), id.Year)
	}
	p("")
	p("summary: %d books, %d with ASIN, %d with ISBN, %d with neither, %d without EXTH, %d kfx (not parsed), %d errors",
		total, withASIN, withISBN, withNone, noExth, kfx, bad)
	var tl []string
	for t, n := range exthTypes {
		tl = append(tl, fmt.Sprintf("%d:%d", t, n))
	}
	sort.Strings(tl)
	p("EXTH text types seen (type:books): %s", strings.Join(tl, " "))
	return 0
}

func fieldsPresent(m map[string]sql.NullString) string {
	var have []string
	for _, k := range []string{"title", "credits", "publisher", "pubDate", "lang"} {
		if v := m[k]; v.Valid && strings.TrimSpace(v.String) != "" && v.String != "[]" {
			have = append(have, k)
		}
	}
	return strings.Join(have, ",")
}

func creditNames(s string) []string {
	var list []struct {
		Name struct {
			Display string `json:"display"`
		} `json:"name"`
	}
	if json.Unmarshal([]byte(s), &list) != nil {
		return nil
	}
	var out []string
	for _, c := range list {
		if c.Name.Display != "" {
			out = append(out, c.Name.Display)
		}
	}
	return out
}

// errNoPath hides the file path (it holds the title).
func errNoPath(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Op + ": " + pe.Err.Error()
	}
	return err.Error()
}
