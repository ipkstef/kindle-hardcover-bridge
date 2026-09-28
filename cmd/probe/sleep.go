package main

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/events"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/readers"

	_ "modernc.org/sqlite"
)

// sleep records every candidate source of the reading position at each
// event, to find one that is written when the user sleeps inside a book
// (cc.db percent is only written on "go Home").
//
// Output folder (default /mnt/us/hcprobe-sleep):
//
//	timeline.txt          every LIPC / inotify event, with times
//	lipc-all.txt          lipc-probe of all services, once at start
//	NN-<event>/report.txt state at that moment
//	NN-<event>/sdr/...    raw copies of sidecar files that changed
func sleepProbe(args []string) int {
	fs := flag.NewFlagSet("sleep", flag.ExitOnError)
	out := fs.String("out", "/mnt/us/hcprobe-sleep", "output folder")
	dbPath := fs.String("db", "/var/local/cc.db", "cc.db path")
	varDir := fs.String("var", "/var/local", "folder to scan for changed files")
	secs := fs.Int("secs", 2700, "run time in seconds")
	tick := fs.Int("tick", 120, "periodic snapshot interval in seconds")
	extra := fs.String("pubs", "", "extra LIPC publishers (comma list); every event from them makes a snapshot")
	scan := fs.String("scan", "", "extra folders to scan for changed files (comma list)")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	tl, err := os.Create(filepath.Join(*out, "timeline.txt"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer tl.Close()

	dirs := []string{*varDir}
	for _, d := range strings.Split(*scan, ",") {
		if d = strings.TrimSpace(d); d != "" {
			dirs = append(dirs, d)
		}
	}
	p := &sleepRec{out: *out, db: &readers.Database{Path: *dbPath}, varDir: *varDir, scanDirs: dirs,
		tl: tl, sdrHash: map[string]string{}, lipcHash: map[string]string{}}
	p.logf("hcprobe sleep v0.1, %d s", *secs)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*secs)*time.Second)
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sig; p.logf("stop signal"); cancel() }()

	p.lipcAll(ctx)
	p.findBook(ctx, "")
	p.lastScan = time.Now().Add(-time.Minute)
	p.snapshot(ctx, "start")

	snap := make(chan string, 8)
	req := func(why string) {
		select {
		case snap <- why:
		default:
		}
	}

	interesting := regexp.MustCompile(`goingToScreenSaver|outOfScreenSaver|exitingScreenSaver|readyToSuspend|suspending|resuming|wakeup|appPaused|appActivating 1|historyChange|connectionAvailable|connectionNotAvailable|cmDisconnected|cmConnected`)
	pubs := []string{"com.lab126.powerd", "com.lab126.appmgrd", "com.lab126.wifid", "com.lab126.cmd",
		"com.lab126.readingstreams", "com.lab126.booklet.reader", "com.lab126.reader", "com.lab126.reader.readingtimer"}
	extraSet := map[string]bool{}
	for _, e := range strings.Split(*extra, ",") {
		if e = strings.TrimSpace(e); e != "" {
			pubs = append(pubs, e)
			extraSet[e] = true
		}
	}
	for _, pub := range pubs {
		pub := pub
		err := events.LIPC(ctx, pub, func(line string) {
			p.logf("lipc %s: %s", pub, line)
			if strings.Contains(line, "historyChange") {
				if m := regexp.MustCompile(`"file://([^"]+)"`).FindStringSubmatch(line); m != nil {
					if path, err := url.PathUnescape(m[1]); err == nil {
						p.findBook(ctx, path)
					}
				}
			}
			if extraSet[pub] || interesting.MatchString(line) {
				ev := strings.Fields(line)[0]
				req(pub[strings.LastIndex(pub, ".")+1:] + "-" + ev)
			}
		}, p.logf)
		if err != nil {
			p.logf("lipc %s: %v", pub, err)
		}
	}

	// inotify on /var/local (names only, cc.db and others).
	_, _ = events.WatchDir(ctx, *varDir, func(name string, mask uint32) {
		if mask&(events.InCloseWrite|events.InDelete|events.InMovedTo) != 0 {
			p.logf("inotify %s: %s %s", *varDir, name, maskStr(mask))
		}
	})

	t := time.NewTicker(time.Duration(*tick) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			p.snapshot(context.Background(), "end")
			p.logf("end")
			return 0
		case why := <-snap:
			time.Sleep(3 * time.Second) // let the reader finish writing
			for more := true; more; {
				select {
				case w := <-snap:
					why += "+" + w
				default:
					more = false
				}
			}
			p.snapshot(ctx, why)
		case <-p.sdrEvent():
			time.Sleep(2 * time.Second)
			p.snapshot(ctx, "sdr-write")
		case <-t.C:
			p.snapshot(ctx, "tick")
		}
	}
}

type sleepRec struct {
	mu       sync.Mutex
	out      string
	db       *readers.Database
	varDir   string
	tl       *os.File
	n        int
	book     string // book file path
	sdrDir   string
	sdrHash  map[string]string
	lipcHash map[string]string
	lastScan time.Time
	scanDirs []string
	sdrCh    chan struct{}
	sdrStop  context.CancelFunc
}

func (p *sleepRec) logf(f string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.tl, "%s %s\n", time.Now().UTC().Format("15:04:05.000"), fmt.Sprintf(f, a...))
	p.tl.Sync()
}

func (p *sleepRec) sdrEvent() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sdrCh
}

// findBook sets the current book (from a historyChange path, or cc.db) and
// watches its .sdr folder with inotify.
func (p *sleepRec) findBook(ctx context.Context, path string) {
	if path == "" {
		if b, err := p.db.CurrentBook(ctx); err == nil {
			path = b.Path
		}
	}
	if path == "" || path == p.book {
		return
	}
	sdr := strings.TrimSuffix(path, filepath.Ext(path)) + ".sdr"
	p.mu.Lock()
	p.book, p.sdrDir = path, sdr
	if p.sdrStop != nil {
		p.sdrStop()
	}
	wctx, stop := context.WithCancel(ctx)
	p.sdrStop = stop
	ch := make(chan struct{}, 1)
	p.sdrCh = ch
	p.mu.Unlock()
	p.logf("book: %s (sdr %s)", filepath.Base(path), filepath.Base(sdr))
	_, err := events.WatchDir(wctx, sdr, func(name string, mask uint32) {
		p.logf("inotify sdr: %s %s", name, maskStr(mask))
		if mask&(events.InCloseWrite|events.InMovedTo) != 0 {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		p.logf("inotify sdr: %v", err)
	}
}

func (p *sleepRec) snapshot(ctx context.Context, why string) {
	p.n++
	label := regexp.MustCompile(`[^A-Za-z0-9+-]+`).ReplaceAllString(why, "_")
	if len(label) > 60 {
		label = label[:60]
	}
	dir := filepath.Join(p.out, fmt.Sprintf("%02d-%s", p.n, label))
	_ = os.MkdirAll(dir, 0o755)
	rep, err := os.Create(filepath.Join(dir, "report.txt"))
	if err != nil {
		p.logf("snapshot: %v", err)
		return
	}
	defer rep.Close()
	w := func(f string, a ...any) { fmt.Fprintf(rep, f+"\n", a...) }
	now := time.Now()
	w("snapshot %d: %s at %s", p.n, why, now.UTC().Format(time.RFC3339))
	p.logf("snapshot %d: %s", p.n, why)

	w("\n== state ==")
	w("power: %s", lipcGet(ctx, "com.lab126.powerd", "state"))
	w("wifi: %s", lipcGet(ctx, "com.lab126.wifid", "cmState"))

	w("\n== cc.db current book ==")
	p.ccRow(ctx, w)

	w("\n== sidecar %s ==", filepath.Base(p.sdrDir))
	p.copySdr(dir, w)

	w("\n== LIPC reader services (only if changed) ==")
	for _, svc := range []string{"com.lab126.reader", "com.lab126.booklet.reader", "com.lab126.reader.readingtimer",
		"com.lab126.readingstreams", "com.lab126.powerd"} {
		outp := run(ctx, 15*time.Second, "lipc-probe", "-v", svc)
		h := hash([]byte(outp))
		if p.lipcHash[svc] == h {
			w("%s: unchanged", svc)
			continue
		}
		p.lipcHash[svc] = h
		w("--- %s ---\n%s", svc, outp)
	}

	for _, d := range p.scanDirs {
		w("\n== changed files in %s since last snapshot ==", d)
		p.changedFiles(ctx, d, w)
	}
	p.lastScan = now
}

func (p *sleepRec) ccRow(ctx context.Context, w func(string, ...any)) {
	db, err := sql.Open("sqlite", "file:"+p.db.Path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		w("error: %v", err)
		return
	}
	defer db.Close()
	var key, pos, loc sql.NullString
	var pct sql.NullFloat64
	var last, rs sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT p_cdeKey, p_percentFinished, p_lastAccess,
		CAST(p_lastAccessedPosition AS TEXT), p_readState, p_location FROM Entries
		WHERE p_location = ?`, p.book).Scan(&key, &pct, &last, &pos, &rs, &loc)
	if err != nil {
		w("error: %v", err)
		return
	}
	w("key %s | percent %.6f | lastAccess %d | lastAccessedPosition %q | readState %v",
		shortKey(key.String), pct.Float64, last.Int64, pos.String, rs.Int64)
}

func (p *sleepRec) copySdr(dir string, w func(string, ...any)) {
	ents, err := os.ReadDir(p.sdrDir)
	if err != nil {
		w("error: %v", err)
		return
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		src := filepath.Join(p.sdrDir, e.Name())
		b, err := os.ReadFile(src)
		if err != nil {
			w("%s: %v", e.Name(), err)
			continue
		}
		fi, _ := e.Info()
		h := hash(b)
		changed := p.sdrHash[src] != h
		mark := "same"
		if changed {
			mark = "CHANGED, copied"
			p.sdrHash[src] = h
			ext := strings.TrimPrefix(filepath.Ext(e.Name()), ".")
			_ = os.MkdirAll(filepath.Join(dir, "sdr"), 0o755)
			// File names hold the title; keep only the extension.
			_ = os.WriteFile(filepath.Join(dir, "sdr", "sidecar."+ext), b, 0o644)
		}
		w("*.%s | %d bytes | mtime %s | sha1 %s | %s", strings.TrimPrefix(filepath.Ext(e.Name()), "."),
			len(b), fi.ModTime().UTC().Format("15:04:05"), h[:12], mark)
	}
}

// changedFiles lists files under varDir changed since the last snapshot.
// For changed SQLite DBs it dumps table names, row counts and newest rows.
func (p *sleepRec) changedFiles(ctx context.Context, root string, w func(string, ...any)) {
	count := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || count > 5000 {
			return nil
		}
		if d.IsDir() {
			if strings.Count(strings.TrimPrefix(path, root), "/") > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		fi, err := d.Info()
		if err != nil || !fi.ModTime().After(p.lastScan) {
			return nil
		}
		w("%s | %d bytes | %s", path, fi.Size(), fi.ModTime().UTC().Format("15:04:05"))
		if strings.HasSuffix(path, ".db") && path != p.db.Path && fi.Size() < 20<<20 {
			dumpDB(ctx, path, w)
		}
		return nil
	})
}

func dumpDB(ctx context.Context, path string, w func(string, ...any)) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		return
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		w("   db error: %v", err)
		return
	}
	var tables []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			tables = append(tables, n)
		}
	}
	rows.Close()
	sort.Strings(tables)
	for _, t := range tables {
		var n int
		_ = db.QueryRowContext(ctx, `SELECT count(*) FROM "`+t+`"`).Scan(&n)
		w("   table %s: %d rows", t, n)
		r, err := db.QueryContext(ctx, `SELECT * FROM "`+t+`" ORDER BY rowid DESC LIMIT 5`)
		if err != nil {
			continue
		}
		cols, _ := r.Columns()
		for r.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if r.Scan(ptrs...) != nil {
				continue
			}
			var parts []string
			for i, c := range cols {
				s := fmt.Sprint(vals[i])
				if b, ok := vals[i].([]byte); ok {
					s = string(b)
					if !isPrintable(s) {
						s = "hex:" + hex.EncodeToString(b)
					}
				}
				if len(s) > 300 {
					s = s[:300] + "…"
				}
				parts = append(parts, c+"="+s)
			}
			w("     %s", strings.Join(parts, " | "))
		}
		r.Close()
	}
}

func lipcGet(ctx context.Context, svc, prop string) string {
	return strings.TrimSpace(run(ctx, 5*time.Second, "lipc-get-prop", svc, prop))
}

// lipcAll saves lipc-probe output for all services once.
func (p *sleepRec) lipcAll(ctx context.Context) {
	f, err := os.Create(filepath.Join(p.out, "lipc-all.txt"))
	if err != nil {
		return
	}
	defer f.Close()
	io.WriteString(f, "== lipc-probe -l ==\n"+run(ctx, 30*time.Second, "lipc-probe", "-l"))
	io.WriteString(f, "\n== lipc-probe -a -v ==\n"+run(ctx, 120*time.Second, "lipc-probe", "-a", "-v"))
	p.logf("lipc-all.txt written")
}

func run(ctx context.Context, d time.Duration, name string, args ...string) string {
	c, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	b, err := exec.CommandContext(c, name, args...).CombinedOutput()
	if err != nil {
		return string(b) + fmt.Sprintf("\n(error: %v)", err)
	}
	return string(b)
}

func hash(b []byte) string {
	s := sha1.Sum(b)
	return hex.EncodeToString(s[:])
}

func shortKey(k string) string {
	if len(k) > 8 {
		return k[:8]
	}
	return k
}

func isPrintable(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

func maskStr(m uint32) string {
	var out []string
	for _, n := range []struct {
		b uint32
		s string
	}{{events.InModify, "MODIFY"}, {events.InCloseWrite, "CLOSE_WRITE"}, {events.InCreate, "CREATE"},
		{events.InDelete, "DELETE"}, {events.InMovedTo, "MOVED_TO"}} {
		if m&n.b != 0 {
			out = append(out, n.s)
		}
	}
	return strings.Join(out, "|")
}
