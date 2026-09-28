# Device probe (first test on a real Kindle)

A KUAL extension. No SSH. It reads only; it changes nothing.

## What it checks
- Kernel, firmware, model code, CPU, tools on the device.
- `cc.db` schema (table/column names only).
- Two LIPC reader properties.
- Can a static Go binary start? Go 1.23 build (kernel 2.6.32+) and Go 1.26
  build (kernel 3.2+).
- Can it reach `api.hardcover.app` over HTTPS with bundled CA roots?

## Build
`./scripts/build-probe.sh` → `dist/hcprobe.zip`

## User steps
1. Connect the Kindle to the computer with USB.
2. Unzip `hcprobe.zip` into the Kindle drive root. You get
   `extensions/hcprobe/`. (The `extensions` folder already exists if KUAL is
   installed; merge into it.)
3. Eject the Kindle. Turn Wi-Fi on.
4. Open KUAL → Hardcover probe → "1. Run test". Wait for "done" at the top of
   the screen (up to 1 minute).
5. Connect USB. Send `hcprobe-report.txt` from the drive root.
6. Optional: "2. Copy reading DB to USB" → `hcprobe-cc.db`. It holds your
   book list. Share only if you want to.

## Watch (menu item 3)
Goal: find **when** the reader writes progress (`p_percentFinished`) to
`cc.db`, and when sidecar (`.sdr`) files change.

Runs 10 min in the background. Every 10 s it logs to
`/mnt/us/hcprobe-watch.txt`: time, power state, current book (first 8 chars of
key), percent, read state, last access, `cc.db*` file times, newest sidecar
file time. No titles.

User steps:
1. KUAL → Hardcover probe → "3. Watch reading (10 min)".
2. Open a book. Turn about 5 pages, 1 every 20 s.
3. Go back to Home. Wait 30 s.
4. Open the book again. Turn 2 pages.
5. Press power (sleep). Wait 30 s. Wake.
6. Wait until 10 min are done (or KUAL → "4. Stop watch").
7. Connect USB. Send `hcprobe-watch.txt`.

**UNVERIFIED:** the report part ran on one Kindle; the watch has not run on a
device yet. Older note: not yet run on any Kindle. HTTPS could not be tested in the
dev sandbox (TLS is intercepted there).
