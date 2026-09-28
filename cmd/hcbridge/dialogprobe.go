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
	// Probe #2 found pillow's dialog files and that fakeTap / fakeKeyEvent
	// work only in ASR (screen reader) or eat-tap mode. Probe #3 copies the
	// files that define the message and dialog formats, to read them.
	w("copied files: %s", copyProbeFiles())
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
	a.screen.Show("Dialog probe: done.", "Connect USB and send", "  hcbridge-dialogprobe.txt",
		"  + folder hcbridge-probe-files")
	return nil
}

const probeFilesDir = "/mnt/us/hcbridge-probe-files"

// probeFiles define pillow's alert / custom dialog formats and the window
// manager's fake input rules (paths seen in probe #2).
var probeFiles = []string{
	"/usr/share/webkit-1.0/pillow/simple_alert.html",
	"/usr/share/webkit-1.0/pillow/sample_custom_dialog.html",
	"/usr/share/webkit-1.0/pillow/javascripts/simple_alert.js",
	"/usr/share/webkit-1.0/pillow/javascripts/simple_alert_config.js",
	"/usr/share/webkit-1.0/pillow/javascripts/sample_custom_dialog.js",
	"/usr/share/webkit-1.0/pillow/javascripts/client_params_handler.js",
	"/usr/share/webkit-1.0/pillow/javascripts/pillow.js",
	"/usr/share/webkit-1.0/pillow/javascripts/pillow_case.js",
	"/usr/share/webkit-1.0/pillow/javascripts/lipc_event_handler.js",
	"/usr/share/webkit-1.0/pillow/javascripts/widget_button_bar.js",
	"/usr/share/webkit-1.0/pillow/javascripts/constants.js",
	"/usr/share/webkit-1.0/pillow/strings/simple_alert_strings.js",
	"/usr/share/webkit-1.0/pillow/strings/sample_custom_dialog_strings.js",
	"/etc/xdg/awesome/lab126_eat_tap_mode.lua",
	"/etc/xdg/awesome/lab126_asr.lua",
}

// copyProbeFiles copies probeFiles (read-only on the source) to the USB
// folder and returns a short result.
func copyProbeFiles() string {
	if err := os.MkdirAll(probeFilesDir, 0o755); err != nil {
		return err.Error()
	}
	ok, errs := 0, []string{}
	for _, p := range probeFiles {
		b, err := os.ReadFile(p)
		if err == nil {
			err = os.WriteFile(probeFilesDir+"/"+strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", "_"), b, 0o644)
		}
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		ok++
	}
	return fmt.Sprintf("%d of %d to %s %v", ok, len(probeFiles), probeFilesDir, errs)
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
