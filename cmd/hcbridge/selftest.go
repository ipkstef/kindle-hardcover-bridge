package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/clippings"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/events"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/metrics"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/screen"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/store"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/syncer"
)

const selfTestOut = "/mnt/us/hcbridge-selftest.txt"

// selfTest checks every part it can check by itself and shows OK / FAIL on
// the screen, in the log and in /mnt/us/hcbridge-selftest.txt. It writes
// nothing to Hardcover (book look-up only) and nothing to the Kindle's
// files. One tap answers most "does X work on this device" questions.
func (a *app) selfTest(ctx context.Context) error {
	a.screen.Show("Hardcover self-test: running...")
	var lines, screenLines []string
	res := func(ok bool, name, detail string) {
		tag := "OK  "
		if !ok {
			tag = "FAIL"
		}
		l := fmt.Sprintf("%s %s: %s", tag, name, detail)
		lines = append(lines, l)
		screenLines = append(screenLines, trim(l, 46))
		log.Printf("selftest: %s", l)
	}
	info := func(name, detail string) {
		l := fmt.Sprintf("     %s: %s", name, detail)
		lines = append(lines, l)
		log.Printf("selftest: %s", l)
	}
	info("version", version)

	// Daemon.
	if pid := a.running(); pid != 0 {
		res(true, "background sync", fmt.Sprintf("running (pid %d)", pid))
	} else {
		res(false, "background sync", "not running")
	}

	// Kindle tools and events.
	for _, t := range []string{"lipc-wait-event", "lipc-get-prop", "lipc-set-prop", "eips"} {
		_, err := exec.LookPath(t)
		res(err == nil, t, map[bool]string{true: "found", false: "missing"}[err == nil])
	}
	wctx, cancel := context.WithCancel(ctx)
	_, err := events.WatchDir(wctx, filepath.Dir(a.db.Path), func(string, uint32) {})
	cancel()
	res(err == nil, "inotify", errText(err, "ok"))

	// Space.
	var fs syscall.Statfs_t
	if syscall.Statfs(a.stateDir, &fs) == nil {
		free := int64(fs.Bavail) * int64(fs.Bsize) >> 20
		res(free >= 5, "free space", fmt.Sprintf("%d MB in %s", free, a.stateDir))
	}

	// State database.
	if db := a.stateDB(); db != nil {
		res(true, "state db", fmt.Sprintf("%d books, %d matches, %d clips sent, %d waiting",
			db.Count(store.TProgress), db.Count(store.TBookMap), db.Count(store.TClips), db.Count(store.TPending)))
	} else {
		res(false, "state db", "not open (JSON files used)")
	}

	// Kindle data.
	all, err := a.db.AllProgress(ctx)
	res(err == nil, "cc.db", errText(err, fmt.Sprintf("%d books", len(all))))
	var cur *book.Local
	if err == nil {
		cur, err = a.db.CurrentBook(ctx)
		if err == nil {
			res(true, "latest book", fmt.Sprintf("%q %.2f%%", cur.Title, cur.Percent))
			if pct, saved, ok := syncer.SidecarPercent(cur); ok {
				info("sidecar", fmt.Sprintf("%.2f%% saved %s", pct, saved.UTC().Format("01-02 15:04")))
			} else {
				info("sidecar", "none (KFX or no file)")
			}
		}
	}
	if _, err := os.Stat(metrics.DefaultPath); err == nil {
		_, err := metrics.Ratings(ctx, metrics.DefaultPath, time.Now().UnixMilli())
		res(err == nil, "ratings file", errText(err, "readable"))
	} else {
		info("ratings file", "not there yet")
	}
	if f, err := os.Open(clippings.DefaultPath); err == nil {
		clips, err := clippings.Parse(f)
		f.Close()
		res(err == nil, "My Clippings", errText(err, fmt.Sprintf("%d entries", len(clips))))
	} else {
		info("My Clippings", "none yet")
	}

	// Hardcover (read-only).
	if me, err := a.client().Me(ctx); err != nil {
		res(false, "Hardcover", err.Error())
	} else {
		res(true, "Hardcover", "signed in as @"+me.Username)
		if cur != nil {
			r, ub, err := a.syncer().Identify(ctx, cur)
			switch {
			case err != nil:
				res(false, "book match", err.Error())
			case ub == nil:
				res(true, "book match", fmt.Sprintf("%q (not on shelves)", r.Title))
			default:
				hp := "-"
				if rd := ub.CurrentRead(); rd != nil && rd.ProgressPages != nil {
					hp = fmt.Sprint(*rd.ProgressPages)
				}
				pages, _ := ub.Pages()
				res(true, "book match", fmt.Sprintf("%q %s, HC page %s, Kindle page %d/%d", r.Title,
					syncer.StatusName(ub.StatusID), hp, book.PercentToPage(cur.Percent, pages), pages))
			}
		}
	}

	// Alert box (closes by itself).
	err = screen.Alert("Hardcover self-test", "This box closes by itself in 4 s.", 4000)
	res(err == nil, "alert box", errText(err, "shown"))

	out := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(selfTestOut, []byte(out), 0o644); err != nil {
		log.Printf("selftest: %v", err)
	}
	time.Sleep(5 * time.Second) // let the alert close before drawing the result
	a.screen.Show(append([]string{"Hardcover self-test (also in", "hcbridge-selftest.txt):"}, screenLines...)...)
	return nil
}

func errText(err error, ok string) string {
	if err != nil {
		return err.Error()
	}
	return ok
}
