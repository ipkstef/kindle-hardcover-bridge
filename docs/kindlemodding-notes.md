# KindleModding wiki — notes for this project (2026-10-01)

Source: kindlemodding.org (site blocked from the build machine; read from its
source repo github.com/KindleModding/kindlemodding.github.io, `content/`).
Pages read: kindle-dev/* (Scriptlets, Kindle SDK, GTK tutorial, Awesome
Window Manager, KPM: package + repository), kindle-hacking/appreg,
kindle-hacking/hotfix, kindle-hacking/usermode-boot-process.

## Scriptlets (launcher without KUAL)
- Part of the Universal Hotfix (WinterBreak era): `SH_Integration`
  (github.com/KindleModding/sh_integration). Any `.sh` file in
  `/mnt/us/documents` shows in the **library like a book**; a tap runs it.
- Header lines: `# Name:`, `# Author:`, `# Icon:` (path or base64 data URI),
  `# UseHooks` (functions `on_install`, `on_remove`, `on_run`),
  `# DontUseFBInk` (else stdout/stderr are shown on screen with FBInk).
- Registered in appreg.db as `com.notmarek.shell_integration.launcher`
  (command `/var/local/kmc/bin/sh_integration_launcher`, `maxGoTime` 60 …)
  and an extractor for `*.sh`.
- UNVERIFIED on the user's Kindle: is the Universal Hotfix / SH_Integration
  installed? (KUAL is, so the jailbreak is recent.)

## KPM — Kindle Package Manager (recommended distribution "as of hdnext")
- Package = folder → `.kpkg` via `kpm-helper.py`; `manifest.json`, optional
  `supported_platforms`: `kindle`, `kindle5`, `kindlepw2`, `kindlehf`.
- Hooks run from the unpacked package folder: `install.sh`,
  `uninstall.sh` (with `upgrade` on upgrade; must not delete files),
  `launch.sh`.
- Advice: `install.sh` puts a **scriptlet in `/mnt/us/documents`** that runs
  `/var/local/kmc/bin/kpm launch PACKAGENAME`.
- **Hooks MUST NOT write to rootfs or remount it rw** ("solution planned").
  → no `/etc/upstart` job from a KPM package today.
- Official repo: github.com/KindleModding/repo (pull request);
  example package: github.com/KindleModding/example_kpm_package.

## Boot / autostart
- Usermode boot uses **upstart**. The hotfix installs `kmc.conf` (runs on
  `framework_ready`): runs `/mnt/us/emergency.sh` if present, else fixes
  permissions of `/var/local/kmc`. No general "run my app at boot" hook.
- So autostart needs either our own upstart job (rootfs write: against KPM
  rules, lost on firmware update) or a future KPM mechanism.

## appreg.db (`/var/local/appreg.db`)
- Tables: `associations` (handler ↔ interface/content, e.g. extractor for
  `GL:*.sh`), `handlerIds`, `interfaces` (`application`, `extractor`, …),
  `extenstions` (sic), `mimetypes`, `properties` (per handler: `command`,
  `lipcId`, `extend-start`, `unloadPolicy`, `maxGoTime` …; also dconf).
- Registering an app gives a launchable handler; no boot start.

## Toolchains / platforms
- koxtoolchain targets: `kindle` (K2/DX/K3), `kindle5` (K4/Touch/PW1),
  `kindlepw2` (PW2+ on FW < 5.16.3), `kindlehf` (any Kindle on FW ≥ 5.16.3,
  armhf). Our **static Go binary (CGO off, GOARM=6) needs no libc**, so the
  soft-float / hard-float split does not apply to it. Kernel floor for Go is
  still open (open-questions).

## Window manager (awesome)
- Window titles are key/value: `L` layer (A app, C chrome, D dialog, KB, SS),
  `N` role (`application`, `dialog`, `pillowAlert` — blocks the Home
  button), `ID`, `PC` chrome, `M` modal (`dismissible`), `RC`, `HIDE` …
  Matches the titles in our logs (e.g. `L:D_N:dialog_…_A_ConfirmationDialog`).

## Not used by us
- Kindle SDK / GTK tutorial (native C++ GUI apps with Meson): we have no GUI
  beyond eips text and pillow alerts.
