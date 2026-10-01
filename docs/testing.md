# Testing

Goal: few device rounds. The user has one Kindle.

## On the build machine
- `go test -race ./...` before every build.
- **Replay fixtures** from the user's device (FW 5.17.1):
  - `internal/readers/testdata/cc-fw5.17.1.db` — real `Entries` schema, 3
    books (collation `icu` removed).
  - `internal/sidecar/testdata/*.azw3f` — Red Rising sidecar before and
    after a sleep (sleep research 2026-09-28).
  Add new device files here when a log shows a new format.

## On the device
- **Self-test** (KUAL menu): checks tools, inotify, free space, state DB,
  cc.db, latest book + sidecar, ratings file, clippings, Hardcover sign-in
  and the latest book's match (read-only), alert box. Result on screen and
  in `hcbridge-selftest.txt`.
- **Save log to USB**: header (version, daemon pid, DB counts, last
  self-test) + log. Each sleep / go-Home logs one line (update, or "no new
  position in cc.db … sidecar …"); other events are summed up hourly.
- **One checklist per build**: every build comes with one complete list of
  steps and the log lines that mean pass.
