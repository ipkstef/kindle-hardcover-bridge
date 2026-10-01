# Roadmap (user goals, 2026-09-28)

Goal: **replace Goodreads on the Kindle**. Everything else stays native.

## Decisions from the user
- **Forward only.** Never lower progress on Hardcover (done in `hcbridge sync`).
- **No manual "add book" step** in the end product.
- **No KUAL in the end product** if possible. OK if unavoidable.

## 1. Sync triggers (instead of a 30 s poll) — confirmed on device
See findings "Events test". Design:
- **Main trigger:** `appPaused "com.lab126.booklet.reader"` (user left the
  book) **or** inotify `cc.db-journal DELETE` (DB commit). Then wait ~5 s,
  read `cc.db`, send if the percent went up.
- **Retry trigger:** `connectionAvailable "wifi" "internet"` (or
  `cmConnected`) → send queued updates.
- **Safety net:** slow poll (e.g. 15 min) while awake.
- No work while in screensaver/suspend (nothing runs then anyway).
- `lipc-wait-event` is a Kindle tool, not curl/Python; exists on FW 5.x.
  Present on old firmware: **UNVERIFIED**. inotify needs only the kernel.

## 1b. Sleep inside a book (user: this matters) — solved on FW 5.17.1
Sleep research (findings): on `goingToScreenSaver` the reader writes cc.db
percent **and** the `.azw3f` sidecar (`lpr`/`fpr` text positions) within 1 s.
Wi-Fi stays up ~70 s, and `connectionAvailable` fires ~1.5 s after wake.
Daemon: add `goingToScreenSaver` as a trigger (done); send within the window;
retry on wake.

Position sources (built, user decision): cc.db percent → cc.db
`p_lastAccessedPosition` → sidecar `lpr` (fallback; logged as cross-check).
APNX page maps dropped: the device's APNX is Calibre-made with a fixed 2300
chars per page (not printed pages), so it adds nothing over percent.

## 2. Find the book on Hardcover (waterfall) — works on device (ISBN + library steps)
Code: `internal/book/identity.go` (collect IDs), `internal/match/resolve.go`
(waterfall). Rule from the user: read **every** field; do not assume one tool
(e.g. Calibre) wrote the file; go from most exact to least exact.

Identifier sources (all fields, checksum-validated ISBNs, `B0` ASINs):
- **Dedicated:** EXTH 113 / 504 (ASIN), EXTH 104 (ISBN), `cc.db p_cdeKey`
  (ASIN for store books; old store books use an ISBN-10).
- **Other text fields:** every other EXTH text record (source, description,
  rights, …) and the file name. An ID from here needs a title match.
- Titles: `cc.db` title, EXTH 503, MOBI full name. Authors: `cc.db` credits,
  EXTH 100. Year: `cc.db` publication date, EXTH 106. Also publisher, language.

Waterfall (first confident hit wins):
1. ASIN → `editions.asin` (field **UNVERIFIED**; errors are logged, next step).
2. ISBN-13 → `editions.isbn_13`, then ISBN-10 → `editions.isbn_10`.
   An ID hit must point to exactly one book.
3. User's own library (all shelves) → exact normalized title + author.
4. Catalog search (title + first author) → one exact title + author hit, or
   one hit with the same year.
5. Else skip and log. Never guess.

Then (`hcbridge sync`, built, **UNVERIFIED** on device):
- not on shelves → **auto-add** to Currently Reading (`insert_user_book`, the
  ID's edition or the default ebook/physical edition, account privacy);
- Want to Read → move to Currently Reading (`update_user_book`);
- Currently Reading → progress (forward only);
- Read / DNF → not sent (re-read rule, §3); percent ≥ 99 → not sent (§4);
  percent 0 → not sent.
KFX: no file metadata yet (cc.db fields only).

## 2b. User decisions (2026-09-28)
- **Finish:** Kindle percent > 99 % → finished; fallback: Kindle read state 2.
  Built: finish open read (last page, finished_at today), then status Read.
- **Highlights → private quotes, notes → private notes** (journal, privacy 3),
  from `My Clippings.txt`. Built. First run records a baseline; menu
  "Import all old highlights & notes" sends older ones.
- **Rating:** reuse the stock end-of-book "Before you go…" dialog. Built: the
  star tap is read from `fmcache.db` (`goodreads_book_ratings`) and sent as
  the Hardcover rating (`internal/metrics`, `RateSync`). The Kindle still
  shows "Rating Error" for sideloaded books (we cannot change the reader).
  Goodreads shelf choice: not mapped yet. Granular edits on hardcover.app.
- **Notes on highlights:** one private note: `Highlight: "…"` + `Note: …`.
- **Privacy:** shelf entry (status, progress, rating) uses the account
  default (user controls it on Hardcover). Notes/quotes always private.
- **No reviews** from the Kindle → no `write:reviews` scope.

## 3. Restart a book — built (user decision 2026-09-28)
Re-read = (a) end-of-book shelf choice "Currently Reading" on a finished
book, or (b) a book that is Read on Hardcover goes under 5 % on the Kindle.
Action: status Currently Reading, then the open read (if Hardcover made one)
or a new read dated today. The daemon's "finished" mark is cleared.
Kindle read state 2 counts as "finished" only when it changes to 2 (the
Kindle keeps 2 after going back to the start).

## 3 (old notes)
The Kindle has no "restart" concept; the user just goes to the start.
Proposed rule (**to confirm with a test**):
- Hardcover read is finished (status Read / `finished_at` set), and the Kindle
  percent is small (< 5 %) and then grows → start a **new read**
  (`insert_user_book_read`), set status back to Currently Reading.
- Also check what Kindle "Mark as unread" does to `p_readState` /
  `p_percentFinished`.

## 4. Finish a book — built (see 2b)

## 5. Install without KUAL
- The Kindle library already lists `KUAL.sh` (type PDOC, mime
  `text/x-shellscript`), and tapping it starts
  `com.notmarek.shell_integration.launcher` (seen in the events test). So this
  jailbreak runs **`.sh` scriptlets from the library**. Idea: one file `documents/Hardcover.sh`:
  tap it → install/start daemon, show sign-in code if needed.
- Autostart after reboot: an upstart job in `/etc/upstart/` (needs rootfs
  write; likely lost on firmware update → tap `Hardcover.sh` again).
  **UNVERIFIED** on this device.

## Later (user decision: core first)
- **Local SQLite database** for daemon state (`/var/local/hcbridge/hcbridge.db`):
  match cache, progress snapshot, pending retries, sent clippings, ratings.
  Gains: atomic updates (safe on crash / battery off), indexed lookups as
  the sent-clippings list grows, one file instead of several JSON files.
  No size gain (state is a few KB). The SQLite driver is already in the
  binary (used for cc.db). Today: JSON files in `/var/local/hcbridge/`.
- **Goodreads shelf choice → Hardcover status** (user decision 2026-09-28:
  off in the first release, "default logic only"). Built and device-tested
  (forward only; "Currently Reading" on a Read book = re-read). Turn on with
  `syncShelves = true` in `cmd/hcbridge/main.go`. While off, the choice is
  only logged.
- **Telemetry to S3** (out of scope for now): design in
  `docs/decisions/telemetry.md`.
- **Rating Error dialog — C built 2026-09-28:** as soon as a star tap is
  read (before the network call), the daemon shows the Kindle's system
  alert `appAlert1` ("…stars will be saved to Hardcover. You can ignore a
  Goodreads rating error.", closes after 8 s), after waiting ≤ 3 s for the
  Goodreads error box so ours is on top. The rating is sent at once (no
  debounce). First version (alert after the send) came 6–8 s late (user).
  B (close the error box) dropped: fake taps need ASR/eat-tap mode and
  per-model coordinates. **Half-star picker:** needs our own pillow dialog
  with buttons and a reply channel; both need system changes or an
  UNVERIFIED path trick — not planned.
- (old note) **Rating Error dialog (approved: B + C):** close the stock error dialog
  after a star tap (window manager, format UNVERIFIED), then show a short
  "Saved to Hardcover" message. Dialog state research is in the daemon log.

## Book matching: 2 matching books (built 2026-09-28)
Search step, when several books match title + author: (1) the one with the
file's year; (2) else the one with a clear lead in readers (≥ 20 readers and
≥ 10× the next: Hardcover duplicates); (3) else skip, show a box once per
book ("…add the right book to a shelf on hardcover.app"), and do not look
the book up again for 1 h (miss cache, also saves API calls).

## User decisions 2026-09-28 (business rules review)
- Progress on a **Paused / DNF** book: move it to Currently Reading (a).
  Setting Paused / DNF stays on the Hardcover website (no Kindle trigger).
- Rating an unfinished book not on the shelves: add as Currently Reading (b).
- Highlights/notes do **not** add the book to the shelves (c).
- Finish: only from Kindle values (cc.db percent / read state), once per
  read (a re-read clears it) — confirmed.
- Next build: SQLite state store (`/var/local/hcbridge/hcbridge.db`, WAL,
  synchronous=NORMAL), skip API calls when the page is unchanged, offline
  mode (no API tries until Wi-Fi is back), quiet log, onboarding (auto-start
  after sign-in), first-run rules (below).
- First run: baseline, only the latest book syncs; old highlights only via
  "Import all"; the Kindle library is never bulk-pushed (optional import
  later, with a preview).

## Install without KUAL: appreg.db (to study)
`/var/local/appreg.db` (SQLite) registers apps with appmgrd: handler
(`handlerId`, `command`, `lipcId`, `extend-start`, `unloadPolicy`,
`maxGoTime` …), associations and properties. Used by
github.com/KindleModding/sh_integration and
github.com/notmarek/KOReaderIntegration (KUAL-free launchers). Source:
kindlemodding.org/kindle-hacking/appreg.html (not reachable from the build
machine; summary from search, UNVERIFIED). Use for: a launcher icon / file
handler for sign-in, status, self-test without KUAL. Autostart at boot is
a separate question (not covered by appreg).
