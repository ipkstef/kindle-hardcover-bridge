package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	dialogProbeOut = "/mnt/us/hcbridge-dialogprobe.txt"
	dialogProbeFor = 3 * time.Minute
)

// dialogProbe records the window manager while the user taps stars in the
// end-of-book dialog, to find a way to close the "Rating Error" dialog
// (roadmap "Later", option B + C). It changes nothing in the reader. The
// only writes are the window manager's two list requests (getAllWindows,
// visibleWindows), which are expected to print to the system log
// (UNVERIFIED).
func (a *app) dialogProbe(ctx context.Context) error {
	f, err := os.Create(dialogProbeOut)
	if err != nil {
		return err
	}
	defer f.Close()
	w := func(format string, args ...any) {
		fmt.Fprintf(f, time.Now().Format("15:04:05.000")+" "+format+"\n", args...)
		f.Sync()
	}
	a.screen.Show("Dialog probe: running 3 min.",
		"Go to the end of a book now,",
		"tap stars, wait for the error,",
		"then close it. Result file:",
		"  hcbridge-dialogprobe.txt")
	w("dialog probe, version %s", version)

	section := func(title, out string) { w("== %s ==\n%s", title, strings.TrimRight(out, "\n")) }
	section("lipc-probe -l (goodreads, kpp, dialog)", grepLines(runOut(ctx, 30*time.Second, "lipc-probe", "-l"),
		"goodreads", "kpp", "dialog", "pillow", "winmgr", "endaction", "booklet"))
	dump := func(why string) {
		for _, svc := range []string{"com.lab126.winmgr", "com.lab126.pillow"} {
			section(why+": lipc-probe -v "+svc, runOut(ctx, 15*time.Second, "lipc-probe", "-v", svc))
		}
		for _, p := range []string{"getAllWindows", "visibleWindows"} {
			section(why+": set winmgr "+p, runOut(ctx, 5*time.Second, "lipc-set-prop", "com.lab126.winmgr", p, ""))
		}
		time.Sleep(300 * time.Millisecond) // let syslog catch up
		section(why+": /var/log/messages (last 60 lines)", tailFile("/var/log/messages", 60))
		section(why+": ps", runOut(ctx, 10*time.Second, "ps"))
	}
	dump("start")

	end := time.Now().Add(dialogProbeFor)
	last, lastN, dumps := "", 0, 0
	for time.Now().Before(end) && ctx.Err() == nil {
		ns := lipcGet(ctx, "com.lab126.winmgr", "activeDialogCount")
		title := lipcGet(ctx, "com.lab126.winmgr", "getActiveAppTitle")
		if cur := ns + " | " + title; cur != last {
			w("dialogs %s", cur)
			last = cur
		}
		// A second dialog on top of the end-of-book dialog: the error
		// (device log 2026-09-28). Dump at most 3 times.
		n, _ := strconv.Atoi(ns)
		if n >= 2 && n != lastN && dumps < 3 {
			dumps++
			dump(fmt.Sprintf("dialog count %d (#%d)", n, dumps))
		}
		lastN = n
		time.Sleep(300 * time.Millisecond)
	}
	w("done")
	a.screen.Show("Dialog probe: done.", "Connect USB and send", "  hcbridge-dialogprobe.txt")
	return nil
}

func runOut(ctx context.Context, d time.Duration, name string, args ...string) string {
	c, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	b, err := exec.CommandContext(c, name, args...).CombinedOutput()
	s := string(b)
	if err != nil {
		s += "\n(error: " + err.Error() + ")"
	}
	return s
}

func grepLines(s string, words ...string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		low := strings.ToLower(l)
		for _, w := range words {
			if strings.Contains(low, w) {
				out = append(out, l)
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

func tailFile(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(error: " + err.Error() + ")"
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
