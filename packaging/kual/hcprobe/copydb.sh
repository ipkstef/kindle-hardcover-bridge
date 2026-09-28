#!/bin/sh
# Copy the Kindle library DB to USB storage for research.
# It holds your book list and reading state. Share it only if you want to.
OUT=/mnt/us/hcprobe-cc.db
say() { command -v eips >/dev/null 2>&1 && eips 1 2 "$1                              "; }
if cp /var/local/cc.db "$OUT" 2>/dev/null; then
	sync
	say "Copied to hcprobe-cc.db. Connect USB."
else
	say "Copy failed: /var/local/cc.db not found"
fi
