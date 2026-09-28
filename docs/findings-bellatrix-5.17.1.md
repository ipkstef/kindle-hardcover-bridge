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
2. **Sleep writes `cc.db` and `.azw3f`, but not the new percent.** The user
   confirmed pages were turned after reopen; the percent stayed the same.
3. Page turns do not write anything (test 1).
4. `lipc-get-prop com.lab126.powerd state` gives `active`, `screenSaver`,
   `readyToSuspend`. Background processes stop when the device suspends.
5. The percent at open (9.668701) was lower than the value before test 1
   (9.851788). The +1 % from test 1 was lost when USB was connected while
   reading. **UNVERIFIED** why.
6. `.azw3f` / `.azw3r` sidecars may hold the exact location (LPR). Format
   **UNVERIFIED**; research later.

## Prototype test (hcbridge, 2026-09-28, 02:35–02:42 UTC)
- **Device-code sign-in works.** Approved on phone in ~40 s.
  Granted scope: `read:me:content read:library write:library read:catalog`
  (we asked for `read:me`; the server gave `read:me:content`). Enough for
  `me`, `user_books` and `update_user_book_read`.
- **Sync works.** `update_user_book_read` set `progress_pages` on the open read
  (edition with 688 pages).
- Book not in "Currently Reading" → skipped (as designed). After the user added
  it, the title + author match found it.
- Percent sequence from `cc.db`: 10.72 → 10.60 → 14.58 → 10.60. Hardcover
  followed it, **including backwards** (page 100 → 72). Cause of the drop is
  **UNVERIFIED** (user paged back, or the reader wrote an older position).

## Events test (probe item 5, 2026-09-28, 03:04–03:05 UTC)
All three event sources work on this device.

**inotify on `/var/local/cc.db*`:** works. Each reader transaction shows as
`cc.db-journal CREATE … DELETE` (rollback journal). There are also many
`cc.db CLOSE_WRITE` events without a change (noise). Good trigger:
`cc.db-journal DELETE` (= commit), then read the DB.

**LIPC `com.lab126.appmgrd`** (`lipc-wait-event -m com.lab126.appmgrd '*'`):
- Open book: `appActivating 1 "com.lab126.booklet.reader"`, and
  `historyChange … "file:///mnt/us/documents/<path>.azw3"` (**gives the open
  book's file path**).
- Go Home: `appPaused "com.lab126.booklet.reader"` (03:04:44), then the
  `cc.db` commit 3 s later (03:04:47).
- Library `.sh` launch: `com.notmarek.shell_integration.launcher` → the
  jailbreak has notmarek's shell integration (library scriptlets work).

**LIPC `com.lab126.powerd`:** `goingToScreenSaver`, `outOfScreenSaver`,
`exitingScreenSaver`, `t1TimerReset`, `battLevelChanged`, `charging`,
`usbConfigured`.

**LIPC `com.lab126.wifid` / `com.lab126.cmd`:** Wi-Fi off →
`cmDisconnected`, `connectionNotAvailable`. Wi-Fi on → `cmConnected`,
`cmStateChange "CONNECTED"`, `InternetConnected 1`,
`connectionAvailable "wifi" "internet"`. After wake, `cmConnected` again.

`com.lab126.readingstreams` and `com.lab126.booklet.reader`: no events seen.

Percent did not change in this test (10.6016 throughout).

## Book ID scan v0.1 (probe item 6) — invalid
Reported 0 ISBN / 0 ASIN for all 98 MOBI/AZW3 files. Cause: parser bug (EXTH
flag read at a wrong offset). Fixed in v0.2; to re-run.

## Book ID scan v0.3 + identify (2026-09-28, 03:24–03:27 UTC)
Scan (98 MOBI/AZW3 + 12 KFX, no titles in report):
- EXTH present in all 98 MOBI/AZW3. Text types seen (type:books):
  100:98 101:94 103:79 104:75 105:60 106:98 108:98 109:38 112:98 113:98
  129:95 501:98 503:98 504:1 524:98 525:16 527:2 528:98 535:45.
- **ISBN in EXTH 104: 75 books.** No `B0` ASIN anywhere (113 holds a tool UUID,
  504 on one book, not an ASIN).
- 23 MOBI/AZW3 books have no ISBN → library/search steps.
- 12 KFX files (PDOC, 8-hex keys): not parsed; only cc.db fields.
- **False positives:** EXTH 106 (publish date, e.g. `2023-05-23…`) gave fake
  ISBNs via the ISBN-10 checksum (8 books). Fixed: only free-text fields
  (103, 105, 109, 112) and the file name are scanned besides the dedicated ones,
  and there an ISBN-10 needs an "ISBN" label.

Identify on device (live API):
- "The Strength of the Few": no ISBN → **library** step → book 824777
  (Currently Reading). Correct.
- "Games Wizards Play": ISBN 9780544633711 (EXTH 104) → `editions.isbn_13` →
  book 651967. Correct. **`editions.isbn_13` filter works.**
- `editions.asin` still untested (no ASIN on this device).
