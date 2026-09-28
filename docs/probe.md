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

**UNVERIFIED:** not yet run on any Kindle. HTTPS could not be tested in the
dev sandbox (TLS is intercepted there).
