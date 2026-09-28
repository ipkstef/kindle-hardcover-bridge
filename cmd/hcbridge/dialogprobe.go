package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/events"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/screen"
)

const (
	dialogProbeOut = "/mnt/us/hcbridge-dialogprobe.txt"
	probeFilesDir  = "/mnt/us/hcbridge-probe-files"
	// probeTestWait: time for the user to tap a button on each test box.
	probeTestWait = 20 * time.Second
)

// dialogProbe (#3, final) collects all that is needed to show our own
// message after a star tap (option C) and maybe a half-star picker:
//   - copies of pillow's dialog files and the window manager's Lua scripts;
//   - all pillow / winmgr LIPC events during the run (what a button tap
//     sends back);
//   - firmware, screen size, ASR and eat-tap modes;
//   - three live tests that try to open a pillow box (the user taps any
//     button). Formats are guesses (UNVERIFIED); a wrong one is ignored.
//
// Earlier probes (findings, 2026-09-28): the error box is a cvm
// ConfirmationDialog; winmgr fakeTap / fakeKeyEvent only work in ASR or
// eat-tap mode.
func (a *app) dialogProbe(ctx context.Context) error {
	f, err := os.Create(dialogProbeOut)
	if err != nil {
		return err
	}
	defer f.Close()
	var mu sync.Mutex
	w := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(f, time.Now().Format("15:04:05.000")+" "+format+"\n", args...)
		f.Sync()
	}
	section := func(title, out string) { w("== %s ==\n%s", title, strings.TrimRight(out, "\n")) }
	a.screen.Show("Dialog probe: about 2 min.",
		"Boxes may appear on the screen.",
		"Tap any button on each box.",
		"Do not use the Kindle otherwise.",
		"Wait for 'Dialog probe: done'.")
	w("dialog probe #3, version %s", version)

	// 1. Files.
	w("copied: %s", copyTree("/usr/share/webkit-1.0/pillow", "pillow", "locales"))
	w("copied: %s", copyTree("/etc/xdg/awesome", "awesome", ""))
	w("copied: %s", copyTree("/opt/var/local/mesquite/shared/javascripts", "mesquite-js", ""))

	// 2. Events (background, until the end of the probe).
	pctx, stop := context.WithCancel(ctx)
	defer stop()
	for _, svc := range []string{"com.lab126.pillow", "com.lab126.winmgr"} {
		svc := svc
		if err := events.LIPC(pctx, svc, func(l string) { w("event %s: %s", svc, l) }, w); err != nil {
			w("events %s: %v", svc, err)
		}
	}

	// 3. Device info.
	section("firmware", tailFile("/etc/prettyversion.txt", 5))
	section("eips -i (screen)", runOut(ctx, 10*time.Second, "eips", "-i"))
	for _, p := range []string{"ASRMode", "eatTapMode", "activeDialogCount", "getActiveAppTitle"} {
		w("winmgr %s = %s", p, lipcGet(ctx, "com.lab126.winmgr", p))
	}

	// 4. Live tests. Each: set the property, wait for a tap, record.
	// Only the tested, self-closing alert. The sample custom dialog of
	// probe #3 is modal, has no visible button and stayed on screen until a
	// restart (2026-09-28): never open it again.
	tests := []struct{ prop, value string }{
		{"pillowAlert", screen.AlertParams("Hardcover test", "Test box. It closes in 8 s.", 8000)},
	}
	for i, t := range tests {
		w("test %d: lipc-set-prop com.lab126.pillow %s %s", i+1, t.prop, t.value)
		w("test %d result: %s", i+1, strings.TrimSpace(runOut(ctx, 10*time.Second,
			"lipc-set-prop", "com.lab126.pillow", t.prop, t.value)))
		for s := 0; s < int(probeTestWait/time.Second) && ctx.Err() == nil; s += 2 {
			time.Sleep(2 * time.Second)
			w("test %d: dialogs %s | %s", i+1, lipcGet(ctx, "com.lab126.winmgr", "activeDialogCount"),
				lipcGet(ctx, "com.lab126.winmgr", "getActiveAppTitle"))
		}
	}

	time.Sleep(time.Second) // let syslog catch up
	section("system log: pillow, dialogs, winmgr", grepLines(tailFile("/var/log/messages", 1500),
		"pillow", "kdialog", "kindleframefactory", "winmgr", "alert", "customdialog"))
	stop()
	w("done")
	a.screen.Show("Dialog probe: done.", "Connect USB and send:", "  hcbridge-dialogprobe.txt",
		"  folder hcbridge-probe-files")
	return nil
}

// copyTree copies the regular files under src to probeFilesDir/name,
// skipping a folder named skip. Read-only on the source.
func copyTree(src, name, skip string) string {
	dst := filepath.Join(probeFilesDir, name)
	n, size, errs := 0, int64(0), 0
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			errs++
			return nil
		}
		if d.IsDir() {
			if skip != "" && d.Name() == skip {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || size > 20<<20 { // cap: 20 MB in all
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			errs++
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if os.MkdirAll(filepath.Dir(out), 0o755) != nil || os.WriteFile(out, b, 0o644) != nil {
			errs++
			return nil
		}
		n++
		size += int64(len(b))
		return nil
	})
	if err != nil {
		return fmt.Sprintf("%s: %v", src, err)
	}
	return fmt.Sprintf("%s → %s: %d files, %d KB, %d errors", src, dst, n, size>>10, errs)
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
