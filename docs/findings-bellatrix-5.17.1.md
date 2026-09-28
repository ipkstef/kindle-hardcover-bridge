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

## Sleep research (probe item 7, 2026-09-28, 04:11–04:20 UTC)
Book: an AZW3 (Calibre) with an `.apnx` page map in its `.sdr` folder.
Steps: open book, read, short sleep, wake, read, long sleep (~5 min), wake,
go Home. Raw data not in the repo (the LIPC dump holds account data).

**Sleeping inside the book writes the position** (both sleeps):
| UTC | Event | cc.db percent | `.azw3f` lpr/fpr |
|---|---|---|---|
| 04:12:02 | open book | 2.457042 | 22929 |
| 04:12:25.6 | `goingToScreenSaver` | → **5.395857** (04:12:26) | → **50343** (04:12:26) |
| 04:13:36.9 | `goingToScreenSaver` | → **7.290831** (04:13:37) | → **68089** (04:13:37) |
| 04:19:08 | go Home | (no change, no pages turned) | rewritten |

- Writes happen < 1 s after `goingToScreenSaver`: `.azw3f`, `.azw3r`,
  `cc.db`, `/var/local/java/prefs/<hash>.reader.pref`.
- Test 2 (another book) saw no percent change on sleep. Cause **UNVERIFIED**;
  this test is clearer (hash-checked files, two sleeps, both wrote).

**`.azw3f` sidecar** (303 bytes, binary key/value store, rewritten via
`.tmp` + rename): holds ASCII keys `timer.model`, `timer.average.calculator`,
`fpr` (furthest position read), `lpr` (last position read),
`book.info.store`, `page.history.store`, `whisperstore.migration.status`.
`fpr`/`lpr` are decimal strings = text position.
Check: lpr / (percent/100) = 933195, 932994, 933899 → constant (≈ book text
length). So percent ≈ lpr / text length. Exact format **UNVERIFIED** (only
MOBI/AZW3 seen; KFX uses `.yjr`/`.yjf`).

**Page map:** the `.sdr` has an `.apnx` (1882 bytes): 407 pages, positions
0, 2300, 4600 … 933800 → Calibre "fast" mode (fixed 2300 chars per page), not
printed pages. Same units as `lpr` (lpr 68089 → page 30). Not used.

**KRDS format** (decoded, `internal/sidecar`): signature
`00 00 00 00 00 1a b1 26`; typed values (0 bool, 1 int32, 2 int64, 3 UTF =
null-flag + uint16 length + bytes, 4 double, 5 short, 6 float, 7 byte, 9 char);
`fe` + name = object begin, `ff` = end. `lpr` object = byte 2, UTF position,
int64 save time in ms (equals cc.db `p_lastAccess`).

**Power and Wi-Fi during sleep:**
- `goingToScreenSaver` → ~70 s later `readyToSuspend` countdown → Wi-Fi
  `cmDisconnected` (04:14:47) → `suspending "mem"`.
- While asleep the device wakes briefly (`wakeupFromSuspend`) and suspends
  again about every 95 s. Wi-Fi stays off.
- Wake: `resuming` → `outOfScreenSaver` → Wi-Fi `cmConnected` +
  `connectionAvailable "wifi"` **~1.5 s later**.
- So: after sleep there is a ~60 s window with Wi-Fi to send; if missed,
  `connectionAvailable` on wake is a reliable retry trigger.

**LIPC:** no live position property found. Related names (not readable):
`com.lab126.KPPAnnotationController saveReadingProgress`,
`com.lab126.CVMAnnotationProxy sendReadingProgress`,
`com.lab126.whisperstore ingest_lpr_sidecar`,
`com.lab126.yjr.annotations gotoBookPosition`.

## Rating research (probe item 8, 2026-09-28, 05:26–05:36 UTC)
User opened the end of a sideloaded book ("Before you go…" dialog), tapped
4 stars. The Kindle showed "Rating Error: An error occurred while posting your
rating" (sideloaded book, no Amazon/Goodreads match). Raw data not in the repo
(metrics hold device serial and account IDs).

- **No LIPC event** from dialog/journal/userdata/outbox/Goodreads services.
- **The tap is stored in the metrics cache** `/mnt/us/system/fmcache/fmcache.db`,
  table `records` (id, schema_name, schema_version, app_session_id,
  reading_session_id, encoded_size, sequence_number, created_timestamp (ms),
  priority, record JSON):
  `schema_name = goodreads_book_ratings`,
  `{"action_id":"write_rating","book_asin":"<cc.db p_cdeKey>","context":"end_actions","context_id":"none","event_type":"change_rating","rating":"4"}`
  Written **before** the failed post (05:27:48).
- The cache is uploaded and **emptied** on sleep (73 KB → 20 KB at 05:32:56).
  So the daemon reads it on every change (inotify) and copies taps to its own
  state at once.
- Other record types seen: `highlight_actions`, `note_actions`,
  `ereader_open_book`, `ereader_close_book`, `eink_end_actions_class_instance`,
  … Table `reading_sessions` holds `end_reading_location`, `is_complete`
  (possible future source).
- Goodreads shelf choice in the dialog: not tested (the rating error came
  first). **UNVERIFIED** where it is stored.

## Daemon test (2026-09-28, 05:29–05:36 UTC)
- Start: all event sources OK (appmgrd, powerd, cmd, inotify).
- Red Rising (100 %, read state 2): found via ISBN, added, finished, status
  Read. Highlights sent as private quotes, notes as private notes.
- **Bug (fixed):** 3 min later Hardcover showed the book as Currently Reading
  (cause UNVERIFIED); paging back to 98.22 % (read state still 2) finished it
  a **second** time (second finished read). Fix: finish is a one-time action
  per book in the daemon; a read already finished today only gets the status.
- **Fixed:** a note on a highlight was sent without the highlighted text.

## Rating on device (2026-09-28, 05:52 UTC)
- Star tap (3) read from fmcache.db 1 s after the tap, sent to Hardcover as
  rating 3 (user_book 19022737). **Works.** The dialog wrote the record twice
  (4 s apart); last tap wins, so harmless.
- Note on a highlight ("A Parade of Horribles", location 1218) sent as one
  private note with the highlight text. **Works.**

## "Rating Error" dialog — options (open)
The error comes from the stock reader (post to Amazon/Goodreads fails for a
sideloaded book). Changing its text = patching Amazon's reader → breaks the
hard constraint "do not modify the reader". Candidate LIPC hooks (from
`lipc-probe`, all **UNVERIFIED**, write-only, format unknown):
`com.lab126.winmgr activeDialogCount` (r), `fakeKeyEvent`, `fakeTap`,
`com.lab126.pillow pillowAlert` / `customDialog` / `dismissChrome`.
Daemon now logs `activeDialogCount` + `getActiveAppTitle` for 20 s after a
tap (read-only research).

## Shelf choice + dialog state (2026-09-28, 06:03–06:06 UTC)
- Goodreads shelf choice in "Before you go…" → fmcache record
  `goodreads_autoshelvings`:
  `{"action_id":"PerformManualShelving","context":"end_actions","event_type":"ManualShelving","kindle_asin":"<p_cdeKey>","shelf_status":"currently-reading",...,"widget_invoked_by":"InvokedByBookFinish"}`.
  Values seen: `currently-reading`, `to-read`. (Key is `kindle_asin`, not
  `book_asin` → first mapping missed it; fixed.)
- `com.lab126.winmgr activeDialogCount`: 1 with "Before you go…" open,
  2 while the "Rating Error" is shown, 3 with the shelf list open. Active
  app stays `com.lab126.booklet.reader`.
- **HTTP 429** (free tier) during one rating: ~15 API calls in ~1 s.
  Fixed: match cache (`bookmap.json`), one shelf-entry lookup instead of the
  whole library, cached `me`, no re-reads, rate limiter (burst 5, 0.9/s),
  retry after 429. Rating on a known book = 2 calls.

## Test with a stale daemon (2026-09-28, 06:18–06:23 UTC)
- "Start background sync" found the old daemon running (`already running`),
  so the **old binary kept running** and the new code was not tested (shelf
  records only logged, no mapping). Fix: `daemon` now stops a running daemon
  and takes over; the log and Status show the build version.
- Shelf values seen again: `currently-reading`, `to-read`
  (`goodreads_autoshelvings`).
- USB mode: `stat My Clippings.txt: stale NFS file handle` → retried later
  (as designed).

## Version 3f83ac6 on device (2026-09-28, 06:28–06:30 UTC)
- Start replaced the old daemon (pid 23520) → version shown in the log.
- Rating 5 stars: sent in 1 s (not cached yet: ISBN lookup + shelf entry +
  rating). Shelf choice `currently-reading` → Hardcover **Currently
  Reading**, via the match cache (2 calls). **No HTTP 429.**
- Noisy metrics schemas no longer logged.
- Finish-once guard: "already finished …, not sent again".
- Open (user decision): choosing "Currently Reading" in the end-of-book
  dialog after a finish — treat as a re-read and clear the daemon's
  "finished" mark? Now progress for that book is held until < 5 %.

## Version b991c98 on device (2026-09-28, 07:02–07:05 UTC)
- Rating 2 stars on Red Rising: sent ~5 s after the tap (watcher copies the
  tap, the scan loop sends it; debounce 5 s).
- Shelf `currently-reading` after a finish (`widget_invoked_by:
  InvokedByBookFinish`, `PerformManualShelving`: a user tap, not automatic)
  → **re-read**: status Currently Reading, Hardcover's open read 6944544
  set to page 0.
- Stop, then Start: clean (no hang).
- Not tested yet: offline (Wi-Fi off → go Home → Wi-Fi on), shelf
  forward-only (`to-read` on a Read book).

## Offline test, versions b991c98 / b3ec80f (2026-09-28, 07:10–07:21 UTC)
- Wi-Fi off, go Home: `waiting (temporary failure: … network is
  unreachable), will retry` — no attempt counted, book kept (The Rithmatist,
  Darth Bane: Path of Destruction). Each DB commit re-tries (fails at once).
- Wi-Fi back: the next `db commit` scan sent it (auto-add + page) **before**
  `connectionAvailable` came (`scan (network up)` 8 s later, nothing left).
- Shelf `to-read` (`InvokedByMenu`) on a Read book: `Read → Want to Read not
  sent (forward only)`.
- The Rithmatist got read state 2 at 5.26 % (07:14) → finished as Read (rule:
  read state 2 = Kindle marked read). UNVERIFIED: the user's action (Mark as
  Read in the menu?).
- Darth Bane: Kindle title "Path of Destruction: Star Wars Legends (Darth
  Bane): A Novel of the Old Republic" → Hardcover "Darth Bane: Path of
  Destruction" via ISBN (dedicated field, title differs).
- Scan trigger lines work: `start`, `db commit`, `left book`, `network up`.

## Dialog probe #1 (version 9050712, 2026-09-28 03:49–03:50 device time)
End-of-book dialogs are **Java dialogs in cvm (KDialog)**, not pillow. The
system log (`/var/log/messages`) names each window:
- `…_A_SelectShelfDialog` — the shelf list.
- Star tap → `…_A_UpdatingDialog` → Goodreads call fails → dismissed →
  `…_A_ConfirmationDialog` = **the "Rating Error" box**.
- Cause: `GoodreadsShelfManager: GetBookByAsinRequest failed; status = 404`
  → `RatingController: Error on upating rating/shelf: UNKNOWN`. The book key
  is a Calibre UUID, not an Amazon ASIN, so Goodreads cannot find it
  (UNVERIFIED for store books; they may not show the error).
- The rating record (`goodreads_book_ratings`, `change_rating`) is written
  **before** the Goodreads call, so our sync does not depend on it.
- winmgr has `fakeKeyEvent`, `fakeTap` (write, Str) — candidates to close the
  box (format UNVERIFIED). `getAllWindows` / `visibleWindows` only throw Lua
  errors (`lab126_asr.lua`).
- `lipc-get-prop` without `-s` on `getActiveAppTitle` makes winmgr log a
  type warning on every read → now read with `-s`.

## Dialog probe #2 + log of version 4959c3f (2026-09-28 ~11:51 / 15:50)
- `/var/local`: **415 MB free of 487 MB**.
- Pillow dialogs are HTML/JS in `/usr/share/webkit-1.0/pillow/`:
  `simple_alert.html`, `sample_custom_dialog.html`, `javascripts/*` (incl.
  `widget_button_bar.js`, `client_params_handler.js`). Pillow LIPC props:
  `pillowAlert`, `customDialog` (write). Format: see probe #3 (UNVERIFIED).
- winmgr `fakeTap` ("button:x:y:press") and `fakeKeyEvent`
  ("windowId:keyType:keyCode:state") are **blocked unless ASR (screen
  reader) mode or eat-tap mode is on** (`lab126_eat_tap_mode.lua`). So
  closing the error box by a fake tap is not simple (option B).
- End-of-book box = `…_A_EndActions` (non-centered). Goodreads lookup for
  the sideloaded key fails (`LibraryBookByAsinGet … IOException`) before any
  tap; the star tap then shows `UpdatingDialog` → `ConfirmationDialog`.
- Sidecar `lpr` has a new form `224199:224198:83:<base64>`; the first field
  is the position (224199 ÷ 798994 = 28.06 % = cc.db). Parser fixed.
- `fmcache.db` read failed with `attempt to write a readonly database (776)`
  (hot journal: read during the Kindle's commit). Watcher now reacts to the
  journal delete (commit end), and reads retry 3× on busy/readonly.
- Search step matched "An Unwelcome Quest" (library had no match).

## Dialog probe #3 (version b3862ce, 2026-09-28 12:17)
- Screen 1072×1448, 300 dpi (`eips -i`). ASRMode 0.
- **pillowAlert works** with a configured alert id. Our id `hcbridgeTest`
  failed (`pillow-bb-no-buttons`, JS TypeError): ids must exist in
  `simple_alert_config.js`. Generic one: **`appAlert1`** = title
  `{alertTitle}`, text `{alertText}`, one Close button. Params:
  `{"clientParams":{"alertId":"appAlert1","show":true,"autoHide":<ms>,
  "customStrings":[{"matchStr":"alertTitle","replaceStr":…},
  {"matchStr":"alertText","replaceStr":…}]}}`. Hide: `"hide":true`.
- Button replies: only alerts with `callbackProp` in the config call
  `setLipcProperty(<replySrc>, <callbackProp>, <button id>)`; `appAlert1`
  has none. A reply needs a LIPC service of our own (not possible from a
  static Go binary without liblipc — UNVERIFIED).
- `customDialog {"name":"sample_custom_dialog"}` opened Amazon's sample
  (its visible part is a font test; the real dialog div is hidden). Names
  map to `/usr/share/webkit-1.0/pillow/<name>.html` (native pillowd;
  loading a file outside that folder is UNVERIFIED). `simple_alert` as a
  customDialog name: nothing shown.
- Taps inside pillow dialogs are logged (`button-press:target=<dialog>`).

## Version 229bd86 on device (2026-09-28 17:21–17:25)
- Star tap → sent to Hardcover in **~1 s** (Dungeon Crawler Carl: tap
  17:25:29, saved 17:25:30), no debounce.
- The early box ("will be saved") was **wrong** for "A Wizard of Mars":
  search found 2 books with the same title + author and no year → not
  found, rating dropped. Now the box shows the real result (saved / not
  found / sent later), ~1–2 s after the tap.
- Dungeon Crawler Carl: Kindle read state 2 at 97.53 % → finished (rule).

## Goodreads unlinked, version 83ebc1b (2026-09-28 17:46–17:51)
- **With Goodreads unlinked on the Kindle, the end-of-book stars still work**
  and still write `goodreads_book_ratings` to fmcache.db; no Goodreads
  error box. User decision: keep Goodreads unlinked with hcbridge.
- Ratings sent in ~1 s (Mother of Learning ARC 2: 5 stars) — but ~5 s when a
  "network up" event came first (debounce). Fixed: a tap ends the wait.
- Changing stars on a book that already had stars (no X first) was not
  seen. Only `action_id: write_rating` was accepted; the change probably
  uses another action_id (UNVERIFIED). Now all rating records with a book
  are read and logged (`tap N stars … (context, action, event)`); 1–5 stars
  are sent, others ignored. Clearing with X then tapping works.

## Version 492e91c on device (2026-09-28 18:04–18:12)
- **Changing stars without X** writes `action_id: update_rating` (first
  rating: `write_rating`); both are now sent. E.g. The Naturals 2 → 3,
  Dungeon Anarchist's Cookbook 4 → 5.
- **Duplicate books:** "A Wizard of Mars" → 2 matches, book 124283 with 30
  readers chosen (`search+readers`), rating saved.
- Rating on a finished book that is not on the shelves: added, finished,
  rated in ~2–4 s.
- Not found: "The Naturals" — author stored as **"Barnes, Jennifer Lynn"**
  (last name first). Fixed: "Last, First" names are turned around.
- Miss cache works (second tap: no look-up). Clips (note, quote) sent.
- Progress: Darth Bane moved Want to Read → Currently Reading, page 17.

## Version b3b70fe on device (2026-09-28 18:21–18:25)
- The Naturals ("Barnes, Jennifer Lynn") now found via library, 5 stars
  saved in 1 s. Log counts "1 saved".
- **Sleep inside a book** (no go-Home): scan `(sleep, …)` sent Darth Bane
  6.11 % → page 25/418, then 11.06 % → page 46/418, ~5 s after sleep.
