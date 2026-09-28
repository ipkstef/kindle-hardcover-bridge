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
3. **Canonical position = identifier + location + percent.** Page number is optional
   and only sent when a reliable page map (APNX) exists.
4. **Capability model.** Several reader backends (`database`, `filesystem`, `lipc`,
   `pagemap`). The daemon asks "give me the best state you can find", it does not
   require one specific property.
5. **Auth = Hardcover OAuth Device Authorization Grant.** Kindle shows a code/QR,
   user approves on phone at hardcover.app/link. No token typing on the Kindle.
6. **Runtime: one static ARM binary in Go** (`CGO_ENABLED=0`), with its own TLS
   and bundled CA certs. Do not rely on system curl/Python.
   Risk: new Go needs a newer Linux kernel than old Kindles have. Pin the Go
   version after a test binary runs on a real device (see `docs/open-questions.md`).

## Proposed layout
```
cmd/daemon/          entry point, poll loop, change detection
internal/readers/    database, filesystem, lipc, pagemap backends
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
Auto-add built in `hcbridge sync`; waiting for a device test. Then: daemon.

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

## Working rules for Claude
- Mark anything not verified on a real device or in real docs as **UNVERIFIED**.
- Prefer small, testable steps. The user has one Kindle for testing.
- Keep answers short. Use simple technical English.
