// Package store is the daemon's own state database: one SQLite file in
// /var/local/hcbridge (ext3, kept across restarts; /mnt/us is FAT and is
// unmounted in USB mode, /tmp is RAM). See docs/decisions/embedded-db.md.
//
// Each table maps a text key to a JSON value (one row per book, clip, ...).
// Tables are read once into memory and saved by diff: only changed rows are
// written, in one transaction. The OAuth token is not stored here (it stays
// a 0600 file).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Tables, all "(k TEXT PRIMARY KEY, v TEXT NOT NULL) WITHOUT ROWID".
const (
	TKV       = "kv"         // scalars: cursors, flags, migration marks
	TProgress = "progress"   // book key → last seen Kindle progress
	TPending  = "pending"    // book key → failed attempts
	TFinished = "finished"   // book key → day marked Read
	TBookMap  = "book_map"   // book key → Hardcover match (+ last page sent)
	TClips    = "clips_sent" // clip ID → journal ID
	TRatings  = "ratings"    // book key → star tap not yet sent
)

var tables = []string{TKV, TProgress, TPending, TFinished, TBookMap, TClips, TRatings}

// schemaVersion is PRAGMA user_version.
const schemaVersion = 1

// Store is the open database. Safe for use from several goroutines (one
// connection, writes are serialized by database/sql).
type Store struct {
	db   *sql.DB
	Path string
}

// Open opens or creates the database. Settings for low drain on flash:
// WAL with synchronous=NORMAL (one flush per checkpoint, not per write; a
// power loss can lose the last write but never damages the file), a small
// page cache, one connection kept open.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(5000)&_pragma=cache_size(-256)&_pragma=temp_store(MEMORY)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(0)
	s := &Store{db: db, Path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return s, nil
}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if v >= schemaVersion {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range tables {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS ` + t + ` (k TEXT PRIMARY KEY, v TEXT NOT NULL) WITHOUT ROWID`); err != nil {
			return fmt.Errorf("store: create %s: %w", t, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// Close checkpoints the WAL into the main file and closes the database.
func (s *Store) Close() error {
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return s.db.Close()
}

// Get reads one kv value ("" and false if missing).
func (s *Store) Get(name string) (string, bool) {
	var v string
	err := s.db.QueryRow(`SELECT v FROM kv WHERE k = ?`, name).Scan(&v)
	return v, err == nil
}

// Set writes one kv value.
func (s *Store) Set(name, value string) error {
	_, err := s.db.Exec(`INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, name, value)
	return err
}

// GetJSON / SetJSON store a value as JSON in kv.
func (s *Store) GetJSON(name string, v any) bool {
	raw, ok := s.Get(name)
	return ok && json.Unmarshal([]byte(raw), v) == nil
}

func (s *Store) SetJSON(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.Set(name, string(b))
}

// Has reports if a key exists in a table (indexed look-up).
func (s *Store) Has(table, key string) bool {
	var one int
	return s.db.QueryRow(`SELECT 1 FROM `+table+` WHERE k = ?`, key).Scan(&one) == nil
}

// Count returns the number of rows in a table.
func (s *Store) Count(table string) int {
	var n int
	_ = s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n)
	return n
}

// Op is one pending write.
type Op struct {
	q    string
	args []any
}

// Apply runs ops in one transaction.
func (s *Store) Apply(ops ...Op) error {
	if len(ops) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, o := range ops {
		if _, err := tx.ExecContext(ctx, o.q, o.args...); err != nil {
			return fmt.Errorf("store: %w", err)
		}
	}
	return tx.Commit()
}

// SetOp is Set as an Op (to join a transaction).
func SetOp(name, value string) Op {
	return Op{`INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, []any{name, value}}
}

// Table is a key → T table, loaded into memory and saved by diff.
type Table[T any] struct {
	s     *Store
	name  string
	mu    sync.Mutex
	saved map[string]string // last written JSON per key
}

// NewTable binds a table.
func NewTable[T any](s *Store, name string) *Table[T] {
	return &Table[T]{s: s, name: name}
}

// Load reads all rows. Rows that do not decode are skipped.
func (t *Table[T]) Load() (map[string]T, error) {
	rows, err := t.s.db.Query(`SELECT k, v FROM ` + t.name)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer rows.Close()
	out := map[string]T{}
	saved := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
		var x T
		if json.Unmarshal([]byte(v), &x) != nil {
			continue
		}
		out[k] = x
		saved[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.saved = saved
	t.mu.Unlock()
	return out, nil
}

// Diff returns the writes that turn the table into m (upsert changed rows,
// delete missing ones) and a commit func to call after Apply succeeded.
func (t *Table[T]) Diff(m map[string]T) ([]Op, func(), error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	next := make(map[string]string, len(m))
	var ops []Op
	for k, v := range m {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, nil, err
		}
		next[k] = string(b)
		if old, ok := t.saved[k]; !ok || old != next[k] {
			ops = append(ops, Op{`INSERT INTO ` + t.name + ` (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, []any{k, next[k]}})
		}
	}
	for k := range t.saved {
		if _, ok := next[k]; !ok {
			ops = append(ops, Op{`DELETE FROM ` + t.name + ` WHERE k = ?`, []any{k}})
		}
	}
	return ops, func() {
		t.mu.Lock()
		t.saved = next
		t.mu.Unlock()
	}, nil
}

// Save writes m by diff in one transaction.
func (t *Table[T]) Save(m map[string]T) error {
	ops, commit, err := t.Diff(m)
	if err != nil {
		return err
	}
	if err := t.s.Apply(ops...); err != nil {
		return err
	}
	commit()
	return nil
}

// Put writes one row (not through the in-memory copy; for tables that are
// not loaded as a whole, e.g. clips_sent during a large import).
func (t *Table[T]) Put(key string, v T) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = t.s.db.Exec(`INSERT INTO `+t.name+` (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, key, string(b))
	if err == nil {
		t.mu.Lock()
		if t.saved != nil {
			t.saved[key] = string(b)
		}
		t.mu.Unlock()
	}
	return err
}

// ImportJSON runs import once per name: if the kv mark "migrated.<name>" is
// missing and file exists, import(file) moves its data into the store; then
// the file is renamed to <file>.migrated and the mark is set.
func (s *Store) ImportJSON(name, file string, importFn func(b []byte) error) error {
	mark := "migrated." + name
	if _, ok := s.Get(mark); ok {
		return nil
	}
	b, err := os.ReadFile(file)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return err
	default:
		if err := importFn(b); err != nil {
			return fmt.Errorf("store: import %s: %w", filepath.Base(file), err)
		}
		_ = os.Rename(file, file+".migrated")
	}
	return s.Set(mark, time.Now().UTC().Format(time.RFC3339))
}
