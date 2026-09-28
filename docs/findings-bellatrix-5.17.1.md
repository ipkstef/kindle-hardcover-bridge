# Findings: MT8110 "Bellatrix", firmware 5.17.1

Source: `hcprobe` report and a copy of `cc.db` from the user's Kindle
(2026-09-28). The DB copy is not in the repo (personal data).

## Device
- Kernel 4.9.77, armv7l, 2 cores, 475 MB RAM.
- Hardware "MT8110 Bellatrix". Model code prefix `G092AP`.
  Model name **UNVERIFIED** (probably Paperwhite 5 or Kindle 11th gen).
- Tools present: `sqlite3`, `lipc-get-prop`, `eips`, `curl`, `timeout`.
  No Python.

## Go runtime
- Go 1.23 and Go 1.26 static ARM binaries (`GOARM=6`) both start.
- Both reach `api.hardcover.app` over TLS 1.3 with bundled CA roots.
- Kernel 4.9 is above every Go floor, so this device does **not** decide the
  Go version. We need a report from an old Kindle (kernel < 3.2).

## Hardcover OAuth metadata (live)
- Device endpoint: `https://api.hardcover.app/oauth2/device`
- Token endpoint: `https://api.hardcover.app/oauth2/token`
- Write scope for progress is probably `write:library` (**UNVERIFIED**).

## cc.db (`/var/local/cc.db`, table `Entries`)
Books: `p_type = 'Entry:Item'`, `p_cdeType` in (`EBOK`, `PDOC`).

| Column | Seen value | Use |
|---|---|---|
| `p_cdeKey` | Calibre UUID for all 98 books. No ASINs. | local key only |
| `p_location` | `/mnt/us/documents/<author>/<title>.azw3` | book file path |
| `p_titles_0_nominal` | title | matching |
| `j_credits` | JSON, `[{"name":{"display":...},"kind":"Author"}]` | matching |
| `p_lastAccess` | unix seconds (int) | current book = max |
| `p_percentFinished` | 0–100, int or real, NULL if never opened | **progress** |
| `p_lastAccessedPosition` | **NULL for all books** | not usable here |
| `p_readState` | NULL, 1, 2, 3 | meaning **UNVERIFIED** (2 seems = finished) |
| `p_mimeType` | mobi, mobi8 (azw3), kfx | file format |

## Conclusions
1. **Percent is available in cc.db. Location is not** (on this firmware).
   Canonical position must allow "percent only".
2. **No ASIN for sideloaded books.** Identity must come from the book file
   (EXTH ISBN / ASIN in MOBI/AZW3 header) or title + author search.
   **UNVERIFIED** if Calibre wrote ISBNs into these files.
3. Still unknown: **when** `p_percentFinished` is written (each page turn,
   on sleep, or on book close).

## Watch test 1 (2026-09-28, 01:31–01:38 UTC)
User opened a book, turned pages (+1 % on screen), stayed in the book, did
not go Home, did not sleep, then connected USB.

- On book open: sidecar `.azw3r` written (01:31:41), then `cc.db` written
  (01:31:44, `p_lastAccess` updated).
- While reading for 7 min with +1 % progress: **no write** to `cc.db` or the
  sidecar. `p_percentFinished` stayed at the old value.
- Connecting USB stopped the watch loop (no "end" line). `/mnt/us` is
  unmounted in USB mode. The daemon must not depend on `/mnt/us` while
  running (**UNVERIFIED** that the process is killed, not only its log).

Conclusion: progress is **not** written on page turn. Still to test: go Home
(book close) and sleep.

## Watch test 2 (2026-09-28, 01:43–01:46 UTC)
Steps: open book, turn pages, go Home, reopen, turn pages, sleep.

| UTC | Event | `cc.db` | Sidecar |
|---|---|---|---|
| 01:43:07–09 | open book | `p_lastAccess` updated; percent 9.668701 | `.azw3r` written |
| 01:43:41 | **go Home** | **percent → 10.723627** | `.azw3f` written (478 B) |
| 01:44:12–14 | reopen | `p_lastAccess` updated | `.azw3r` written |
| 01:44:34 | **sleep** (`screenSaver`) | written (`p_lastAccess`); percent same | `.azw3f` written |
| 01:45:35 | `readyToSuspend` | – | – |
| 01:46:26 | last log line (device suspended, loop stopped) | – | – |

Conclusions:
1. **Going Home writes `p_percentFinished`.** This is the main sync trigger.
2. **Sleep writes `cc.db` and `.azw3f`.** It is **UNVERIFIED** if sleep also
   writes a new percent (the percent did not change here; maybe no pages were
   turned after reopen).
3. Page turns do not write anything (test 1).
4. `lipc-get-prop com.lab126.powerd state` gives `active`, `screenSaver`,
   `readyToSuspend`. Background processes stop when the device suspends.
5. The percent at open (9.668701) was lower than the value before test 1
   (9.851788). The +1 % from test 1 was lost when USB was connected while
   reading. **UNVERIFIED** why.
6. `.azw3f` / `.azw3r` sidecars may hold the exact location (LPR). Format
   **UNVERIFIED**; research later.
