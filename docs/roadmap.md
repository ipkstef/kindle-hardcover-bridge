# Roadmap (user goals, 2026-09-28)

Goal: **replace Goodreads on the Kindle**. Everything else stays native.

## Decisions from the user
- **Forward only.** Never lower progress on Hardcover (done in `hcbridge sync`).
- **No manual "add book" step** in the end product.
- **No KUAL in the end product** if possible. OK if unavoidable.

## 1. Sync triggers (instead of a 30 s poll)
Candidates, to test with probe item 5 ("Watch events"):
- **inotify on `/var/local/cc.db`.** Kernel feature (since 2.6.13, so on every
  Kindle). Fires when the reader writes `cc.db` (go Home, open, sleep). No
  polling. Works in Go without cgo. Local test: OK. Device: **UNVERIFIED**.
- **LIPC events** (`lipc-wait-event -m <publisher> '*'`):
  `powerd` (screensaver, suspend, wake), `wifid` (Wi-Fi up → send queued
  update), `appmgrd` (reader ↔ Home). Names and events **UNVERIFIED**.
- Plan: inotify = main trigger; Wi-Fi-up event = retry trigger; a slow
  safety poll (e.g. 10 min, only while awake) as a fallback.

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
  `text/x-shellscript`), so this jailbreak runs **`.sh` scriptlets from the
  library**. Idea: one file `documents/Hardcover.sh`:
  tap it → install/start daemon, show sign-in code if needed.
- Autostart after reboot: an upstart job in `/etc/upstart/` (needs rootfs
  write; likely lost on firmware update → tap `Hardcover.sh` again).
  **UNVERIFIED** on this device.
