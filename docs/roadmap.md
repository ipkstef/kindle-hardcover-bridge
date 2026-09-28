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

## 2. Auto-add books (no manual step)
Order, stop at the first confident hit:
1. ASIN in EXTH (113/504) → Hardcover edition by ASIN.
2. ISBN in EXTH (104) → Hardcover edition by ISBN.
3. Title + author search → accept only one exact normalized match.
4. Else: skip, show "not found" once. Never guess.
Then `insert_user_book` with status 2 (Currently Reading) the first time the
book gets progress. Probe item 6 ("Scan book IDs") tells how many books have
ASIN/ISBN. KFX files: not parsed yet.

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
