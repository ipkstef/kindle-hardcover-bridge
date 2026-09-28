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
1. **Companion daemon, not a reader.** Poll about every 30 s. Send only on change.
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
cmd/probe/           one-shot device test (see docs/probe.md)
internal/certs/      bundled CA roots
packaging/           KUAL extension / install scripts
scripts/             build scripts
docs/                context docs (read these)
```

## Where we left off
Research phase. Probe ran on the user's Kindle (kernel 4.9, FW 5.17.1): Go
works, HTTPS works. `cc.db` has percent, not location; books are sideloaded (no
ASIN). Percent is written to `cc.db` on go-Home (book close) and maybe on
sleep, never on page turn. See `docs/findings-bellatrix-5.17.1.md`. Go version
is still open (needs an old-kernel device). Next:
- A) Read `kindle-reading-dashboard` + NickelHardcover source; list exact `cc.db`
  fields and Hardcover mutations needed.
- B) Prototype: device-code login + one progress update to Hardcover.

## Docs
- `docs/architecture.md` — data flow and sync loop
- `docs/kindle-data-sources.md` — cc.db, LIPC services, APNX, research plan
- `docs/hardcover-api.md` — GraphQL + OAuth device flow details
- `docs/open-questions.md` — unknowns and risks
- `docs/references.md` — links and prior art
- `docs/probe.md` — device probe: what it checks, how to run it
- `docs/findings-bellatrix-5.17.1.md` — results from the user's Kindle

## Working rules for Claude
- Mark anything not verified on a real device or in real docs as **UNVERIFIED**.
- Prefer small, testable steps. The user has one Kindle for testing.
- Keep answers short. Use simple technical English.
