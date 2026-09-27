# Open questions and risks

## Kindle side
- [ ] Which file/DB holds current position? Is it `cc.db`? Table/columns?
- [ ] When is it written: every page turn, on sleep, or on book close?
- [ ] Is it the same across old and new firmware?
- [ ] How to get ASIN / ISBN for sideloaded books (no ASIN)?
- [ ] Oldest Kindle model/firmware we will support? (Define the floor.)
- [ ] Daemon autostart method that survives reboot and firmware updates.
- [ ] Battery/Wi-Fi: send only when Wi-Fi is already up? Queue offline updates?

## Hardcover side
- [ ] Mutation for progress update. Percent or pages?
- [ ] Book/edition match by ASIN first, then ISBN, then title+author?
      If no confident match: skip, do not guess.
- [ ] Write scopes needed.
- [ ] Rate limits.
- [ ] Start a new read vs update current read (re-reads).
- [ ] Mark finished at 100%?

## Runtime risks
- Old firmware: old TLS, no Python. Decision: static Go ARM binary
  (`CGO_ENABLED=0`) with bundled CA certs.
- **Kernel floor.** Go 1.24+ needs Linux kernel 3.2+. Go 1.23 needs 2.6.32+.
  (Rust also needs 3.2+, so Rust does not fix this.) Kindle kernels
  (**UNVERIFIED**): Touch / PW1 about 2.6.31, PW2 / PW3 / Voyage about 3.0.35,
  newer models 4.x. Test: build a small "HTTPS GET" binary, run it on the real
  Kindle, record `uname -r`. Then pin the Go version.
- SQLite without cgo: use a pure-Go driver (e.g. `modernc.org/sqlite`).
  **UNVERIFIED** on linux/arm with old kernels.
- Kindle CPU arch per model (armel vs armhf). Build for the oldest.
- Token storage: no keychain on Kindle. Store in a file with tight permissions.
