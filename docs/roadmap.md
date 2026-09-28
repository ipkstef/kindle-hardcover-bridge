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

## 2. Find the book on Hardcover (waterfall) — built, not yet run on device
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

Then: book on "Currently Reading" → update progress. Other shelf / not on
shelves → **auto-add** (next step, after `identify` is checked on device).
KFX: no file metadata yet (cc.db fields only).

## 3. Restart a book
The Kindle has no "restart" concept; the user just goes to the start.
Proposed rule (**to confirm with a test**):
- Hardcover read is finished (status Read / `finished_at` set), and the Kindle
  percent is small (< 5 %) and then grows → start a **new read**
  (`insert_user_book_read`), set status back to Currently Reading.
- Also check what Kindle "Mark as unread" does to `p_readState` /
  `p_percentFinished`.

## 4. Finish a book
- Kindle percent ≥ ~98 % or `p_readState` = 2 → mark read finished on
  Hardcover (status 3, `finished_at` = today). Threshold **UNVERIFIED**.

## 5. Install without KUAL
- The Kindle library already lists `KUAL.sh` (type PDOC, mime
  `text/x-shellscript`), and tapping it starts
  `com.notmarek.shell_integration.launcher` (seen in the events test). So this
  jailbreak runs **`.sh` scriptlets from the library**. Idea: one file `documents/Hardcover.sh`:
  tap it → install/start daemon, show sign-in code if needed.
- Autostart after reboot: an upstart job in `/etc/upstart/` (needs rootfs
  write; likely lost on firmware update → tap `Hardcover.sh` again).
  **UNVERIFIED** on this device.
