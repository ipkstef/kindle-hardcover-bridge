package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/readers"
)

// LIPC publishers to listen to. Names from kindlemodding.org; which events
// they send is UNVERIFIED, which is why we log everything.
var lipcPublishers = []string{
	"com.lab126.powerd",
	"com.lab126.appmgrd",
	"com.lab126.wifid",
	"com.lab126.cmd",
	"com.lab126.readingstreams",
	"com.lab126.booklet.reader",
}

type logger struct {
	mu sync.Mutex
	f  *os.File
}

func (l *logger) printf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := time.Now().UTC().Format("15:04:05.000") + " " + fmt.Sprintf(format, a...) + "\n"
	l.f.WriteString(line)
	l.f.Sync()
}

func eventsProbe(args []string) int {
	fs := flag.NewFlagSet("events", flag.ExitOnError)
	out := fs.String("out", "/mnt/us/hcprobe-events.txt", "log file")
	dbPath := fs.String("db", "/var/local/cc.db", "cc.db path")
	secs := fs.Int("secs", 900, "run time in seconds")
	_ = fs.Parse(args)

	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer f.Close()
	l := &logger{f: f}
	l.printf("hcprobe events v0.1, %d s", *secs)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*secs)*time.Second)
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sig; l.printf("stop signal"); cancel() }()

	var wg sync.WaitGroup
	for _, pub := range lipcPublishers {
		wg.Add(1)
		go func(pub string) { defer wg.Done(); watchLIPC(ctx, l, pub) }(pub)
	}
	wg.Add(1)
	go func() { defer wg.Done(); watchDB(ctx, l, *dbPath) }()
	wg.Wait()
	l.printf("end")
	return 0
}

// watchLIPC runs lipc-wait-event in monitor mode for all events of one
// publisher and logs each line.
func watchLIPC(ctx context.Context, l *logger, pub string) {
	cmd := exec.CommandContext(ctx, "lipc-wait-event", "-m", pub, "*")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		l.printf("lipc %s: %v", pub, err)
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		l.printf("lipc %s: start: %v", pub, err)
		return
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		l.printf("lipc %s: %s", pub, sc.Text())
	}
	err = cmd.Wait()
	if ctx.Err() == nil {
		l.printf("lipc %s: exited: %v", pub, err)
	}
}

// watchDB logs inotify events for cc.db* in its directory. After a burst of
// events it logs the current book's percent.
func watchDB(ctx context.Context, l *logger, dbPath string) {
	fd, err := syscall.InotifyInit()
	if err != nil {
		l.printf("inotify: init: %v", err)
		return
	}
	defer syscall.Close(fd)
	dir, base := filepath.Dir(dbPath), filepath.Base(dbPath)
	mask := uint32(syscall.IN_MODIFY | syscall.IN_CLOSE_WRITE | syscall.IN_CREATE |
		syscall.IN_DELETE | syscall.IN_MOVED_TO | syscall.IN_MOVED_FROM)
	if _, err := syscall.InotifyAddWatch(fd, dir, mask); err != nil {
		l.printf("inotify: watch %s: %v", dir, err)
		return
	}
	l.printf("inotify: watching %s/%s*", dir, base)

	// Close the fd on cancel so Read returns.
	go func() { <-ctx.Done(); syscall.Close(fd) }()

	db := &readers.Database{Path: dbPath}
	lastPct := -1.0
	report := func() {
		b, err := db.CurrentBook(context.Background())
		if err != nil {
			l.printf("db: %v", err)
			return
		}
		key := b.Key
		if len(key) > 8 {
			key = key[:8]
		}
		mark := ""
		if b.Percent != lastPct {
			mark = "  <-- percent changed"
			lastPct = b.Percent
		}
		l.printf("db: current %s percent %.4f lastAccess %d%s", key, b.Percent, b.LastAccess, mark)
	}
	report()

	buf := make([]byte, 64*1024)
	var timer *time.Timer
	var mu sync.Mutex
	counts := map[string]int{}
	for {
		n, err := syscall.Read(fd, buf)
		if err != nil || n <= 0 {
			return
		}
		for off := 0; off+syscall.SizeofInotifyEvent <= n; {
			ev := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[off]))
			name := strings.TrimRight(string(buf[off+syscall.SizeofInotifyEvent:off+syscall.SizeofInotifyEvent+int(ev.Len)]), "\x00")
			off += syscall.SizeofInotifyEvent + int(ev.Len)
			if !strings.HasPrefix(name, base) {
				continue
			}
			mu.Lock()
			counts[name+" "+maskName(ev.Mask)]++
			mu.Unlock()
		}
		// Log one summary line per burst (events quiet for 1 s).
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(time.Second, func() {
			mu.Lock()
			parts := make([]string, 0, len(counts))
			for k, v := range counts {
				parts = append(parts, fmt.Sprintf("%s x%d", k, v))
			}
			counts = map[string]int{}
			mu.Unlock()
			if len(parts) == 0 {
				return // burst had only other files in the directory
			}
			l.printf("inotify: %s", strings.Join(parts, ", "))
			report()
		})
	}
}

func maskName(m uint32) string {
	names := []struct {
		bit  uint32
		name string
	}{
		{syscall.IN_MODIFY, "MODIFY"}, {syscall.IN_CLOSE_WRITE, "CLOSE_WRITE"},
		{syscall.IN_CREATE, "CREATE"}, {syscall.IN_DELETE, "DELETE"},
		{syscall.IN_MOVED_TO, "MOVED_TO"}, {syscall.IN_MOVED_FROM, "MOVED_FROM"},
	}
	var out []string
	for _, n := range names {
		if m&n.bit != 0 {
			out = append(out, n.name)
		}
	}
	return strings.Join(out, "|")
}
