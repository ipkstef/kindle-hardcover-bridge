// Package events watches Kindle events: LIPC (lipc-wait-event) and inotify.
// Findings on FW 5.17.1: docs/findings-bellatrix-5.17.1.md, "Events test".
package events

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// LIPC runs `lipc-wait-event -m <publisher> '*'` and calls onLine for each
// event line. It restarts the tool if it exits. It returns an error at once
// if lipc-wait-event is missing; otherwise it runs until ctx is done.
func LIPC(ctx context.Context, publisher string, onLine func(string), logf func(string, ...any)) error {
	bin, err := exec.LookPath("lipc-wait-event")
	if err != nil {
		return err
	}
	go func() {
		backoff := time.Second
		for ctx.Err() == nil {
			start := time.Now()
			cmd := exec.CommandContext(ctx, bin, "-m", publisher, "*")
			out, err := cmd.StdoutPipe()
			if err == nil {
				err = cmd.Start()
			}
			if err == nil {
				sc := bufio.NewScanner(out)
				for sc.Scan() {
					onLine(sc.Text())
				}
				err = cmd.Wait()
			}
			if ctx.Err() != nil {
				return
			}
			if time.Since(start) > time.Minute {
				backoff = time.Second
			}
			logf("events: lipc %s exited (%v), restart in %s", publisher, err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < time.Minute {
				backoff *= 2
			}
		}
	}()
	return nil
}

// Inotify masks we use.
const (
	InModify     = syscall.IN_MODIFY
	InCloseWrite = syscall.IN_CLOSE_WRITE
	InCreate     = syscall.IN_CREATE
	InDelete     = syscall.IN_DELETE
	InMovedTo    = syscall.IN_MOVED_TO
)

// WatchDir watches a directory with inotify and calls onEvent for each event.
// It returns an error at once if inotify is not available. The returned
// channel is closed when the watch ends: ctx done, or the folder is gone or
// unmounted (e.g. /mnt/us in USB mode); the caller may then watch again.
func WatchDir(ctx context.Context, dir string, onEvent func(name string, mask uint32)) (<-chan struct{}, error) {
	fd, err := syscall.InotifyInit()
	if err != nil {
		return nil, err
	}
	mask := uint32(InModify | InCloseWrite | InCreate | InDelete | InMovedTo)
	if _, err := syscall.InotifyAddWatch(fd, dir, mask); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	done := make(chan struct{})
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		syscall.Close(fd)
	}()
	go func() {
		defer close(done)
		defer close(stop)
		buf := make([]byte, 64*1024)
		for {
			n, err := syscall.Read(fd, buf)
			if err != nil {
				if errors.Is(err, syscall.EINTR) {
					continue
				}
				return
			}
			if n <= 0 {
				return
			}
			for off := 0; off+syscall.SizeofInotifyEvent <= n; {
				ev := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[off]))
				end := off + syscall.SizeofInotifyEvent + int(ev.Len)
				if end > n {
					break
				}
				name := strings.TrimRight(string(buf[off+syscall.SizeofInotifyEvent:end]), "\x00")
				if ev.Mask&(syscall.IN_IGNORED|syscall.IN_UNMOUNT) != 0 {
					return // watch removed: folder deleted or unmounted
				}
				onEvent(name, ev.Mask)
				off = end
			}
		}
	}()
	return done, nil
}
