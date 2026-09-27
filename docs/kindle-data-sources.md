# Kindle data sources

## 1. Local reading database (primary)
- The project `kindle-reading-dashboard` (GitHub) reads the Kindle's own files over
  SSH/USB and reports book, author, % complete, current page, reading status,
  reading time, sessions, and raw DB fields.
- It extracts fields from **`cc.db`** and decoded JSON (reading timelines, device
  sessions).
- It converts position → printed page using the book's **APNX** page map.
- **UNVERIFIED:** exact table/column names, and whether `cc.db` holds position on
  older firmware. This is the #1 research item.

## 2. Filesystem / processes (fallback)
- Detect which book is open (open file handles, recently modified sidecar files).
- **UNVERIFIED:** exact paths per firmware.

## 3. LIPC (optional)
Source: https://kindlemodding.org/kindle-apps-and-services/index.html
- Query with `lipc-get-prop <service> <property>`.
- Relevant services listed there:
  - `com.lab126.reader.readingtimer`
    - `getReadingProgressTypes`, `readingProgressType`
    - types: `location`, `page`, `training`, `timeLeftInBook`,
      `timeLeftInSection`, `timeLeftInGoal`, `hidden`
  - `com.lab126.readingstreams`
  - `com.lab126.booklet.reader.sa`
  - `com.lab126.booklet`
  - `com.lab126.ccat`
- The service list is marked WIP/incomplete. Some properties are hashes not yet
  reverse engineered. Do not depend on LIPC.

## Page numbers
- Not all Kindle books have page numbers. Location/percent is the base unit.
- Send page only when an APNX map exists and gives a clean result.

## Research plan (on the real Kindle)
1. Record model + firmware version.
2. Jailbreak (done/needed?).
3. SSH in. Snapshot `cc.db` and other candidate files.
4. Open a book → snapshot. Turn pages → snapshot. Close → snapshot. Reopen → snapshot.
5. Diff snapshots. Find the authoritative position field and when it is written.
6. Find how to map the book to ASIN / ISBN.
7. Try the LIPC properties above while a book is open; record output.
8. Write findings into `docs/findings-<model>-<firmware>.md`.
