// Package config holds paths and the token store.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/hardcover"
)

// Default paths on the Kindle.
//
// State lives in /var/local, not /mnt/us: /mnt/us is FAT (no file modes) and
// is unmounted while USB is connected.
//
// The log also lives in /var/local: a daemon must not keep writing to
// /mnt/us, which disappears in USB mode. "Save log to USB" copies it.
const (
	DefaultStateDir = "/var/local/hcbridge"
	DefaultCCDB     = "/var/local/cc.db"
	DefaultLog      = "/var/local/hcbridge/hcbridge.log"
	USBLog          = "/mnt/us/hcbridge.log"
)

// TokenStore saves the OAuth token as JSON with mode 0600.
type TokenStore struct {
	Path string
}

// NewTokenStore returns a store in dir.
func NewTokenStore(dir string) *TokenStore {
	return &TokenStore{Path: filepath.Join(dir, "token.json")}
}

// ErrNoToken means the user has not signed in.
var ErrNoToken = errors.New("not signed in")

// Load reads the token.
func (s *TokenStore) Load() (*hardcover.Token, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoToken
	}
	if err != nil {
		return nil, err
	}
	var t hardcover.Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	if t.AccessToken == "" {
		return nil, ErrNoToken
	}
	return &t, nil
}

// Save writes the token atomically.
func (s *TokenStore) Save(t *hardcover.Token) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Delete removes the token (sign out).
func (s *TokenStore) Delete() error {
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
