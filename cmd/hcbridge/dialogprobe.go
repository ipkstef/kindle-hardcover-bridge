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
// (roadmap "Later", option B + C). It only reads: LIPC properties, the
// system log and system files. (The winmgr list requests getAllWindows and
// visibleWindows were removed: they only threw Lua errors, probe 2026-09-28.)
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
	// Pillow dialogs are HTML/JS files; customDialog takes one of their names
	// (UNVERIFIED). Needed to show our own box (e.g. a half-star picker).
	section("pillow dialog files", runOut(ctx, 30*time.Second, "find", "/usr/share", "/usr/lib", "/opt",
		"-maxdepth", "6", "-path", "*pillow*", "(", "-name", "*.html", "-o", "-name", "*.js", "-o", "-name", "*.json", ")"))
	// How winmgr handles fakeKeyEvent / fakeTap (to close the error dialog).
	section("winmgr lua handlers", runOut(ctx, 20*time.Second, "grep", "-n", "-A25",
		"-e", "fakeKeyEvent", "-e", "fakeTap", "-e", "activeDialogCount", "-r", "/etc/xdg/awesome"))
	section("pillow: customDialog / pillowAlert users", runOut(ctx, 30*time.Second, "grep", "-rln",
		"-e", "customDialog", "-e", "pillowAlert", "/usr/share", "/usr/lib", "/opt"))
	dump := func(why string) {
		time.Sleep(300 * time.Millisecond) // let syslog catch up
		section(why+": dialog lines in /var/log/messages", grepLines(tailFile("/var/log/messages", 400),
			"kdialog", "kindleframefactory", "ratingcontroller", "goodreads"))
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
	dump("end")
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
