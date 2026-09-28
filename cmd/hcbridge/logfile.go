package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// maxLog is the size at which the log is moved to <log>.1.
const maxLog = 256 << 10

// rotLog opens, appends and closes the file on each write, so no file stays
// open (safe for a long-running daemon).
type rotLog struct {
	mu   sync.Mutex
	path string
}

func (r *rotLog) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if fi, err := os.Stat(r.path); err == nil && fi.Size() > maxLog {
		_ = os.Rename(r.path, r.path+".1")
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return len(p), nil // never fail the caller because of the log
	}
	defer f.Close()
	return f.Write(p)
}

func setupLog(path string) {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	log.SetOutput(io.MultiWriter(os.Stderr, &rotLog{path: path}))
}
