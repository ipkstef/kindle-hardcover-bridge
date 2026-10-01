package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/daemon"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/events"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/metrics"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/screen"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/syncer"
)

const (
	// debounce: wait this long after the last trigger before a scan (the
	// reader writes cc.db ~3 s after leaving the book).
	debounce = 5 * time.Second
	// safetyPoll: scan at least this often while the device is awake.
	safetyPoll = 15 * time.Minute
	// memLimit: soft heap limit for the Go runtime. The garbage collector
	// works harder near it, so memory is given back sooner on small devices.
	// Idle RSS was ~12 MB (x86-64, 2026-09-28).
	memLimit = 24 << 20
	// alertHideMs: our rating box closes itself after this time.
	alertHideMs = 8000
	// alertWait: longest wait for the Goodreads error box before ours.
	alertWait = 3 * time.Second
)

func (a *app) pidPath() string   { return filepath.Join(a.stateDir, "daemon.pid") }
func (a *app) statePath() string { return filepath.Join(a.stateDir, "state.json") }

// daemon runs until SIGTERM. Triggers (docs/roadmap.md §1):
//   - LIPC appmgrd: appPaused "com.lab126.booklet.reader" (user left the book)
//   - LIPC powerd: goingToScreenSaver (sleep inside the book writes the
//     position; Wi-Fi stays up ~70 s after it)
//   - inotify: cc.db-journal deleted (DB commit) or cc.db-wal modified
//   - LIPC cmd: connectionAvailable (Wi-Fi back: retry pending)
//   - a safety poll every 15 min
//
// Each source is optional; the daemon works with any subset.
func (a *app) daemon(ctx context.Context) error {
	debug.SetMemoryLimit(memLimit)
	if err := os.MkdirAll(a.stateDir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(a.pidPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// An older daemon is running (maybe an old version, device log
		// 2026-09-28): stop it and take over, so "Start" always runs this
		// binary.
		old := a.running()
		log.Printf("daemon: replacing running daemon (pid %d)", old)
		if old != 0 {
			_ = syscall.Kill(old, syscall.SIGTERM)
		}
		locked := false
		for i := 0; i < 40 && !locked; i++ {
			time.Sleep(250 * time.Millisecond)
			locked = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
		}
		if !locked {
			return fmt.Errorf("old daemon (pid %d) did not stop", old)
		}
	}
	_ = lock.Truncate(0)
	_, _ = lock.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	signal.Ignore(syscall.SIGHUP)
	go func() {
		s := <-sig
		log.Printf("daemon: %v, stopping", s)
		cancel()
		// Safety net: a stuck call must never keep a stopped daemon alive
		// (then "Start" cannot replace it).
		time.Sleep(15 * time.Second)
		log.Printf("daemon: did not stop in 15 s, exiting")
		os.Exit(1)
	}()

	trig := make(chan string, 1)
	fire := func(why string) {
		select {
		case trig <- why:
		default: // a scan is already queued
		}
	}

	var caps []string
	onApp := func(line string) {
		if strings.Contains(line, "appPaused") && strings.Contains(line, "com.lab126.booklet.reader") {
			fire("left book")
		}
	}
	if err := events.LIPC(ctx, "com.lab126.appmgrd", onApp, log.Printf); err != nil {
		caps = append(caps, "lipc: "+err.Error())
	} else {
		caps = append(caps, "lipc appmgrd: ok")
		onPower := func(line string) {
			if strings.HasPrefix(strings.TrimSpace(line), "goingToScreenSaver") {
				fire("sleep")
			}
		}
		if err := events.LIPC(ctx, "com.lab126.powerd", onPower, log.Printf); err == nil {
			caps = append(caps, "lipc powerd: ok")
		}
		onNet := func(line string) {
			if strings.Contains(line, "connectionAvailable") {
				a.client().Online() // end offline mode: send what is waiting
				fire("network up")
			}
		}
		if err := events.LIPC(ctx, "com.lab126.cmd", onNet, log.Printf); err == nil {
			caps = append(caps, "lipc cmd: ok")
		}
	}
	dbDir, dbBase := filepath.Dir(a.db.Path), filepath.Base(a.db.Path)
	onFile := func(name string, mask uint32) {
		switch {
		case name == dbBase+"-journal" && mask&events.InDelete != 0,
			name == dbBase+"-wal" && mask&events.InModify != 0:
			fire("db commit")
		}
	}
	if _, err := events.WatchDir(ctx, dbDir, onFile); err != nil {
		caps = append(caps, "inotify: "+err.Error())
	} else {
		caps = append(caps, "inotify: ok")
	}
	log.Printf("daemon: started, version %s, pid %d (%s)", version, os.Getpid(), strings.Join(caps, ", "))
	var fs syscall.Statfs_t
	if syscall.Statfs(a.stateDir, &fs) == nil {
		log.Printf("daemon: %s: %d MB free of %d MB", a.stateDir,
			int64(fs.Bavail)*int64(fs.Bsize)>>20, int64(fs.Blocks)*int64(fs.Bsize)>>20)
	}

	clips := a.clipSync()
	ratings := a.rateSync()
	// The Kindle shows "Rating Error" for a Goodreads call it cannot make
	// (sideloaded books, probe 2026-09-28). Tell the user what really
	// happened to the tap (option C). The tap is sent at once (no debounce),
	// so the box comes ~1-2 s after it; an early box that said "will be
	// saved" was wrong for a book not found (device log 2026-09-28).
	ratings.OnResult = func(title string, stars float64, res syncer.RateResult) {
		var text string
		switch res {
		case syncer.RateSaved:
			text = fmt.Sprintf("%s: %g of 5 stars saved to Hardcover. You can ignore a Goodreads rating error.", title, stars)
		case syncer.RateNotFound:
			text = fmt.Sprintf("%s was not found on Hardcover, so the %g-star rating was not saved. Rate it on hardcover.app.", title, stars)
		case syncer.RateQueued:
			text = fmt.Sprintf("No connection. %g of 5 stars for %s will be sent to Hardcover later.", stars, title)
		}
		go ratingAlert(ctx, text)
	}
	a.syncer().OnNotFound = func(title string) {
		go ratingAlert(ctx, title+" was not found on Hardcover, or matches more than one book there. "+
			"Add the right book to a shelf on hardcover.app; it syncs within an hour.")
	}
	runRatings := func(ctx context.Context) {
		if n, err := ratings.Run(ctx); err != nil {
			if !a.client().Offline() { // offline: already known, no log per event
				log.Printf("daemon: ratings: %v (retry at next check)", err)
			}
		} else if n > 0 {
			log.Printf("daemon: ratings: %d saved", n)
		}
	}
	// All Hardcover writes run in this goroutine (scan, then ratings, then
	// clips), so two writes for one book never overlap.
	var d *daemon.Daemon
	ratings.OnReread = func(key string) { d.ClearFinished(key) }
	d = &daemon.Daemon{Src: a.db, Sync: a.syncer(), StatePath: a.statePath(), DB: a.stateDB(), Logf: log.Printf,
		Offline: a.client().Offline,
		SidecarPct: func(ctx context.Context, key string) (float64, time.Time, bool) {
			b, err := a.db.BookByKey(ctx, key)
			if err != nil {
				return 0, time.Time{}, false
			}
			return syncer.SidecarPercent(b)
		},
		After: func(ctx context.Context) {
			runRatings(ctx)
			if n, err := clips.Run(ctx, false); err != nil {
				if !a.client().Offline() {
					log.Printf("daemon: clips: %v (retry at next check)", err)
				}
			} else if n > 0 {
				log.Printf("daemon: clips: %d sent", n)
			}
		}}
	// The Kindle empties fmcache.db soon after a star tap, so the watcher
	// copies new taps at once (no network), then asks for a scan to send them.
	collect := func(ctx context.Context) {
		n, err := ratings.Collect(ctx)
		if err != nil {
			log.Printf("daemon: ratings: %v", err)
		}
		if n > 0 {
			fire("rating")
		}
	}
	go watchRatings(ctx, filepath.Dir(metrics.DefaultPath), collect)
	// Wall clock (Round(0) drops the monotonic reading): the monotonic
	// clock stops while the Kindle sleeps, so "one hour" never came in 3
	// days of mostly-asleep use (device log 2026-10-01).
	counts, countsAt := map[string]int{}, time.Now().Round(0)
	scan := func(why string) {
		sctx, scancel := context.WithTimeout(ctx, 5*time.Minute)
		defer scancel()
		// Quiet log: events are counted and summed up once an hour; a scan
		// logs only real changes and errors.
		for _, w := range strings.Split(why, ", ") {
			counts[w]++
		}
		if time.Since(countsAt) >= time.Hour {
			logCounts(counts, countsAt)
			counts, countsAt = map[string]int{}, time.Now().Round(0)
		}
		if err := d.ScanFor(sctx, why); err != nil && ctx.Err() == nil {
			log.Printf("daemon: scan (%s): %v", why, err)
		}
		// User actions (few per day) always get one line, so a log shows
		// whether the Kindle wrote a new position for them.
		if strings.Contains(why, "sleep") || strings.Contains(why, "left book") {
			if n, book, pct, side := d.LastScan(); n == 0 {
				log.Printf("daemon: scan (%s): no new position in cc.db (latest book %s at %.2f%%; %s)", why, book, pct, side)
			}
		}
	}
	scan("start")

	poll := time.NewTicker(safetyPoll)
	defer poll.Stop()
	for {
		var why string
		select {
		case <-ctx.Done():
			log.Printf("daemon: stopped")
			return nil
		case why = <-trig:
		case <-poll.C:
			if readerOpen(ctx) {
				// The Kindle writes cc.db when the user leaves the book or
				// sleeps; both are events. Do not read cc.db meanwhile.
				continue
			}
			why = "poll"
		}
		// Debounce: more triggers in the next seconds join this scan. A star
		// tap is sent at once (the user is looking at the screen).
		wait := debounce
		if why == "rating" {
			wait = 0
		}
		t := time.NewTimer(wait)
	wait:
		for {
			select {
			case <-ctx.Done():
				t.Stop()
				log.Printf("daemon: stopped")
				return nil
			case w := <-trig:
				why += ", " + w
				if w == "rating" {
					t.Stop() // a star tap does not wait for other events
					break wait
				}
			case <-t.C:
				break wait
			}
		}
		scan(why)
	}
}

// logCounts writes one line with the events of the last period.
func logCounts(c map[string]int, since time.Time) {
	if len(c) == 0 {
		return
	}
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, c[k])
	}
	log.Printf("daemon: events since %s: %s", since.UTC().Format("15:04"), strings.Join(parts, ", "))
}

// readerOpen reports if the stock reader is the active window (winmgr).
// Without LIPC it reports false, so the poll still runs.
func readerOpen(ctx context.Context) bool {
	return strings.Contains(lipcGet(ctx, "com.lab126.winmgr", "getActiveAppTitle"), "com.lab126.booklet.reader")
}

// ratingAlert shows the rating result on top of the Kindle's Goodreads
// error box: it waits until that box is open (a second dialog, at most
// alertWait), so ours is not hidden under it.
func ratingAlert(ctx context.Context, text string) {
	for end := time.Now().Add(alertWait); time.Now().Before(end) && ctx.Err() == nil; {
		if n, _ := strconv.Atoi(lipcGet(ctx, "com.lab126.winmgr", "activeDialogCount")); n >= 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := screen.Alert("Hardcover", text, alertHideMs); err != nil {
		log.Printf("daemon: alert: %v", err)
	}
}

// lipcGet reads one LIPC property. String properties need -s: without it
// lipc-get-prop tries Int first, and winmgr logs a warning to the system log
// on each read (device probe 2026-09-28).
func lipcGet(ctx context.Context, svc, prop string) string {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	args := []string{svc, prop}
	if prop == "getActiveAppTitle" {
		args = []string{"-s", svc, prop}
	}
	b, err := exec.CommandContext(c, "lipc-get-prop", args...).CombinedOutput()
	if err != nil {
		return "error: " + err.Error()
	}
	return strings.TrimSpace(string(b))
}

// watchRatings runs fn ~1 s after fmcache.db changes. /mnt/us disappears in
// USB mode, so it watches again when the folder is back.
func watchRatings(ctx context.Context, dir string, fn func(context.Context)) {
	kick := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-kick:
			}
			// The trigger is the end of the Kindle's commit; a short pause
			// lets a burst of commits join one read.
			time.Sleep(300 * time.Millisecond)
			fn(ctx)
		}
	}()
	logged := false
	for ctx.Err() == nil {
		done, err := events.WatchDir(ctx, dir, func(name string, mask uint32) {
			// React to the end of a commit: journal deleted (rollback mode) or
			// WAL written. A plain write to fmcache.db comes in the middle of
			// a commit; reading then fails with "readonly database (776)"
			// (hot journal, device log 2026-09-28).
			if (name == "fmcache.db-journal" && mask&events.InDelete != 0) ||
				(name == "fmcache.db-wal" && mask&events.InModify != 0) ||
				(name == "fmcache.db" && mask&events.InCloseWrite != 0) {
				select {
				case kick <- struct{}{}:
				default:
				}
			}
		})
		if err != nil {
			if !logged {
				log.Printf("daemon: ratings watch %s: %v (will retry)", dir, err)
				logged = true
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
			}
			continue
		}
		log.Printf("daemon: ratings watch on %s", dir)
		logged = false
		<-done
	}
}

// running returns the daemon's pid, or 0.
func (a *app) running() int {
	b, err := os.ReadFile(a.pidPath())
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0
	}
	if syscall.Kill(pid, 0) != nil {
		return 0
	}
	// The pid file is locked while the daemon runs.
	f, err := os.Open(a.pidPath())
	if err != nil {
		return 0
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return 0 // not locked: stale pid
	}
	return pid
}

func (a *app) stop() error {
	pid := a.running()
	if pid == 0 {
		a.screen.Show("Background sync is not running.")
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	for i := 0; i < 20 && a.running() != 0; i++ {
		time.Sleep(250 * time.Millisecond)
	}
	log.Printf("stop: daemon %d stopped", pid)
	a.screen.Show("Background sync stopped.")
	return nil
}

func (a *app) status() error {
	var lastScan time.Time
	var lastResult string
	var pending int
	if db := a.stateDB(); db != nil {
		lastScan, lastResult, pending = daemon.ReadStatus(db)
	} else {
		st, err := daemon.LoadState(a.statePath())
		if err != nil {
			return err
		}
		lastScan, lastResult, pending = st.LastScan, st.LastResult, len(st.Pending)
	}
	run := "Background sync: OFF"
	if pid := a.running(); pid != 0 {
		run = fmt.Sprintf("Background sync: ON (pid %d)", pid)
	}
	last := "Last check: never"
	if !lastScan.IsZero() {
		last = "Last check: " + lastScan.Local().Format("Jan 2 15:04")
	}
	res := lastResult
	if res == "" {
		res = "-"
	}
	signed := "Signed in: yes"
	if _, err := a.store.Load(); err != nil {
		signed = "Signed in: NO"
	}
	a.screen.Show(run, signed, last, "Last result:", trim(res, 46), fmt.Sprintf("Waiting to retry: %d", pending),
		"Version: "+version)
	return nil
}
