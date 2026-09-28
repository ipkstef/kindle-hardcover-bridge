# CLAUDE.md — Kindle → Hardcover Sync

## Project goal
A small background daemon on a **jailbroken Kindle** that reads the user's reading
state from the **stock Amazon Kindle reader** and sends it to **Hardcover.app**.

- The user reads normally in Amazon's default reader.
- The daemon observes: current book, position (location), percent, page (if available).
- The daemon updates the user's Hardcover "currently reading" progress.

## Hard constraints (do not break)
- **No KOReader.** Do not replace or modify the Amazon reader. We only observe it.
- **Lowest common denominator.** Must work on the oldest common jailbroken Kindle,
  not only on the developer's one device. Assume fair tolerances.
- **No per-model config** unless there is no other way.
- **Simple install:** jailbreak → install package → sign in with a code → done.
  No SSH after setup.

## Design decisions (already made)
1. **Companion daemon, not a reader.** Event-driven (LIPC + inotify, see
   `docs/roadmap.md` §1), slow poll only as a safety net. Send only on change.
   Near-real-time is not required; a sync after the user closes the book is fine.
2. **Data source priority** (see `docs/kindle-data-sources.md`):
   1. Kindle local reading DB (e.g. `cc.db`) — most likely stable across firmware.
   2. Filesystem / process checks — which book is open.
   3. LIPC (`lipc-get-prop`) — extra data where available. Never required.
3. **Position = percent, from the first usable source** (user decision
   2026-09-28, `internal/syncer/position.go`):
   1. `cc.db` `p_percentFinished` (confirmed: written on go-Home and sleep);
   2. `cc.db` `p_lastAccessedPosition` ÷ text length (empty on FW 5.17.1);
   3. sidecar `.azw3f` `lpr` ÷ text length — **fallback only**, always logged
      as a cross-check.
   Hardcover page = percent × edition pages. **APNX page maps are dropped**
   (Calibre APNX on the device is synthetic: fixed 2300 chars per page).
4. **Capability model.** Backends are optional (`database`, `sidecar`, `lipc`,
   `inotify`); a missing one must never stop the daemon.
5. **Auth = Hardcover OAuth Device Authorization Grant.** Kindle shows a code/QR,
   user approves on phone at hardcover.app/link. No token typing on the Kindle.
6. **Runtime: one static ARM binary in Go** (`CGO_ENABLED=0`), with its own TLS
   and bundled CA certs. Do not rely on system curl/Python.
   Risk: new Go needs a newer Linux kernel than old Kindles have. Pin the Go
   version after a test binary runs on a real device (see `docs/open-questions.md`).

## Proposed layout
```
cmd/daemon/          entry point, poll loop, change detection
internal/readers/    cc.db reader
internal/sidecar/    KRDS sidecar reader (.azw3f lpr/fpr)
internal/clippings/  My Clippings.txt parser
internal/metrics/    fmcache.db reader (end-of-book star rating)
internal/syncer/     sync logic: progress, finish, clips, ratings
internal/daemon/     change detection + state
internal/events/     LIPC + inotify watchers
internal/book/       book identity (ASIN, title, author, ISBN)
internal/hardcover/  OAuth device flow, token refresh, GraphQL client
internal/config/     config + token storage
cmd/hcbridge/        prototype CLI: login, sync, whoami, logout
cmd/probe/           one-shot device test (see docs/probe.md)
internal/certs/      bundled CA roots
packaging/           KUAL extension / install scripts
scripts/             build scripts
docs/                context docs (read these)
```

## Where we left off
Research done for FW 5.17.1: percent is in `cc.db`, written on go-Home only
(not page turn, not sleep). See `docs/findings-bellatrix-5.17.1.md`.

Option B prototype built (`cmd/hcbridge`, KUAL menu "Hardcover", see
`docs/prototype.md`): device-code sign-in + one manual sync. It only syncs a book
that is already in the user's Hardcover "Currently Reading" list and matches by
title + author (never guesses). **Works on the user's Kindle** (sign-in +
progress update confirmed). Sync is **forward only** (user decision).

User goals and plan: `docs/roadmap.md` (event triggers, auto-add books,
restart/finish, install without KUAL). Events test done: appmgrd
`appPaused` reader + inotify on cc.db + Wi-Fi events all work (roadmap §1).
Book matching is now a waterfall over all fields (ASIN → ISBN → library →
search → skip), roadmap §2; **works on device** (ISBN and library steps).
Auto-add **works on device** (Red Rising: added via ISBN, page set).
Daemon core is **work in progress** (`hcbridge daemon`, `internal/daemon`,
`internal/events`, `internal/syncer`): built and unit-tested, not run on the
device. The user wants to **discuss system design before it is locked in**.
User: syncing when sleeping **inside** a book matters (no go-Home). Sleep
research done: sleep writes cc.db percent + `.azw3f` lpr/fpr; Wi-Fi up ~70 s
after sleep, back ~1.5 s after wake (findings, roadmap §1b). Daemon now also
triggers on `goingToScreenSaver`. Sidecar reader built (KRDS parser,
`internal/sidecar`) as the 3rd position source; APNX dropped.
Hardcover features researched (`docs/hardcover-features.md`). User decisions
in `docs/roadmap.md` §2b. Built: finish detection (>99 % / read state 2),
highlights+notes → private journal (`internal/clippings`, `ClipSync`).
Daemon ran on device (finish + clips OK; double-finish bug fixed). Rating
found in `/mnt/us/system/fmcache/fmcache.db` (`goodreads_book_ratings`,
emptied on sleep) → `RateSync` built and **works on device**. Goodreads shelf
choice: `goodreads_autoshelvings` (`kindle_asin`, `shelf_status`) mapped.
API calls cut (match cache `bookmap.json`, one shelf lookup, cached `me`,
rate limiter + 429 retry). Version 3f83ac6 **works on device**: rating,
shelf mapping, cache, no 429; Start replaces an old daemon. **User: lock down
core first**; later items (SQLite state DB, Rating Error dialog B+C) are in
`docs/roadmap.md` "Later". Re-read built (Currently Reading after a finish,
or back under 5 %). Code review 2026-09-28: HIGH fixes done (deadlock,
offline ≠ not found, no retry limit while offline, token refresh lock,
subtitle-safe title match, forward-only shelf, zone-free clip IDs, one
writer goroutine, fsync writes). b991c98 **works on device** (rating,
re-read via shelf, stop/start, offline → retry when Wi-Fi is back,
forward-only shelf). First release = default logic only: shelf choice →
status sync **off** (`syncShelves`), telemetry and SQLite DB later
(roadmap "Later"). Open: autostart/no-KUAL install.

Next after it works: the daemon (poll loop, send on change, Wi-Fi handling).
Go version still open (needs an old-kernel device); builds use Go 1.23.

## Docs
- `docs/architecture.md` — data flow and sync loop
- `docs/kindle-data-sources.md` — cc.db, LIPC services, APNX, research plan
- `docs/hardcover-api.md` — GraphQL + OAuth device flow details
- `docs/open-questions.md` — unknowns and risks
- `docs/references.md` — links and prior art
- `docs/probe.md` — device probe: what it checks, how to run it
- `docs/findings-bellatrix-5.17.1.md` — results from the user's Kindle
- `docs/prototype.md` — hcbridge prototype: setup and test steps
- `docs/roadmap.md` — user goals and plan to the end product
- `docs/hardcover-features.md` — journals, ratings, finish, re-read, scopes, limits
- `docs/decisions/embedded-db.md` — state DB choice (modernc sqlite, later)
- `docs/decisions/telemetry.md` — opt-in telemetry to S3 (draft, user decisions open)

## Working rules for Claude
- Mark anything not verified on a real device or in real docs as **UNVERIFIED**.
- Prefer small, testable steps. The user has one Kindle for testing.
- Keep answers short. Use simple technical English.
