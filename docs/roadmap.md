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
