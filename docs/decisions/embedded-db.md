# Decision: embedded database for daemon state

Status: **built** (2026-09-28, `internal/store`). Settings changed from the first proposal: see "As built".

## Context
Daemon state today is JSON files in `/var/local/hcbridge/` (ext3, persistent):
`state.json` (progress snapshot, pending, finished), `clips.json` (sent clip
IDs), `ratings.json` (pending taps, cursors), `bookmap.json` (match cache),
`token.json`. Each change rewrites a whole file (tmp + rename). Sizes today:
a few KB; `clips.json` grows with every highlight, forever.

Limits of the device: ARM (armv7, maybe armv6 on old models), ~475 MB RAM on
the test device (less on old ones), old kernels, `CGO_ENABLED=0`, one static
binary. Measured here (x86-64): daemon RSS ~12 MB idle and after a scan;
ARM binary 10.4 MB.

## Options
| Option | Binary cost | RAM | Fit |
|---|---|---|---|
| JSON files (now) | 0 | whole file in RAM | OK while small; no cross-file atomicity; rewrite per change |
| **modernc.org/sqlite** (pure Go) | **0** (already linked to read `cc.db`) | page cache, set to ~0.5 MB | SQL, transactions, indexes; same library already works on the device |
| bbolt (pure Go B+tree KV) | ~+0.5–1 MB (UNVERIFIED) | mmap of file | ACID KV; no queries; hand-made encoding |
| buntdb (in-memory KV + AOF) | small | whole DB in RAM | fine small; less common |
| BadgerDB / Pebble (LSM) | many MB | tens of MB, many goroutines | too heavy — reject |
| mattn/go-sqlite3 (cgo) | — | — | needs cgo + cross C toolchain — reject |

## Decision (proposed)
**modernc.org/sqlite**, one file `/var/local/hcbridge/hcbridge.db`:
- no new dependency or binary size; proven on this device (cc.db reads);
- transactions make "sent + state update" atomic (safe on crash/battery off);
- indexed `clips_sent` scales with years of highlights.

Settings: `journal_mode=DELETE` (no WAL shared memory; simple on old
kernels), `synchronous=FULL`, `busy_timeout=5000`, `cache_size=-512`
(512 KB), `SetMaxOpenConns(1)`. Schema version in `PRAGMA user_version`.
Never on `/mnt/us` (FAT, gone in USB mode).

Tables (draft): `book_map(key PK, book_id, edition_id, pages, title, method, at)`,
`progress(key PK, percent, last_access, read_state)`,
`pending(key PK, attempts)`, `finished(key PK, day)`,
`clips_sent(id PK, journal_id, at)`, `ratings_pending(key PK, stars, created_ms)`,
`kv(name PK, value)` for cursors. `token.json` stays a separate 0600 file.

Migration: on first start, import the JSON files in one transaction, then
rename them `*.migrated`.

Pinned version: `modernc.org/sqlite v1.38.2` (needs Go 1.23; newest
versions need newer Go — keep pinned until the Go/kernel floor is decided).

## As built (2026-09-28)
- File `/var/local/hcbridge/hcbridge.db` (0600). Why `/var/local`: ext3,
  kept across restarts, where Amazon keeps `cc.db`; `/mnt/us` is FAT and is
  unmounted in USB mode (the daemon keeps running), `/tmp` is RAM, rootfs is
  read-only and replaced by updates. 5 MB free-space guard stays.
- **WAL + `synchronous=NORMAL`** (not DELETE+FULL): fewest flash flushes per
  write; a power loss can lose the last write, never damages the file.
  `cache_size=-256` (256 KB), `temp_store=MEMORY`, one connection kept open,
  `wal_checkpoint(TRUNCATE)` on exit.
- Tables are `(k TEXT PRIMARY KEY, v TEXT) WITHOUT ROWID`, value = JSON of
  the row: `kv`, `progress`, `pending`, `finished`, `book_map`,
  `clips_sent`, `ratings`. Read once into memory; saved **by diff** (only
  changed rows, one transaction). `clips_sent` is read only when
  `My Clippings.txt` changed.
- `book_map` also holds the last page sent → no API call when the page is
  unchanged.
- Old JSON files are imported once and renamed `*.migrated`. If the DB
  cannot be opened, the JSON files are used (capability model).
- `token.json` stays a separate 0600 file.
