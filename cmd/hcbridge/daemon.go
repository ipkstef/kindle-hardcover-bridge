package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/daemon"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/events"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/metrics"
)

const (
	// debounce: wait this long after the last trigger before a scan (the
	// reader writes cc.db ~3 s after leaving the book).
	debounce = 5 * time.Second
	// safetyPoll: scan at least this often while the device is awake.
	safetyPoll = 15 * time.Minute
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
	if err := os.MkdirAll(a.stateDir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(a.pidPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		log.Printf("daemon: already running")
		return nil
	}
	_ = lock.Truncate(0)
	_, _ = lock.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	signal.Ignore(syscall.SIGHUP)
	go func() { s := <-sig; log.Printf("daemon: %v, stopping", s); cancel() }()

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
	log.Printf("daemon: started, pid %d (%s)", os.Getpid(), strings.Join(caps, ", "))

	clips := a.clipSync()
	ratings := a.rateSync()
	ratings.OnTap = func() { go logDialog(ctx) }
	runRatings := func(ctx context.Context) {
		if n, err := ratings.Run(ctx); err != nil {
			log.Printf("daemon: ratings: %v (retry at next check)", err)
		} else if n > 0 {
			log.Printf("daemon: ratings: %d sent", n)
		}
	}
	// The Kindle empties fmcache.db soon after a star tap, so read it right
	// after it changes (not only at scans).
	go watchRatings(ctx, filepath.Dir(metrics.DefaultPath), runRatings)
	d := &daemon.Daemon{Src: a.db, Sync: a.syncer(), StatePath: a.statePath(), Logf: log.Printf,
		After: func(ctx context.Context) {
			runRatings(ctx)
			if n, err := clips.Run(ctx, false); err != nil {
				log.Printf("daemon: clips: %v (retry at next check)", err)
			} else if n > 0 {
				log.Printf("daemon: clips: %d sent", n)
			}
		}}
	scan := func(why string) {
		sctx, scancel := context.WithTimeout(ctx, 5*time.Minute)
		defer scancel()
		if err := d.Scan(sctx); err != nil && ctx.Err() == nil {
			log.Printf("daemon: scan (%s): %v", why, err)
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
			why = "poll"
		}
		// Debounce: more triggers in the next seconds join this scan.
		t := time.NewTimer(debounce)
	wait:
		for {
			select {
			case <-ctx.Done():
				t.Stop()
				log.Printf("daemon: stopped")
				return nil
			case w := <-trig:
				why += ", " + w
			case <-t.C:
				break wait
			}
		}
		scan(why)
	}
}

// logDialog records the window manager state for 20 s after a star tap, to
// learn how the "Rating Error" dialog can be closed or replaced. Read-only.
func logDialog(ctx context.Context) {
	last := ""
	for i := 0; i < 40 && ctx.Err() == nil; i++ {
		n := lipcGet(ctx, "com.lab126.winmgr", "activeDialogCount")
		title := lipcGet(ctx, "com.lab126.winmgr", "getActiveAppTitle")
		cur := n + " | " + title
		if cur != last {
			log.Printf("research: dialogs %s", cur)
			last = cur
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func lipcGet(ctx context.Context, svc, prop string) string {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	b, err := exec.CommandContext(c, "lipc-get-prop", svc, prop).CombinedOutput()
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
			time.Sleep(time.Second) // let the Kindle finish its write
			fn(ctx)
		}
	}()
	logged := false
	for ctx.Err() == nil {
		done, err := events.WatchDir(ctx, dir, func(name string, mask uint32) {
			if strings.HasPrefix(name, "fmcache.db") && mask&(events.InCloseWrite|events.InModify) != 0 {
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
	st, err := daemon.LoadState(a.statePath())
	if err != nil {
		return err
	}
	run := "Background sync: OFF"
	if pid := a.running(); pid != 0 {
		run = fmt.Sprintf("Background sync: ON (pid %d)", pid)
	}
	last := "Last check: never"
	if !st.LastScan.IsZero() {
		last = "Last check: " + st.LastScan.Local().Format("Jan 2 15:04")
	}
	res := st.LastResult
	if res == "" {
		res = "-"
	}
	signed := "Signed in: yes"
	if _, err := a.store.Load(); errors.Is(err, os.ErrNotExist) || err != nil {
		signed = "Signed in: NO"
	}
	a.screen.Show(run, signed, last, "Last result:", trim(res, 46), fmt.Sprintf("Waiting to retry: %d", len(st.Pending)))
	return nil
}
