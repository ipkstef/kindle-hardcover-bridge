# Architecture

## Data flow
```
        Amazon Kindle reader (stock, unmodified)
                        │
                 Kindle local state
          ┌─────────────┼─────────────┐
     Local DB       Filesystem       LIPC / DBus
     (cc.db)        / processes      (optional)
          └─────────────┼─────────────┘
                        │
               Companion daemon
          ┌─────────────┴─────────────┐
     Book identity               Position
     ASIN / ISBN / title         location, percent,
     / author                    page (if APNX)
                        │
                        ▼
              Hardcover GraphQL API
```

## Sync loop
```
every ~30s:
    state = best_available_state(backends)   # capability model
    if state == last_sent: continue
    hc_book = resolve(state.book)            # cache ASIN → Hardcover book/edition
    if not hc_book: log + skip (do not guess)
    update_hardcover_progress(hc_book, state)
    last_sent = state (persist to disk)
```

## Position model
Canonical:
```json
{ "book": "B0XXXXXXXX", "progress": 0.37, "location": 4812 }
```
Optional, only with a reliable page map:
```json
{ "page": 147 }
```

## Capability model
| Backend    | Purpose                    | Required? |
|------------|----------------------------|-----------|
| database   | persisted reading state    | primary   |
| filesystem | which book is open         | fallback  |
| lipc       | live/richer progress       | optional  |
| pagemap    | location → printed page    | optional (APNX only) |

Each backend reports what it can supply on this device. The daemon merges the
best data. A missing backend must never crash the daemon.

## Install target (user view)
1. Jailbreak Kindle.
2. Install package (likely a KUAL extension).
3. Open it → Kindle shows a code/QR → approve on phone.
4. Done. Daemon auto-starts on boot.
